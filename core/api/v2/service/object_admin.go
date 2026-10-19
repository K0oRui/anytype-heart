package v2service

// object_admin.go implements the object administration surface: the verbs
// that act on a whole object (duplicate, archive/restore, layout,
// set/collection conversion, source, bookmark refresh, icon and cover) and
// the bulk forms of type and property writes. Every method is a thin wrapper
// over one ClientCommands RPC. The two bulk writes are the exception: they
// hand a synthetic one-op PATCH body to PatchObject, so the property
// vocabulary (api-key slugs, value checking, output-only refusals, the layout
// conversion table) stays stated once.
//
// Archive, and only archive, carries the creator-provenance gate: it is the
// bulk form of DELETE /objects/{object_id}, so it must not become the way
// around §10. A batch is not atomic; a 200 reports what applied and what
// refused.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	apicore "github.com/anyproto/anytype-heart/core/api/core"
	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/core/block/restriction"
	"github.com/anyproto/anytype-heart/core/domain"
	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/anyblockjson"
	"github.com/anyproto/anytype-heart/pkg/lib/anyblockjson/storeresolver"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
)

// v2ObjectRpcError maps the object-administration refusals to the C6
// envelope. These RPCs classify almost nothing: NULL is success, BAD_INPUT is
// the only refusal code, and a restriction refusal arrives as UNKNOWN_ERROR
// with the sentinel in its description, which is a 403 rather than a 500.
func v2ObjectRpcError(op string, code, badInputCode int32, description string) error {
	if code == 0 {
		return nil
	}
	if code == badInputCode {
		return v2model.ValidationFailed(fmt.Sprintf("%s: invalid input", op),
			v2model.Issue{Message: description})
	}
	if strings.Contains(description, restriction.ErrRestricted.Error()) {
		return v2model.NewError(http.StatusForbidden, v2model.CodeForbidden,
			fmt.Sprintf("%s: %s", op, description))
	}
	msg := op + " failed"
	if description != "" {
		msg += ": " + description
	}
	return v2model.NewError(http.StatusInternalServerError, v2model.CodeInternalError, msg)
}

// objectTypeIs reports whether the object's type is one of the given keys.
// The layout a route needs to see is carried by the type: a set and a
// collection are types of their own, so asking a note for a set's source is
// caught here rather than in the editor.
func objectTypeIs(read apicore.ObjectRead, keys ...domain.TypeKey) bool {
	have := domain.TypeKey(objectTypeKey(read))
	for _, key := range keys {
		if have == key {
			return true
		}
	}
	return false
}

// checkIds refuses a batch whose ids cannot name an object. A blank entry can
// never resolve, so it is a malformed request rather than a row in the
// per-object report, which would hide a client bug behind a "not found".
func checkIds(ids []string) error {
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			return v2model.ValidationFailed("an id in the batch is empty",
				v2model.Issue{Path: fmt.Sprintf("/ids/%d", i), Message: "ids must name objects"})
		}
	}
	return nil
}

// typeSlugOf spells an object's type the way every read serves it.
func (s *Service) typeSlugOf(spaceId string, read apicore.ObjectRead) string {
	return s.apiKeys(spaceId, storeresolver.New(s.store.SpaceIndex(spaceId))).TypeSlug(objectTypeKey(read))
}

