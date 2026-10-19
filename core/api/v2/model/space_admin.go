package v2model

// space_admin.go holds the request and response DTOs for the space
// administration surface: invites, membership, sharing, ownership, the
// homepage, and deleting a space. The permission vocabulary matches
// MemberRow.Role ("viewer", "editor", "admin", "owner", "no_permissions"); the
// invite vocabulary is "member", "guest" and "anyone" (the RPC's
// WithoutApprove, an invite that needs no owner approval).

// Invite is one space invite link.
type Invite struct {
	InviteCid     string `json:"invite_cid,omitempty"`
	InviteFileKey string `json:"invite_file_key,omitempty"`
	InviteType    string `json:"invite_type"`
	Permissions   string `json:"permissions"`
	// HeldByOwner is set when the invite is kept in the owner's account:
	// members see it with an empty cid and file key.
	HeldByOwner bool `json:"held_by_owner,omitempty"`
	DryRun      bool `json:"dry_run,omitempty"`
}

// CreateInviteRequest is the body of POST /v2/spaces/{space_id}/invite.
type CreateInviteRequest struct {
	// InviteType is "member" (default), "guest", or "anyone".
	InviteType string `json:"invite_type,omitempty"`
	// Permissions is the role the invite grants: "viewer" (default),
	// "editor", "admin", or "owner".
	Permissions string `json:"permissions,omitempty"`
	// ShareWithinSpace stores the invite in the space, so every member can
	// share it. Otherwise only the owner can.
	ShareWithinSpace bool `json:"share_within_space,omitempty"`
}

// UpdateInviteRequest is the body of PATCH /v2/spaces/{space_id}/invite. The
// route only changes the space-wide ("anyone") invite; a member or guest
// invite is answered with 409 and must be revoked and recreated instead.
type UpdateInviteRequest struct {
	Permissions string `json:"permissions"`
}

// InviteChange is the role the invite now grants.
type InviteChange struct {
	Permissions string `json:"permissions"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// GuestInvite is the guest invite link, answered by the guest route.
type GuestInvite struct {
	InviteCid     string `json:"invite_cid,omitempty"`
	InviteFileKey string `json:"invite_file_key,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

// InviteView is what an invite link discloses before the caller joins.
type InviteView struct {
	SpaceId         string `json:"space_id"`
	SpaceName       string `json:"space_name"`
	SpaceIconCid    string `json:"space_icon_cid,omitempty"`
	CreatorName     string `json:"creator_name,omitempty"`
	CreatorIconCid  string `json:"creator_icon_cid,omitempty"`
	SpaceIconOption int    `json:"space_icon_option,omitempty"`
	SpaceUxType     int    `json:"space_ux_type,omitempty"`
	SpaceType       string `json:"space_type"`
	InviteType      string `json:"invite_type"`
	IsGuestInvite   bool   `json:"is_guest_invite,omitempty"`
}

// JoinSpaceRequest is the body of POST /v2/invites/{invite_cid}/join. The
// invite cid is in the path; space_id and network_id are optional and are
// read from the invite when omitted.
type JoinSpaceRequest struct {
	InviteFileKey string `json:"invite_file_key"`
	SpaceId       string `json:"space_id,omitempty"`
	NetworkId     string `json:"network_id,omitempty"`
}

// AcceptResponse is the answer for a mutation whose only result is that the
// request was accepted.
type AcceptResponse struct {
	DryRun bool `json:"dry_run,omitempty"`
}

// AddMembersRequest is the body of POST /v2/spaces/{space_id}/members.
type AddMembersRequest struct {
	Identities []string `json:"identities"`
	// Permissions is the role every identity is granted: "viewer" (default),
	// "editor", "admin", or "owner".
	Permissions string `json:"permissions,omitempty"`
}

// MemberPermissionChange is one identity's new role.
type MemberPermissionChange struct {
	Identity    string `json:"identity"`
	Permissions string `json:"permissions"`
}

// UpdateMembersRequest is the body of PATCH /v2/spaces/{space_id}/members.
type UpdateMembersRequest struct {
	Changes []MemberPermissionChange `json:"changes"`
}

// RemoveMembersRequest is the body of POST …/members/remove.
type RemoveMembersRequest struct {
	Identities []string `json:"identities"`
}

// ApproveMemberRequest is the body of POST …/members/approve.
type ApproveMemberRequest struct {
	Identity string `json:"identity"`
	// Permissions defaults to "viewer".
	Permissions string `json:"permissions,omitempty"`
}

// DeclineMemberRequest is the body of POST …/members/decline.
type DeclineMemberRequest struct {
	Identity string `json:"identity"`
}

// LeaveApproveRequest is the body of POST …/leave/approve.
type LeaveApproveRequest struct {
	Identities []string `json:"identities"`
}

// ChangeOwnershipRequest is the body of POST …/ownership.
type ChangeOwnershipRequest struct {
	NewOwnerIdentity string `json:"new_owner_identity"`
	// OldOwnerPermissions is the role the previous owner keeps: "viewer"
	// (default), "editor", "admin", or "no_permissions" to leave.
	OldOwnerPermissions string `json:"old_owner_permissions,omitempty"`
}

// DeleteSpaceResponse reports the deletion timestamp.
type DeleteSpaceResponse struct {
	Timestamp int64 `json:"timestamp"`
	DryRun    bool  `json:"dry_run,omitempty"`
}

// SetHomepageRequest is the body of PUT …/homepage.
type SetHomepageRequest struct {
	Homepage string `json:"homepage"`
}
