package v2service

// space_admin.go implements the space administration surface: invites,
// membership, sharing, ownership, the homepage, and deleting a space. Each
// method is a thin wrapper over one ClientCommands RPC; the space grant and
// the write verb are re-checked here (the service-level backstop, service.go)
// because a route can be reached without the gate in tests.
//
// C9 dry runs validate the request and report the shape without calling the
// RPC, because none of these mutations can be simulated, and every mutation
// runs behind the route-level C8 idempotency middleware.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anyproto/any-sync/util/crypto"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
)

// parsePermissions maps the API role vocabulary onto the RPC enum. An empty
// value is an error when required, else the default.
func parsePermissions(path, value string, required bool, def model.ParticipantPermissions) (model.ParticipantPermissions, error) {
	switch strings.TrimSpace(value) {
	case "":
		if required {
			return 0, v2model.ValidationFailed("permissions is required",
				v2model.Issue{Path: path, Message: `allowed roles are "viewer", "editor", "admin", "owner" and "no_permissions"`})
		}
		return def, nil
	case "viewer":
		return model.ParticipantPermissions_Reader, nil
	case "editor":
		return model.ParticipantPermissions_Writer, nil
	case "admin":
		return model.ParticipantPermissions_Admin, nil
	case "owner":
		return model.ParticipantPermissions_Owner, nil
	case "no_permissions":
		return model.ParticipantPermissions_NoPermissions, nil
	}
	return 0, v2model.ValidationFailed("unknown permission "+fmt.Sprintf("%q", value),
		v2model.Issue{Path: path, Message: `allowed roles are "viewer", "editor", "admin", "owner" and "no_permissions"`})
}

// parseInviteType maps the API vocabulary onto the RPC invite enum. An empty
// value is a member invite.
func parseInviteType(path, value string) (model.InviteType, error) {
	switch strings.TrimSpace(value) {
	case "", "member":
		return model.InviteType_Member, nil
	case "guest":
		return model.InviteType_Guest, nil
	case "anyone", "without_approve":
		return model.InviteType_WithoutApprove, nil
	}
	return 0, v2model.ValidationFailed("unknown invite type "+fmt.Sprintf("%q", value),
		v2model.Issue{Path: path, Message: `allowed types are "member", "guest" and "anyone"`})
}

// formatInviteType is parseInviteType's inverse.
func formatInviteType(inviteType model.InviteType) string {
	switch inviteType {
	case model.InviteType_Guest:
		return "guest"
	case model.InviteType_WithoutApprove:
		return "anyone"
	default:
		return "member"
	}
}

// formatSpaceType renders the RPC space type in the product's vocabulary.
func formatSpaceType(spaceType model.SpaceType) string {
	switch spaceType {
	case model.SpaceType_SpaceTypeTech:
		return "tech"
	case model.SpaceType_SpaceTypeChat:
		return "chat"
	case model.SpaceType_SpaceTypeOneToOne:
		return "one_to_one"
	case model.SpaceType_SpaceTypeUnknown:
		return "unknown"
	default:
		return "space"
	}
}

// nonEmptyList trims and drops blanks from an identity or object-id list,
// refusing an empty result: an add or remove that names nobody is an agent
// bug, not a no-op to accept.
func nonEmptyList(path, message string, values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil, v2model.ValidationFailed(message, v2model.Issue{Path: path, Message: "send at least one value"})
	}
	return out, nil
}

// checkIdentities refuses a malformed account identity where it enters the
// API. The space commands decode identities with crypto.DecodeAccountAddress
// and report a decode failure as a generic internal error, which would reach
// the caller as a 500; a value the caller typed is a 400 here instead.
func checkIdentities(path string, values []string) error {
	for i, value := range values {
		if err := checkIdentity(fmt.Sprintf("%s/%d", path, i), value); err != nil {
			return err
		}
	}
	return nil
}

// checkIdentity is checkIdentities for a single value, at the path the caller
// can point a reader at directly.
func checkIdentity(path, value string) error {
	if _, err := crypto.DecodeAccountAddress(value); err != nil {
		return v2model.ValidationFailed("invalid identity",
			v2model.Issue{
				Path:    path,
				Message: fmt.Sprintf("%q is not an account identity", value),
			})
	}
	return nil
}