// DuplicateObject implements POST
// /v2/spaces/{space_id}/objects/{object_id}/duplicate. The copy is a create:
// it gets a new id and the source's type, and the source is left untouched.
func (s *Service) DuplicateObject(ctx context.Context, spaceId, objectId string, dryRun bool) (*v2model.CreateResult, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	result := &v2model.CreateResult{Id: objectId, Type: s.typeSlugOf(spaceId, read)}
	if dryRun {
		result.DryRun = true
		return result, nil
	}
	resp := s.mw.ObjectDuplicate(ctx, &pb.RpcObjectDuplicateRequest{ContextId: objectId})
	if resp.Error != nil {
		if err := v2ObjectRpcError("duplicate object", int32(resp.Error.Code),
			int32(pb.RpcObjectDuplicateResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	if resp.Id == "" {
		return nil, fmt.Errorf("duplicate object %s: the RPC returned no id", objectId)
	}
	result.Id = resp.Id
	return result, nil
}

// SetObjectsArchived implements POST /v2/spaces/{space_id}/objects/archive:
// archive when archived is true (the default), restore when it is false. It
// is DELETE's bulk form, so every id passes DELETE's gate (existence, the
// schema steer, the user-content allowlist and creator provenance) before
// anything is written.
func (s *Service) SetObjectsArchived(ctx context.Context, spaceId string, req v2model.SetObjectsArchivedRequest, dryRun bool) (*v2model.BulkResult, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Ids) == 0 {
		return nil, v2model.ValidationFailed("archive needs at least one object",
			v2model.Issue{Path: "/ids", Message: "ids is empty"})
	}
	if err := checkIds(req.Ids); err != nil {
		return nil, err
	}
	archived := true
	if req.Archived != nil {
		archived = *req.Archived
	}
	result := &v2model.BulkResult{}
	for _, objectId := range req.Ids {
		if err := s.archiveGate(ctx, spaceId, objectId); err != nil {
			result.Failed = append(result.Failed, v2model.BulkFailure{Id: objectId, Message: err.Error()})
			continue
		}
		// already in the requested state: a no-op receipt, so a retry and a
		// bulk call that overlaps the Bin agree
		row, rowErr := s.store.SpaceIndex(spaceId).GetDetails(objectId)
		if rowErr == nil && row.GetBool(bundle.RelationKeyIsArchived) == archived {
			result.Ids = append(result.Ids, objectId)
			continue
		}
		if dryRun {
			result.Ids = append(result.Ids, objectId)
			continue
		}
		resp := s.mw.ObjectSetIsArchived(ctx, &pb.RpcObjectSetIsArchivedRequest{
			ContextId:  objectId,
			IsArchived: archived,
		})
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectSetIsArchivedResponseError_NULL {
			msg := resp.Error.Description
			if msg == "" {
				msg = "the archive RPC refused"
			}
			result.Failed = append(result.Failed, v2model.BulkFailure{Id: objectId, Message: msg})
			continue
		}
		result.Ids = append(result.Ids, objectId)
	}
	result.DryRun = dryRun
	return result, nil
}

// archiveGate is DELETE's predicate, per object: the checks a bulk archive
// must not skip. It returns the C6 error the single-object route would have
// answered, and the bulk result carries its message.
func (s *Service) archiveGate(ctx context.Context, spaceId, objectId string) error {
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return mapReadError(spaceId, objectId, err)
	}
	row, rowErr := s.store.SpaceIndex(spaceId).GetDetails(objectId)
	if rowErr == nil && row.GetBool(bundle.RelationKeyIsDeleted) {
		return v2model.NotFound(fmt.Sprintf("object %q not found in space %q", objectId, spaceId))
	}
	if err := steerSchemaDelete(read.SbType, spaceId); err != nil {
		return err
	}
	if !deletableSbType(read.SbType) {
		return v2model.NewError(http.StatusForbidden, v2model.CodeForbidden,
			fmt.Sprintf("%s objects are not archivable through the API: this route serves user content only (pages, templates, files, chats). "+
				"This is a system or derived surface; manage it in the Anytype app.", read.SbType.String()),
			v2model.Issue{Path: "/ids", Message: fmt.Sprintf("object %s is a %s", objectId, read.SbType.String())})
	}
	return s.checkDeleteProvenance(ctx, spaceId, objectId)
}

// SetObjectsType implements PATCH /v2/spaces/{space_id}/objects/type. The
// type is resolved once, so an unknown one is a 400 on /type rather than N
// identical refusals; the conversion itself runs as one set_type op per
// object through PatchObject, which owns the layout table and the
// restriction checks.
func (s *Service) SetObjectsType(ctx context.Context, spaceId string, req v2model.SetObjectsTypeRequest, dryRun bool) (*v2model.BulkResult, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Ids) == 0 {
		return nil, v2model.ValidationFailed("set type needs at least one object",
			v2model.Issue{Path: "/ids", Message: "ids is empty"})
	}
	if err := checkIds(req.Ids); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Type) == "" {
		return nil, v2model.ValidationFailed("set type needs a type",
			v2model.Issue{Path: "/type", Message: "type is empty"})
	}
	if _, err := s.requireLiveType(spaceId, req.Type, "/type", errKeysFor(ctx)); err != nil {
		return nil, err
	}
	body, err := oneOpEnvelope(map[string]any{"op": "set_type", "type": req.Type})
	if err != nil {
		return nil, err
	}
	return s.applyBulk(ctx, spaceId, req.Ids, body, dryRun), nil
}

