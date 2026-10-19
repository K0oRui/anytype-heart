package v2service

// schema_admin.go implements the property and type maintenance surface: the
// select-option verbs (remove one option, set the picker order), the reverse
// lookup that names the properties carrying a value, and the type verbs that
// report and resolve the properties a type's objects carry beyond its own
// recommendations, plus the space's type order.
//
// RelationListRemoveOption deletes an option OBJECT by id, so its route
// resolves the caller's spelling against the property's live options first
// and forwards exactly the id it found; an id the caller made up never
// reaches the RPC. Property VALUES keep addressing options by name (C2);
// these routes accept either spelling, and the option list route serves both.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/anyproto/anytype-heart/core/api/pagination"
	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/core/domain"
	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/anyblockjson"
	"github.com/anyproto/anytype-heart/pkg/lib/database"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
)

// schemaBadInput is the BAD_INPUT code the relation and type maintenance RPCs
// share; the only code of theirs that is not NULL.
const schemaBadInput = int32(pb.RpcObjectTypeListConflictingRelationsResponseError_BAD_INPUT)

// v2SchemaRpcError is v2ObjectRpcError for the RPCs of this file, with one
// extra refusal: a readonly bundled type arrives as its own code and is a
// 403, because no different request from this caller can fix it.
func v2SchemaRpcError(op string, code, readonlyCode int32, description string) error {
	if code != 0 && readonlyCode != 0 && code == readonlyCode {
		return v2model.NewError(http.StatusForbidden, v2model.CodeForbidden,
			fmt.Sprintf("%s: %s", op, description))
	}
	return v2ObjectRpcError(op, code, schemaBadInput, description)
}

// optionEntry is one live option of a select property: the id the option
// routes pass on, the text they serve, the picker color and the stored
// lexicographic position.
type optionEntry struct {
	Id    string
	Name  string
	Color string
	Order string
}

// selectLikeFormat reports whether a property's format carries options: the
// select (status) and multiSelect (tag) formats.
func selectLikeFormat(format model.RelationFormat) bool {
	return format == model.RelationFormat_status || format == model.RelationFormat_tag
}