// spaceCommandError classifies a Space RPC failure into the C6 shape by the
// error enum's own name. Every Space command carries its own enum, but the
// code names are a shared vocabulary (NO_SUCH_SPACE, INCORRECT_PERMISSIONS,
// NOT_SHAREABLE, …), so one name-keyed mapper classifies all of them
// correctly: a numeric table would mislabel the codes that mean different
// things in different commands.
func spaceCommandError(op string, code fmt.Stringer, description string) error {
	name := code.String()
	if name == "NULL" {
		return nil
	}
	detail := description
	if detail == "" {
		detail = strings.ToLower(strings.ReplaceAll(name, "_", " "))
	}
	switch name {
	case "BAD_INPUT":
		return v2model.ValidationFailed(op+": invalid input", v2model.Issue{Message: detail})
	case "NO_SUCH_SPACE", "SPACE_IS_DELETED", "NO_SUCH_REQUEST", "PARTICIPANT_NOT_FOUND",
		"NO_ACTIVE_INVITE", "INVITE_NOT_FOUND", "INVITE_BAD_CONTENT", "INVALID_SPACE_TYPE":
		return v2model.NotFound(op+": "+detail,
			v2model.Issue{Message: "the target does not exist or is not available"})
	case "INCORRECT_PERMISSIONS":
		return v2model.NewError(http.StatusForbidden, v2model.CodeForbidden, op+": "+detail)
	case "NOT_SHAREABLE", "INVITE_ALREADY_SHARED", "INVITE_NOT_SHAREABLE", "DIFFERENT_NETWORK":
		return v2model.NewError(http.StatusConflict, v2model.CodeConflict, op+": "+detail)
	case "LIMIT_REACHED":
		return v2model.ValidationFailed(op+": "+detail,
			v2model.Issue{Message: "the space has reached a limit"})
	}
	msg := op + " failed"
	if detail != "" {
		msg += ": " + detail
	}
	return v2model.NewError(http.StatusInternalServerError, v2model.CodeInternalError, msg)
}