// SetObjectsProperties implements PATCH /v2/spaces/{space_id}/objects. The
// body's four fields are the set_properties op's, and each object's write
// goes through PatchObject as one such op, with the same vocabulary, the same
// refusals, the same dry-run verdict as a PATCH on that object.
func (s *Service) SetObjectsProperties(ctx context.Context, spaceId string, req v2model.SetObjectsPropertiesRequest, dryRun bool) (*v2model.BulkResult, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Ids) == 0 {
		return nil, v2model.ValidationFailed("set properties needs at least one object",
			v2model.Issue{Path: "/ids", Message: "ids is empty"})
	}
	if err := checkIds(req.Ids); err != nil {
		return nil, err
	}
	op := map[string]any{"op": "set_properties"}
	if len(req.Set) > 0 {
		op["set"] = req.Set
	}
	if len(req.Unset) > 0 {
		op["unset"] = req.Unset
	}
	if len(req.Add) > 0 {
		op["add"] = req.Add
	}
	if len(req.Remove) > 0 {
		op["remove"] = req.Remove
	}
	if len(op) == 1 {
		return nil, v2model.ValidationFailed("set properties needs at least one of set, unset, add, remove",
			v2model.Issue{Message: "set, unset, add and remove are all empty"})
	}
	body, err := oneOpEnvelope(op)
	if err != nil {
		return nil, err
	}
	return s.applyBulk(ctx, spaceId, req.Ids, body, dryRun), nil
}

// oneOpEnvelope marshals a single op into the PATCH body that carries it.
func oneOpEnvelope(op map[string]any) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"ops": []any{op}})
	if err != nil {
		return nil, fmt.Errorf("encode op: %w", err)
	}
	return body, nil
}

// applyBulk runs one synthesized PATCH per object and reports both sides.
func (s *Service) applyBulk(ctx context.Context, spaceId string, ids []string, body []byte, dryRun bool) *v2model.BulkResult {
	result := &v2model.BulkResult{DryRun: dryRun}
	for _, objectId := range ids {
		if _, err := s.PatchObject(ctx, spaceId, objectId, body, "", dryRun, false); err != nil {
			result.Failed = append(result.Failed, v2model.BulkFailure{Id: objectId, Message: err.Error()})
			continue
		}
		result.Ids = append(result.Ids, objectId)
	}
	return result
}