// liveOptions reads the options of one live property in the order the picker
// shows them: the option object's own lexid, with an option that has none yet
// trailing the ordered ones, by name.
func (s *Service) liveOptions(spaceId string, entry propertyEntry) ([]optionEntry, error) {
	options, err := s.store.SpaceIndex(spaceId).ListRelationOptions(domain.RelationKey(entry.Key))
	if err != nil {
		return nil, fmt.Errorf("list the options of property %s: %w", entry.Key, err)
	}
	entries := make([]optionEntry, 0, len(options))
	for _, option := range options {
		if option == nil {
			continue
		}
		entries = append(entries, optionEntry{
			Id: option.Id, Name: option.Text, Color: option.Color, Order: option.OrderId,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Order == entries[j].Order {
			return entries[i].Name < entries[j].Name
		}
		if entries[i].Order == "" {
			return false
		}
		if entries[j].Order == "" {
			return true
		}
		return entries[i].Order < entries[j].Order
	})
	return entries, nil
}

// resolveOption turns an option spelling into one of the property's live
// options: an exact id wins, then a name. The name pass folds case, the way
// the key layers do, and an exact spelling wins over the folded ones it sits
// beside. A name two options still share is refused with both ids rather than
// guessed at, because the ids are what disambiguate them on this surface.
func resolveOption(spaceId, propertyKey, input, path string, options []optionEntry) (optionEntry, error) {
	for _, option := range options {
		if option.Id != "" && option.Id == input {
			return option, nil
		}
	}
	var named []optionEntry
	for _, option := range options {
		if strings.EqualFold(option.Name, input) {
			named = append(named, option)
		}
	}
	if len(named) > 1 {
		var exact []optionEntry
		for _, option := range named {
			if option.Name == input {
				exact = append(exact, option)
			}
		}
		if len(exact) == 1 {
			return exact[0], nil
		}
	}
	if len(named) == 1 {
		return named[0], nil
	}
	if len(named) > 1 {
		ids := make([]string, 0, len(named))
		for _, option := range named {
			ids = append(ids, option.Id)
		}
		return optionEntry{}, v2model.AmbiguousInput(
			fmt.Sprintf("option %q is ambiguous", input),
			v2model.Issue{Path: path,
				Message: fmt.Sprintf("%q names %d options", input, len(named)),
				Hint:    "address the intended one by its id, one of " + strings.Join(ids, ", ")})
	}
	return optionEntry{}, v2model.NotFound(
		fmt.Sprintf("option %q is not an option of property %s", input, propertyKey),
		v2model.Issue{Path: path,
			Message: "the property's live options are " + optionNames(options)}.WithHint(
			v2model.Hintf("the options are listed by %s", v2model.RefListPropertyOptions(spaceId, propertyKey))))
}

// optionNames renders an option vocabulary for a refusal, capped so a
// thousand-option tag property cannot turn a refusal into a document.
func optionNames(options []optionEntry) string {
	const maxNames = 12
	names := make([]string, 0, maxNames)
	for _, option := range options {
		if len(names) == maxNames {
			names = append(names, fmt.Sprintf("and %d more", len(options)-maxNames))
			break
		}
		names = append(names, fmt.Sprintf("%q", option.Name))
	}
	if len(names) == 0 {
		return "empty"
	}
	return strings.Join(names, ", ")
}

// optionUsage counts the objects that hold an option, the check
// RelationListRemoveOption runs itself when check_in_objects is on. The dry
// run and the refusal need the number before the RPC is called, so the count
// is taken here either way.
func (s *Service) optionUsage(spaceId string, entry propertyEntry, optionId string) (int, error) {
	records, err := s.store.SpaceIndex(spaceId).Query(database.Query{Filters: []database.FilterRequest{{
		RelationKey: domain.RelationKey(entry.Key),
		Condition:   model.BlockContentDataviewFilter_Equal,
		Value:       domain.String(optionId),
	}}})
	if err != nil {
		return 0, fmt.Errorf("count the objects holding option %s: %w", optionId, err)
	}
	return len(records), nil
}

// RemovePropertyOption implements DELETE
// /v2/spaces/{space_id}/properties/{key}/options/{option}: one option leaves
// the property's picker.
//
// An option objects still point at is refused with a 409 unless
// delete_in_objects is set, because the objects keep the value while the
// property stops offering it. delete_in_objects is the caller saying they
// know.
func (s *Service) RemovePropertyOption(ctx context.Context, spaceId, propertyKey, option string, deleteInObjects, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	entry, err := s.requireLiveProperty(spaceId, propertyKey, errKeysFor(ctx))
	if err != nil {
		return nil, err
	}
	if !selectLikeFormat(entry.Format) {
		return nil, v2model.ValidationFailed(
			fmt.Sprintf("property %s has no options to remove", propertyKey),
			v2model.Issue{Path: "/key",
				Message: fmt.Sprintf("its format is %s", anyblockjson.FormatName(entry.Format))})
	}
	options, err := s.liveOptions(spaceId, entry)
	if err != nil {
		return nil, err
	}
	target, err := resolveOption(spaceId, entry.Key, option, "/option", options)
	if err != nil {
		return nil, err
	}
	used, err := s.optionUsage(spaceId, entry, target.Id)
	if err != nil {
		return nil, err
	}
	if used > 0 && !deleteInObjects {
		return nil, v2model.NewError(http.StatusConflict, v2model.CodeConflict,
			fmt.Sprintf("option %q is held by %d object(s)", target.Name, used),
			v2model.Issue{Path: "/option",
				Message: "removing it leaves those objects with a value the property no longer offers"}.
				WithHint(v2model.Plain("pass delete_in_objects=true to remove the option anyway")))
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.RelationListRemoveOption(ctx, &pb.RpcRelationListRemoveOptionRequest{
		OptionIds: []string{target.Id},
		// the check just ran here; the RPC's own copy would only repeat it
		CheckInObjects: false,
	})
	if err := v2SchemaRpcError("remove option", int32(resp.GetError().GetCode()), 0, resp.GetError().GetDescription()); err != nil {
		return nil, err
	}
	return &v2model.AcceptResponse{}, nil
}

// SetPropertyOptionsOrder implements PUT
// /v2/spaces/{space_id}/properties/{key}/options/order. The order is the
// picker's, not the list route's: the option list sorts by name, and this is
// what the answer carries back in the store's order.
func (s *Service) SetPropertyOptionsOrder(ctx context.Context, spaceId, propertyKey string, req v2model.SetPropertyOptionsOrderRequest, dryRun bool) (*v2model.PropertyOptionsOrder, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	entry, err := s.requireLiveProperty(spaceId, propertyKey, errKeysFor(ctx))
	if err != nil {
		return nil, err
	}
	if !selectLikeFormat(entry.Format) {
		return nil, v2model.ValidationFailed(
			fmt.Sprintf("property %s has no options to order", propertyKey),
			v2model.Issue{Path: "/key",
				Message: fmt.Sprintf("its format is %s", anyblockjson.FormatName(entry.Format))})
	}
	if len(req.Options) == 0 {
		return nil, v2model.ValidationFailed("an option order is required",
			v2model.Issue{Path: "/options", Message: "options is empty"})
	}
	options, err := s.liveOptions(spaceId, entry)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(req.Options))
	rows := make([]v2model.OptionRow, 0, len(req.Options))
	seen := make(map[string]bool, len(req.Options))
	for i, input := range req.Options {
		path := fmt.Sprintf("/options/%d", i)
		target, err := resolveOption(spaceId, entry.Key, input, path, options)
		if err != nil {
			return nil, err
		}
		if seen[target.Id] {
			return nil, v2model.ValidationFailed(
				fmt.Sprintf("option %q is named twice", input),
				v2model.Issue{Path: path, Message: "each option takes one place in the order"})
		}
		seen[target.Id] = true
		ids = append(ids, target.Id)
		rows = append(rows, v2model.OptionRow{Id: target.Id, Name: target.Name, Color: target.Color})
	}
	// the order is the WHOLE order: an option left out would keep its own
	// lexicographic place and interleave with the ones given, which is not
	// what a list that reads as a replacement means
	var missing []string
	for _, option := range options {
		if !seen[option.Id] {
			missing = append(missing, fmt.Sprintf("%q", option.Name))
		}
	}
	if len(missing) > 0 {
		return nil, v2model.ValidationFailed("the order leaves options out",
			v2model.Issue{Path: "/options",
				Message: "missing " + strings.Join(missing, ", ")}.WithHint(
				v2model.Hintf("the property's options are listed by %s",
					v2model.RefListPropertyOptions(spaceId, propertyKey))))
	}
	if dryRun {
		return &v2model.PropertyOptionsOrder{DryRun: true, Options: rows}, nil
	}
	resp := s.mw.RelationOptionSetOrder(ctx, &pb.RpcRelationOptionSetOrderRequest{
		SpaceId:             spaceId,
		RelationKey:         entry.Key,
		RelationOptionOrder: ids,
	})
	if err := v2SchemaRpcError("set option order", int32(resp.GetError().GetCode()), 0, resp.GetError().GetDescription()); err != nil {
		return nil, err
	}
	stored, err := s.liveOptions(spaceId, entry)
	if err != nil {
		return nil, err
	}
	rows = rows[:0]
	for _, option := range stored {
		rows = append(rows, v2model.OptionRow{Id: option.Id, Name: option.Name, Color: option.Color})
	}
	return &v2model.PropertyOptionsOrder{Options: rows}, nil
}

// ListPropertiesWithValue implements GET
// /v2/spaces/{space_id}/properties/with-value: the properties that carry a
// value, each with the number of objects carrying it. It is the reverse of a
// read, the question "what points at this" an object id or a date asks.
func (s *Service) ListPropertiesWithValue(ctx context.Context, spaceId, value string, offset, limit int) ([]v2model.RelationValueRow, int, bool, error) {
	if err := s.ensureSpace(ctx, spaceId); err != nil {
		return nil, 0, false, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, 0, false, v2model.ValidationFailed("a value is required",
			v2model.Issue{Path: "value", Message: "value is empty"})
	}
	resp := s.mw.RelationListWithValue(ctx, &pb.RpcRelationListWithValueRequest{
		SpaceId: spaceId,
		Value:   pbtypes.String(value),
	})
	// this RPC reports its one failure with the NULL code and a description
	// (core/relations.go), so a description is the failure, not prose beside
	// a success
	if description := resp.GetError().GetDescription(); description != "" {
		return nil, 0, false, v2model.NewError(http.StatusInternalServerError, v2model.CodeInternalError,
			"list properties with value failed: "+description)
	}
	entries, err := s.liveProperties(spaceId)
	if err != nil {
		return nil, 0, false, err
	}
	byKey := make(map[string]propertyEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	keyTaken, slugHolders := servedPropertyKeySets(entries)
	rows := make([]v2model.RelationValueRow, 0, len(resp.GetList()))
	for _, item := range resp.GetList() {
		row := v2model.RelationValueRow{Key: item.GetRelationKey(), Count: item.GetCounter()}
		// a key no live property answers to is still reported: the count is
		// real, and dropping the row would hide a match
		if entry, ok := byKey[item.GetRelationKey()]; ok {
			row.Key = servedKey(entry.Key, entry.Slug, keyTaken, slugHolders)
			row.Name = entry.Name
			row.Format = anyblockjson.FormatName(entry.Format)
		}
		rows = append(rows, row)
	}
	page, hasMore := pagination.Paginate(rows, offset, limit)
	return page, len(rows), hasMore, nil
}

// propertyRowsByIds serves relation object ids as property rows. An id that
// no longer resolves to a live property is skipped rather than guessed at,
// and rows come back sorted by their served key.
func (s *Service) propertyRowsByIds(spaceId string, ids []string) []v2model.PropertyRow {
	if len(ids) == 0 {
		return nil
	}
	entries, err := s.liveProperties(spaceId)
	if err != nil {
		return nil
	}
	byId := make(map[string]propertyEntry, len(entries))
	for _, entry := range entries {
		byId[entry.Id] = entry
	}
	keyTaken, slugHolders := servedPropertyKeySets(entries)
	rows := make([]v2model.PropertyRow, 0, len(ids))
	for _, id := range ids {
		entry, ok := byId[id]
		if !ok {
			continue
		}
		rows = append(rows, v2model.PropertyRow{
			Key:    servedKey(entry.Key, entry.Slug, keyTaken, slugHolders),
			Name:   entry.Name,
			Format: anyblockjson.FormatName(entry.Format),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

// ListTypeConflicts implements GET
// /v2/spaces/{space_id}/types/{type}/conflicts: the properties objects of
// the type carry that the type does not recommend. It is the read half of
// the app's conflict notice.
func (s *Service) ListTypeConflicts(ctx context.Context, spaceId, typeKey string, offset, limit int) ([]v2model.PropertyRow, int, bool, error) {
	if err := s.ensureSpace(ctx, spaceId); err != nil {
		return nil, 0, false, err
	}
	entry, err := s.requireLiveType(spaceId, typeKey, "/type", errKeysFor(ctx))
	if err != nil {
		return nil, 0, false, err
	}
	resp := s.mw.ObjectTypeListConflictingRelations(ctx, &pb.RpcObjectTypeListConflictingRelationsRequest{
		SpaceId:      spaceId,
		TypeObjectId: entry.Id,
	})
	if err := v2SchemaRpcError("list type conflicts", int32(resp.GetError().GetCode()),
		int32(pb.RpcObjectTypeListConflictingRelationsResponseError_READONLY_OBJECT_TYPE),
		resp.GetError().GetDescription()); err != nil {
		return nil, 0, false, err
	}
	rows := s.propertyRowsByIds(spaceId, resp.GetRelationIds())
	page, hasMore := pagination.Paginate(rows, offset, limit)
	return page, len(rows), hasMore, nil
}

// ResolveTypeConflicts implements POST
// /v2/spaces/{space_id}/types/{type}/resolve: every object of the type is
// brought in line with the type's current layout and recommended properties.
// It is the repair for what the conflict read reports, and it is the caller's
// to run after a type was edited.
func (s *Service) ResolveTypeConflicts(ctx context.Context, spaceId, typeKey string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	entry, err := s.requireLiveType(spaceId, typeKey, "/type", errKeysFor(ctx))
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.ObjectTypeResolveLayoutConflicts(ctx, &pb.RpcObjectTypeResolveLayoutConflictsRequest{
		TypeObjectId: entry.Id,
	})
	if err := v2SchemaRpcError("resolve type conflicts", int32(resp.GetError().GetCode()), 0, resp.GetError().GetDescription()); err != nil {
		return nil, err
	}
	return &v2model.AcceptResponse{}, nil
}

// SetTypesOrder implements PUT /v2/spaces/{space_id}/types/order: the types
// listed come first, in the given order, and the rest keep their relative
// order. Every type is resolved against the space before the RPC runs, so one
// unknown spelling refuses the whole list.
func (s *Service) SetTypesOrder(ctx context.Context, spaceId string, req v2model.SetTypesOrderRequest, dryRun bool) (*v2model.TypesOrder, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Types) == 0 {
		return nil, v2model.ValidationFailed("a type order is required",
			v2model.Issue{Path: "/types", Message: "types is empty"})
	}
	// one live read for the whole list: resolveTypeInput is what
	// requireLiveType runs per input, and a list of types would otherwise
	// re-read the namespace once per entry
	entries, err := s.liveTypes(spaceId)
	if err != nil {
		return nil, err
	}
	keyTaken, slugHolders := servedTypeKeySets(entries)
	v := errKeysFor(ctx)
	ids := make([]string, 0, len(req.Types))
	rows := make([]v2model.TypeRow, 0, len(req.Types))
	seen := make(map[string]bool, len(req.Types))
	for i, input := range req.Types {
		path := fmt.Sprintf("/types/%d", i)
		entry, ok, ambiguous, err := s.resolveTypeInput(spaceId, input, entries)
		if err != nil {
			return nil, err
		}
		if len(ambiguous) > 0 {
			return nil, ambiguousKeyError(v.typeWord(), input, path, ambiguous)
		}
		if !ok || entry.Id == "" {
			return nil, s.typeNotFoundError(spaceId, input, v)
		}
		if seen[entry.Id] {
			return nil, v2model.ValidationFailed(fmt.Sprintf("type %q is named twice", input),
				v2model.Issue{Path: path, Message: "each type takes one place in the order"})
		}
		seen[entry.Id] = true
		ids = append(ids, entry.Id)
		rows = append(rows, v2model.TypeRow{
			Key:  servedTypeKeyOf(entry.Key, entry.Slug, keyTaken, slugHolders),
			Name: entry.Name,
		})
	}
	if dryRun {
		return &v2model.TypesOrder{DryRun: true, Types: rows}, nil
	}
	resp := s.mw.ObjectTypeSetOrder(ctx, &pb.RpcObjectTypeSetOrderRequest{SpaceId: spaceId, TypeIds: ids})
	if err := v2SchemaRpcError("set type order", int32(resp.GetError().GetCode()), 0, resp.GetError().GetDescription()); err != nil {
		return nil, err
	}
	return &v2model.TypesOrder{Types: rows}, nil
}