// GetInvite implements GET /v2/spaces/{space_id}/invite: the current invite
// link, or the owner-held marker when the invite is not readable by this key.
func (s *Service) GetInvite(ctx context.Context, spaceId string) (*v2model.Invite, error) {
	if err := s.ensureSpace(ctx, spaceId); err != nil {
		return nil, err
	}
	resp := s.mw.SpaceInviteGetCurrent(ctx, &pb.RpcSpaceInviteGetCurrentRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("get invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.Invite{
		InviteCid:     resp.InviteCid,
		InviteFileKey: resp.InviteFileKey,
		InviteType:    formatInviteType(resp.InviteType),
		Permissions:   memberRole(resp.Permissions),
		HeldByOwner:   resp.HeldByOwner,
	}, nil
}

// CreateInvite implements POST /v2/spaces/{space_id}/invite.
func (s *Service) CreateInvite(ctx context.Context, spaceId string, req v2model.CreateInviteRequest, dryRun bool) (*v2model.Invite, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	inviteType, err := parseInviteType("/invite_type", req.InviteType)
	if err != nil {
		return nil, err
	}
	permissions, err := parsePermissions("/permissions", req.Permissions, false, model.ParticipantPermissions_Reader)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.Invite{
			InviteType:  formatInviteType(inviteType),
			Permissions: memberRole(permissions),
			DryRun:      true,
		}, nil
	}
	resp := s.mw.SpaceInviteGenerate(ctx, &pb.RpcSpaceInviteGenerateRequest{
		SpaceId:          spaceId,
		InviteType:       inviteType,
		Permissions:      permissions,
		ShareWithinSpace: req.ShareWithinSpace,
	})
	if resp.Error != nil {
		if err := spaceCommandError("create invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	if resp.InviteCid == "" {
		return nil, v2model.NewError(http.StatusInternalServerError, v2model.CodeInternalError,
			"create invite: the invite RPC returned no cid")
	}
	return &v2model.Invite{
		InviteCid:     resp.InviteCid,
		InviteFileKey: resp.InviteFileKey,
		InviteType:    formatInviteType(resp.InviteType),
		Permissions:   memberRole(resp.Permissions),
	}, nil
}

// UpdateInvite implements PATCH /v2/spaces/{space_id}/invite: the permissions
// of the space-wide invite, the one whose link anyone can use. The acl only
// changes that invite in place; a member or guest invite keeps its permissions
// for life, so the route refuses with 409 rather than surfacing the RPC's bare
// "invite not exists" as an internal error.
func (s *Service) UpdateInvite(ctx context.Context, spaceId string, req v2model.UpdateInviteRequest, dryRun bool) (*v2model.InviteChange, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	permissions, err := parsePermissions("/permissions", req.Permissions, true, 0)
	if err != nil {
		return nil, err
	}
	current := s.mw.SpaceInviteGetCurrent(ctx, &pb.RpcSpaceInviteGetCurrentRequest{SpaceId: spaceId})
	if current.Error != nil {
		if err := spaceCommandError("update invite", current.Error.Code, current.Error.Description); err != nil {
			return nil, err
		}
	}
	if current.InviteType != model.InviteType_WithoutApprove {
		return nil, v2model.NewError(http.StatusConflict, v2model.CodeConflict, fmt.Sprintf(
			"update invite: the space's invite is a %s invite, and only the space-wide invite's permissions change in place; revoke it and create the invite you want instead",
			formatInviteType(current.InviteType)))
	}
	if dryRun {
		return &v2model.InviteChange{Permissions: memberRole(permissions), DryRun: true}, nil
	}
	resp := s.mw.SpaceInviteChange(ctx, &pb.RpcSpaceInviteChangeRequest{
		SpaceId:     spaceId,
		Permissions: permissions,
	})
	if resp.Error != nil {
		if err := spaceCommandError("update invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.InviteChange{Permissions: memberRole(permissions)}, nil
}

// RevokeInvite implements DELETE /v2/spaces/{space_id}/invite.
func (s *Service) RevokeInvite(ctx context.Context, spaceId string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceInviteRevoke(ctx, &pb.RpcSpaceInviteRevokeRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("revoke invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// GetGuestInvite implements GET /v2/spaces/{space_id}/invite/guest.
func (s *Service) GetGuestInvite(ctx context.Context, spaceId string) (*v2model.GuestInvite, error) {
	if err := s.ensureSpace(ctx, spaceId); err != nil {
		return nil, err
	}
	resp := s.mw.SpaceInviteGetGuest(ctx, &pb.RpcSpaceInviteGetGuestRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("get guest invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.GuestInvite{InviteCid: resp.InviteCid, InviteFileKey: resp.InviteFileKey}, nil
}

// ViewInvite implements GET /v2/invites/{invite_cid}: an invite folded open
// without joining. The cid and file key are the credential; no space grant
// exists yet, so the gate classifies the route as scoped-denied.
func (s *Service) ViewInvite(ctx context.Context, inviteCid, fileKey string) (*v2model.InviteView, error) {
	if strings.TrimSpace(inviteCid) == "" {
		return nil, v2model.ValidationFailed("invite cid is required",
			v2model.Issue{Path: "invite_cid", Message: "the invite link carries it"})
	}
	if strings.TrimSpace(fileKey) == "" {
		return nil, v2model.ValidationFailed("file_key is required",
			v2model.Issue{Path: "file_key", Message: "the invite link carries it as the fragment after the cid"})
	}
	resp := s.mw.SpaceInviteView(ctx, &pb.RpcSpaceInviteViewRequest{
		InviteCid:     inviteCid,
		InviteFileKey: fileKey,
	})
	if resp.Error != nil {
		if err := spaceCommandError("view invite", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.InviteView{
		SpaceId:         s.servedSpaceRef(ctx, resp.SpaceId),
		SpaceName:       resp.SpaceName,
		SpaceIconCid:    resp.SpaceIconCid,
		CreatorName:     resp.CreatorName,
		CreatorIconCid:  resp.CreatorIconCid,
		SpaceIconOption: int(resp.SpaceIconOption),
		SpaceUxType:     int(resp.SpaceUxType),
		SpaceType:       formatSpaceType(resp.SpaceType),
		InviteType:      formatInviteType(resp.InviteType),
		IsGuestInvite:   resp.IsGuestUserInvite,
	}, nil
}

// JoinSpace implements POST /v2/invites/{invite_cid}/join.
func (s *Service) JoinSpace(ctx context.Context, inviteCid string, req v2model.JoinSpaceRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := ensureWriteGranted(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.InviteFileKey) == "" {
		return nil, v2model.ValidationFailed("invite_file_key is required",
			v2model.Issue{Path: "/invite_file_key", Message: "the invite link carries it as the fragment after the cid"})
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceJoin(ctx, &pb.RpcSpaceJoinRequest{
		NetworkId:     req.NetworkId,
		SpaceId:       req.SpaceId,
		InviteCid:     inviteCid,
		InviteFileKey: req.InviteFileKey,
	})
	if resp.Error != nil {
		if err := spaceCommandError("join space", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// AddMembers implements POST /v2/spaces/{space_id}/members: it invites
// identities at a role, minting a new invite link the caller then shares.
func (s *Service) AddMembers(ctx context.Context, spaceId string, req v2model.AddMembersRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	ids, err := nonEmptyList("/identities", "identities is required", req.Identities)
	if err != nil {
		return nil, err
	}
	if err := checkIdentities("/identities", ids); err != nil {
		return nil, err
	}
	permissions, err := parsePermissions("/permissions", req.Permissions, false, model.ParticipantPermissions_Reader)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceParticipantsAddList(ctx, &pb.RpcSpaceParticipantsAddListRequest{
		SpaceId:     spaceId,
		Identities:  ids,
		Permissions: permissions,
	})
	if resp.Error != nil {
		if err := spaceCommandError("add members", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// UpdateMemberPermissions implements PATCH /v2/spaces/{space_id}/members.
func (s *Service) UpdateMemberPermissions(ctx context.Context, spaceId string, req v2model.UpdateMembersRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if len(req.Changes) == 0 {
		return nil, v2model.ValidationFailed("changes is required",
			v2model.Issue{Path: "/changes", Message: "send at least one {identity, permissions} entry"})
	}
	changes := make([]*model.ParticipantPermissionChange, 0, len(req.Changes))
	for i, change := range req.Changes {
		identity := strings.TrimSpace(change.Identity)
		if identity == "" {
			return nil, v2model.ValidationFailed("identity is required",
				v2model.Issue{Path: fmt.Sprintf("/changes/%d/identity", i), Message: "the entry names no identity"})
		}
		if err := checkIdentity(fmt.Sprintf("/changes/%d/identity", i), identity); err != nil {
			return nil, err
		}
		permissions, err := parsePermissions(fmt.Sprintf("/changes/%d/permissions", i), change.Permissions, true, 0)
		if err != nil {
			return nil, err
		}
		changes = append(changes, &model.ParticipantPermissionChange{Identity: identity, Perms: permissions})
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceParticipantPermissionsChange(ctx, &pb.RpcSpaceParticipantPermissionsChangeRequest{
		SpaceId: spaceId,
		Changes: changes,
	})
	if resp.Error != nil {
		if err := spaceCommandError("update member permissions", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// RemoveMembers implements POST /v2/spaces/{space_id}/members/remove.
func (s *Service) RemoveMembers(ctx context.Context, spaceId string, req v2model.RemoveMembersRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	ids, err := nonEmptyList("/identities", "identities is required", req.Identities)
	if err != nil {
		return nil, err
	}
	if err := checkIdentities("/identities", ids); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceParticipantRemove(ctx, &pb.RpcSpaceParticipantRemoveRequest{
		SpaceId:    spaceId,
		Identities: ids,
	})
	if resp.Error != nil {
		if err := spaceCommandError("remove members", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// ApproveMember implements POST /v2/spaces/{space_id}/members/approve: a
// joining request is accepted at a role.
func (s *Service) ApproveMember(ctx context.Context, spaceId string, req v2model.ApproveMemberRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	identity := strings.TrimSpace(req.Identity)
	if identity == "" {
		return nil, v2model.ValidationFailed("identity is required",
			v2model.Issue{Path: "/identity", Message: "the approval names no identity"})
	}
	if err := checkIdentity("/identity", identity); err != nil {
		return nil, err
	}
	permissions, err := parsePermissions("/permissions", req.Permissions, false, model.ParticipantPermissions_Reader)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceRequestApprove(ctx, &pb.RpcSpaceRequestApproveRequest{
		SpaceId:     spaceId,
		Identity:    identity,
		Permissions: permissions,
	})
	if resp.Error != nil {
		if err := spaceCommandError("approve member", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// DeclineMember implements POST /v2/spaces/{space_id}/members/decline.
func (s *Service) DeclineMember(ctx context.Context, spaceId string, req v2model.DeclineMemberRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	identity := strings.TrimSpace(req.Identity)
	if identity == "" {
		return nil, v2model.ValidationFailed("identity is required",
			v2model.Issue{Path: "/identity", Message: "the decline names no identity"})
	}
	if err := checkIdentity("/identity", identity); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceRequestDecline(ctx, &pb.RpcSpaceRequestDeclineRequest{
		SpaceId:  spaceId,
		Identity: identity,
	})
	if resp.Error != nil {
		if err := spaceCommandError("decline member", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// ApproveLeave implements POST /v2/spaces/{space_id}/leave/approve: the
// owner accepts members' leave requests.
func (s *Service) ApproveLeave(ctx context.Context, spaceId string, req v2model.LeaveApproveRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	ids, err := nonEmptyList("/identities", "identities is required", req.Identities)
	if err != nil {
		return nil, err
	}
	if err := checkIdentities("/identities", ids); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceLeaveApprove(ctx, &pb.RpcSpaceLeaveApproveRequest{
		SpaceId:    spaceId,
		Identities: ids,
	})
	if resp.Error != nil {
		if err := spaceCommandError("approve leave", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// CancelJoin implements POST /v2/spaces/{space_id}/join/cancel: a pending
// join request is withdrawn.
func (s *Service) CancelJoin(ctx context.Context, spaceId string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceJoinCancel(ctx, &pb.RpcSpaceJoinCancelRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("cancel join", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// ShareSpace implements POST /v2/spaces/{space_id}/sharing.
func (s *Service) ShareSpace(ctx context.Context, spaceId string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceMakeShareable(ctx, &pb.RpcSpaceMakeShareableRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("share space", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// StopSharingSpace implements DELETE /v2/spaces/{space_id}/sharing.
func (s *Service) StopSharingSpace(ctx context.Context, spaceId string, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceStopSharing(ctx, &pb.RpcSpaceStopSharingRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("stop sharing space", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// ChangeSpaceOwnership implements POST /v2/spaces/{space_id}/ownership.
func (s *Service) ChangeSpaceOwnership(ctx context.Context, spaceId string, req v2model.ChangeOwnershipRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	newOwner := strings.TrimSpace(req.NewOwnerIdentity)
	if newOwner == "" {
		return nil, v2model.ValidationFailed("new_owner_identity is required",
			v2model.Issue{Path: "/new_owner_identity", Message: "the transfer names no new owner"})
	}
	if err := checkIdentity("/new_owner_identity", newOwner); err != nil {
		return nil, err
	}
	oldOwner, err := parsePermissions("/old_owner_permissions", req.OldOwnerPermissions, false, model.ParticipantPermissions_Reader)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceChangeOwnership(ctx, &pb.RpcSpaceChangeOwnershipRequest{
		SpaceId:             spaceId,
		NewOwnerIdentity:    newOwner,
		OldOwnerPermissions: oldOwner,
	})
	if resp.Error != nil {
		if err := spaceCommandError("change space ownership", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.AcceptResponse{}, nil
}

// DeleteSpace implements DELETE /v2/spaces/{space_id}.
func (s *Service) DeleteSpace(ctx context.Context, spaceId string, dryRun bool) (*v2model.DeleteSpaceResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	if dryRun {
		return &v2model.DeleteSpaceResponse{DryRun: true}, nil
	}
	resp := s.mw.SpaceDelete(ctx, &pb.RpcSpaceDeleteRequest{SpaceId: spaceId})
	if resp.Error != nil {
		if err := spaceCommandError("delete space", resp.Error.Code, resp.Error.Description); err != nil {
			return nil, err
		}
	}
	return &v2model.DeleteSpaceResponse{Timestamp: resp.Timestamp}, nil
}

// SetSpaceHomepage implements PUT /v2/spaces/{space_id}/homepage.
func (s *Service) SetSpaceHomepage(ctx context.Context, spaceId string, req v2model.SetHomepageRequest, dryRun bool) (*v2model.AcceptResponse, error) {
	if err := s.ensureSpaceWrite(ctx, spaceId); err != nil {
		return nil, err
	}
	homepage := strings.TrimSpace(req.Homepage)
	if homepage == "" {
		return nil, v2model.ValidationFailed("homepage is required",
			v2model.Issue{Path: "/homepage", Message: "name the object id or widget the space opens on"})
	}
	if dryRun {
		return &v2model.AcceptResponse{DryRun: true}, nil
	}
	resp := s.mw.WorkspaceSetHomepage(ctx, &pb.RpcWorkspaceSetHomepageRequest{
		SpaceId:  spaceId,
		Homepage: homepage,
	})
	if resp.Error != nil {
		return nil, v2SpaceRpcError("set space homepage", int32(resp.Error.Code),
			int32(pb.RpcWorkspaceSetHomepageResponseError_BAD_INPUT), resp.Error.Description)
	}
	return &v2model.AcceptResponse{}, nil
}