// SetObjectLayout implements PATCH
// /v2/spaces/{space_id}/objects/{object_id}/layout.
func (s *Service) SetObjectLayout(ctx context.Context, spaceId, objectId string, req v2model.SetObjectLayoutRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if _, err := s.reader.ReadObject(ctx, spaceId, objectId); err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	layout, ok := parseObjectLayout(req.Layout)
	if !ok {
		return nil, v2model.ValidationFailed("unknown layout",
			v2model.Issue{Path: "/layout",
				Message: fmt.Sprintf("%q is not a layout name", req.Layout)}.
				WithHint(v2model.Hintf("common layouts: basic, todo, note, profile, bookmark; sets and collections are made with %s",
					v2model.NewRef(v2model.OpConvertObject, "space_id", spaceId, "object_id", objectId))))
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectSetLayout(ctx, &pb.RpcObjectSetLayoutRequest{ContextId: objectId, Layout: layout})
	if resp.Error != nil {
		if err := v2ObjectRpcError("set object layout", int32(resp.Error.Code),
			int32(pb.RpcObjectSetLayoutResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// parseObjectLayout resolves a layout name case-insensitively, so the media
// layouts spelled "objectType" and "relationOptionsList" stay addressable
// without a second vocabulary.
func parseObjectLayout(name string) (model.ObjectTypeLayout, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for layoutName, value := range model.ObjectTypeLayout_value {
		if strings.ToLower(layoutName) == want {
			return model.ObjectTypeLayout(value), true
		}
	}
	return 0, false
}

// ConvertObject implements POST
// /v2/spaces/{space_id}/objects/{object_id}/convert: turn an object into a
// set (with a source) or a collection.
func (s *Service) ConvertObject(ctx context.Context, spaceId, objectId string, req v2model.ConvertObjectRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	if objectTypeIs(read, bundle.TypeKeySet, bundle.TypeKeyCollection) {
		return nil, v2model.ValidationFailed("this object is already a set or a collection",
			v2model.Issue{Path: "/to", Message: fmt.Sprintf("the object's type is %s", objectTypeKey(read)),
				Hint: "change what it holds instead: a set takes a source, a collection takes items"})
	}
	switch req.To {
	case "set":
		if len(req.Source) == 0 {
			return nil, v2model.ValidationFailed("a set needs a source",
				v2model.Issue{Path: "/source", Message: "name at least one type the set is based on"})
		}
		source, err := s.typeSourceIds(ctx, spaceId, req.Source, "/source")
		if err != nil {
			return nil, err
		}
		if dryRun {
			return &v2model.AcceptResponse{DryRun: true}, nil
		}
		resp := s.mw.ObjectToSet(ctx, &pb.RpcObjectToSetRequest{ContextId: objectId, Source: source})
		if resp.Error != nil {
			if err := v2ObjectRpcError("convert object to set", int32(resp.Error.Code),
				int32(pb.RpcObjectToSetResponseError_BAD_INPUT), resp.Error.Description); err != nil {
				return nil, err
			}
		}
	case "collection":
		if len(req.Source) > 0 {
			return nil, v2model.ValidationFailed("a collection has no source",
				v2model.Issue{Path: "/source", Message: "drop source; collections are assembled by adding items"})
		}
		if dryRun {
			return &v2model.AcceptResponse{DryRun: true}, nil
		}
		resp := s.mw.ObjectToCollection(ctx, &pb.RpcObjectToCollectionRequest{ContextId: objectId})
		if resp.Error != nil {
			if err := v2ObjectRpcError("convert object to collection", int32(resp.Error.Code),
				int32(pb.RpcObjectToCollectionResponseError_BAD_INPUT), resp.Error.Description); err != nil {
				return nil, err
			}
		}
	default:
		return nil, v2model.ValidationFailed("unknown conversion",
			v2model.Issue{Path: "/to", Message: fmt.Sprintf("%q is not a conversion target", req.To),
				Hint: "to is \"set\" or \"collection\""})
	}
	return &v2model.AcceptResponse{}, nil
}

// typeSourceIds resolves served type spellings to the ids the editor's set
// source is stored as. It is the type object's own id, not its key spelled as
// a URL: the dataview the set is built into resolves each source by looking
// the id up, and a set the app made holds the same form.
func (s *Service) typeSourceIds(ctx context.Context, spaceId string, inputs []string, path string) ([]string, error) {
	out := make([]string, 0, len(inputs))
	for i, input := range inputs {
		if strings.TrimSpace(input) == "" {
			return nil, v2model.ValidationFailed("a source entry is empty",
				v2model.Issue{Path: fmt.Sprintf("%s/%d", path, i), Message: "each source entry names a type"})
		}
		entry, err := s.requireLiveType(spaceId, input, path, errKeysFor(ctx))
		if err != nil {
			return nil, err
		}
		out = append(out, entry.Id)
	}
	return out, nil
}

// SetObjectSource implements PATCH
// /v2/spaces/{space_id}/objects/{object_id}/source: the types a set is based
// on, replacing the current ones.
func (s *Service) SetObjectSource(ctx context.Context, spaceId, objectId string, req v2model.SetObjectSourceRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	if !objectTypeIs(read, bundle.TypeKeySet) {
		return nil, v2model.ValidationFailed("only a set has a source",
			v2model.Issue{Path: "object_id", Message: fmt.Sprintf("the object's type is %s, not a set", objectTypeKey(read))}.
				WithHint(v2model.Hintf("convert it first with %s",
					v2model.NewRef(v2model.OpConvertObject, "space_id", spaceId, "object_id", objectId))))
	}
	if len(req.Source) == 0 {
		return nil, v2model.ValidationFailed("a set needs a source",
			v2model.Issue{Path: "/source", Message: "name at least one type the set is based on"})
	}
	source, err := s.typeSourceIds(ctx, spaceId, req.Source, "/source")
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectSetSource(ctx, &pb.RpcObjectSetSourceRequest{ContextId: objectId, Source: source})
	if resp.Error != nil {
		if err := v2ObjectRpcError("set object source", int32(resp.Error.Code),
			int32(pb.RpcObjectSetSourceResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// RefreshObject implements POST
// /v2/spaces/{space_id}/objects/{object_id}/refresh: refetch the link a
// bookmark was made from, into the object's stored source. The fetch runs
// after the answer, so this is a 202 and the receipt says only that it
// started.
func (s *Service) RefreshObject(ctx context.Context, spaceId, objectId string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	source := ""
	if read.Snapshot != nil {
		source = pbtypes.GetString(read.Snapshot.Details, bundle.RelationKeySource.String())
	}
	if strings.TrimSpace(source) == "" {
		return nil, v2model.ValidationFailed("this object has no source link to refresh",
			v2model.Issue{Path: "object_id", Message: "the source property is empty"}.
				WithHint(v2model.Hintf("create a bookmark with %s, or set source first",
					v2model.NewRef(v2model.OpCreateObjectFromUrl, "space_id", spaceId))))
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectBookmarkFetch(ctx, &pb.RpcObjectBookmarkFetchRequest{ContextId: objectId, Url: source})
	if resp.Error != nil {
		if err := v2ObjectRpcError("refresh bookmark", int32(resp.Error.Code),
			int32(pb.RpcObjectBookmarkFetchResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// CreateObjectFromUrl implements POST /v2/spaces/{space_id}/objects/from-url.
// The fetch is synchronous: the created object already carries the link's
// title, description and image when the answer arrives.
func (s *Service) CreateObjectFromUrl(ctx context.Context, spaceId string, req v2model.CreateObjectFromUrlRequest, dryRun bool) (*v2model.CreateResult, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Url) == "" {
		return nil, v2model.ValidationFailed("a url is required",
			v2model.Issue{Path: "/url", Message: "url is empty"})
	}
	typeInput := req.Type
	if strings.TrimSpace(typeInput) == "" {
		typeInput = string(bundle.TypeKeyBookmark)
	}
	entry, err := s.requireLiveType(spaceId, typeInput, "/type", errKeysFor(ctx))
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.CreateResult{
			Type:   s.apiKeys(spaceId, storeresolver.New(s.store.SpaceIndex(spaceId))).TypeSlug(entry.Key),
			DryRun: true,
		}, nil
	}
	resp := s.mw.ObjectCreateFromUrl(ctx, &pb.RpcObjectCreateFromUrlRequest{
		SpaceId:             spaceId,
		ObjectTypeUniqueKey: domain.TypeKey(entry.Key).URL(),
		Url:                 req.Url,
		AddPageContent:      req.AddPageContent,
		TemplateId:          req.Template,
	})
	if resp.Error != nil {
		if err := v2ObjectRpcError("create object from url", int32(resp.Error.Code),
			int32(pb.RpcObjectCreateFromUrlResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	result := &v2model.CreateResult{Id: resp.ObjectId}
	if read, err := s.reader.ReadObject(ctx, spaceId, resp.ObjectId); err == nil {
		result.Type = s.typeSlugOf(spaceId, read)
		result.Etag = ComputeEtag(read.Heads)
	}
	return result, nil
}

// SetObjectIcon implements PUT
// /v2/spaces/{space_id}/objects/{object_id}/icon. The icon is a typed
// envelope the format owns, so the variant is built here and validated by
// the same importer the type writer uses.
func (s *Service) SetObjectIcon(ctx context.Context, spaceId, objectId string, req v2model.ObjectIconRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if _, err := s.reader.ReadObject(ctx, spaceId, objectId); err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	icon, err := iconEnvelope(req)
	if err != nil {
		return nil, err
	}
	details, err := iconDetailsForObject(icon)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectSetDetails(ctx, &pb.RpcObjectSetDetailsRequest{ContextId: objectId, Details: details})
	if resp.Error != nil {
		if err := v2ObjectRpcError("set object icon", int32(resp.Error.Code),
			int32(pb.RpcObjectSetDetailsResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// SetObjectCover implements PUT
// /v2/spaces/{space_id}/objects/{object_id}/cover.
func (s *Service) SetObjectCover(ctx context.Context, spaceId, objectId string, req v2model.ObjectCoverRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if _, err := s.reader.ReadObject(ctx, spaceId, objectId); err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	cover, err := coverEnvelope(req)
	if err != nil {
		return nil, err
	}
	details, err := coverPatchDetails(cover)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectSetDetails(ctx, &pb.RpcObjectSetDetailsRequest{ContextId: objectId, Details: details})
	if resp.Error != nil {
		if err := v2ObjectRpcError("set object cover", int32(resp.Error.Code),
			int32(pb.RpcObjectSetDetailsResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// iconEnvelope builds the typed icon from the flat request, refusing a
// variant that is missing the member it requires. A colour is a tint on
// every variant but "color", where it is the icon itself.
func iconEnvelope(req v2model.ObjectIconRequest) (json.RawMessage, error) {
	m := map[string]any{"format": req.Format}
	switch req.Format {
	case "emoji":
		if req.Emoji == "" {
			return nil, v2model.ValidationFailed("icon needs an emoji",
				v2model.Issue{Path: "/emoji", Message: "format emoji requires emoji"})
		}
		m["emoji"] = req.Emoji
	case "file":
		if req.File == "" {
			return nil, v2model.ValidationFailed("icon needs a file",
				v2model.Issue{Path: "/file", Message: "format file requires the id of an image object in this space"})
		}
		m["file"] = req.File
	case "icon":
		if req.Name == "" {
			return nil, v2model.ValidationFailed("icon needs a name",
				v2model.Issue{Path: "/name", Message: "format icon requires name"})
		}
		m["name"] = req.Name
	case "color":
		if req.Color == "" {
			return nil, v2model.ValidationFailed("icon needs a colour",
				v2model.Issue{Path: "/color", Message: "format color requires color"})
		}
		m["color"] = req.Color
	default:
		return nil, v2model.ValidationFailed("unknown icon format",
			v2model.Issue{Path: "/format", Message: fmt.Sprintf("%q is not an icon format", req.Format),
				Hint: "format is emoji, file, icon or color"})
	}
	if req.Color != "" && req.Format != "color" {
		m["color"] = req.Color
	}
	return json.Marshal(m)
}

// coverEnvelope builds the typed cover from the flat request.
func coverEnvelope(req v2model.ObjectCoverRequest) (json.RawMessage, error) {
	m := map[string]any{"format": req.Format}
	switch req.Format {
	case "image":
		if req.File == "" {
			return nil, v2model.ValidationFailed("cover needs an image",
				v2model.Issue{Path: "/file", Message: "format image requires the id of an image object in this space"})
		}
		m["file"] = req.File
		if req.Scale != 0 {
			m["scale"] = req.Scale
		}
		if req.X != 0 {
			m["x"] = req.X
		}
		if req.Y != 0 {
			m["y"] = req.Y
		}
	case "color":
		if req.Color == "" {
			return nil, v2model.ValidationFailed("cover needs a colour",
				v2model.Issue{Path: "/color", Message: "format color requires color"})
		}
		m["color"] = req.Color
	case "gradient":
		if req.Gradient == "" {
			return nil, v2model.ValidationFailed("cover needs a gradient",
				v2model.Issue{Path: "/gradient", Message: "format gradient requires gradient"})
		}
		m["gradient"] = req.Gradient
	default:
		return nil, v2model.ValidationFailed("unknown cover format",
			v2model.Issue{Path: "/format", Message: fmt.Sprintf("%q is not a cover format", req.Format),
				Hint: "format is image, color or gradient"})
	}
	return json.Marshal(m)
}

// iconDetailsForObject is iconPatchDetails with the probe document's /icon
// prefix dropped: this route's body IS the icon, so an issue belongs on the
// member the caller sent (/color, not /icon/color).
func iconDetailsForObject(icon json.RawMessage) ([]*model.Detail, error) {
	details, err := iconPatchDetails(icon)
	if err == nil {
		return details, nil
	}
	var apiErr *v2model.Error
	if !errors.As(err, &apiErr) {
		return nil, err
	}
	for i := range apiErr.Issues {
		path := strings.TrimPrefix(apiErr.Issues[i].Path, "/icon")
		if path == "" {
			path = "/"
		}
		apiErr.Issues[i].Path = path
	}
	return nil, apiErr
}

// coverPatchDetails turns the typed cover into the stored detail keys it
// implies, the way iconPatchDetails does for an icon: the format's importer
// states the variant rules once. Only cover keys are kept, so nothing the
// probe envelope carries for its own sake can leak into the patch.
func coverPatchDetails(cover json.RawMessage) ([]*model.Detail, error) {
	doc, err := encodeEnvelope(map[string]json.RawMessage{
		"formatVersion": json.RawMessage(`"` + anyblockjson.FormatVersion + `"`),
		"kind":          json.RawMessage(`"object_type"`),
		"cover":         cover,
	})
	if err != nil {
		return nil, err
	}
	if err := anyblockjson.Validate(doc); err != nil {
		return nil, memberPatchError(err, "cover", "cover")
	}
	_, snapshot, err := anyblockjson.Unmarshal(doc, anyblockjson.Options{})
	if err != nil {
		return nil, memberPatchError(err, "cover", "cover")
	}
	var out []*model.Detail
	for _, key := range sortedKeys(snapshot.GetDetails().GetFields()) {
		if !strings.HasPrefix(key, "cover") {
			continue
		}
		out = append(out, &model.Detail{Key: key, Value: snapshot.Details.Fields[key]})
	}
	if len(out) == 0 {
		return nil, v2model.ValidationFailed("cover sets nothing",
			v2model.Issue{Path: "/cover", Message: "the cover carries no value the store can hold"})
	}
	return out, nil
}

// memberPatchError re-addresses a probe document's issues onto the request
// member the caller actually sent.
func memberPatchError(err error, member, what string) error {
	var ve *anyblockjson.ValidationError
	if !errors.As(err, &ve) {
		return v2model.ValidationFailed("invalid "+what, v2model.Issue{Path: "/" + member, Message: err.Error()})
	}
	issues := make([]v2model.Issue, 0, len(ve.Issues))
	for _, iss := range ve.Issues {
		path := "/" + member
		if trimmed := strings.TrimPrefix(iss.Path, "/"+member); trimmed != iss.Path && trimmed != "" {
			path += trimmed
		}
		issues = append(issues, v2model.Issue{Path: path, Message: iss.Message})
	}
	return v2model.ValidationFailed("invalid "+what, issues...)
}

// SetObjectItemsOrder implements PUT
// /v2/spaces/{space_id}/objects/{object_id}/items: the complete new order of
// a collection's items.
func (s *Service) SetObjectItemsOrder(ctx context.Context, spaceId, objectId string, req v2model.SetObjectItemsRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Ids) == 0 {
		return nil, v2model.ValidationFailed("an item order needs at least one id",
			v2model.Issue{Path: "/ids", Message: "ids is empty"})
	}
	if err := checkIds(req.Ids); err != nil {
		return nil, err
	}
	read, err := s.reader.ReadObject(ctx, spaceId, objectId)
	if err != nil {
		return nil, mapReadError(spaceId, objectId, err)
	}
	if !objectTypeIs(read, bundle.TypeKeyCollection) {
		return nil, v2model.ValidationFailed("only a collection has an item order",
			v2model.Issue{Path: "object_id", Message: fmt.Sprintf("the object's type is %s, not a collection", objectTypeKey(read))}.
				WithHint(v2model.Hintf("convert it first with %s",
					v2model.NewRef(v2model.OpConvertObject, "space_id", spaceId, "object_id", objectId))))
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectCollectionSort(ctx, &pb.RpcObjectCollectionSortRequest{ContextId: objectId, ObjectIds: req.Ids})
	if resp.Error != nil {
		if err := v2ObjectRpcError("set item order", int32(resp.Error.Code),
			int32(pb.RpcObjectCollectionSortResponseError_BAD_INPUT), resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}
