package v2handler

// space_admin.go holds the space administration handlers: invites,
// membership, sharing, ownership, export, and the account-level workspace
// selection and ordering routes. Each is a thin wrapper: the service owns the
// validation, the grant backstop and the RPC mapping. Every mutation honors
// ?dry_run=true (C9: validate only, the RPC is not called) and runs behind
// the route-level Idempotency-Key middleware (C8).

import (
	"net/http"

	"github.com/gin-gonic/gin"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	v2service "github.com/anyproto/anytype-heart/core/api/v2/service"
)

// maxSpaceAdminRequestBody caps administration bodies: identities, object
// ids and a path. 1 MiB is room for a large batch.
const maxSpaceAdminRequestBody = 1 << 20

// GetInviteHandler reads the space invite
//
//	@Summary		Get the space invite
//	@Description	The owner-held invite is reported with held_by_owner and no cid or file key. A space with no invite is a 404.
//	@Id				get_invite
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id	path		string			true	"Space id, from GET /v2/spaces"
//	@Success		200			{object}	v2model.Invite	"The current invite"
//	@Failure		404			{object}	v2model.Error	"No invite, or the space is not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/invite [get]
func GetInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		invite, err := s.GetInvite(c.Request.Context(), c.Param("space_id"))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, invite)
	}
}

// CreateInviteHandler creates the space invite
//
//	@Summary		Create the space invite
//	@Description	A space has one invite link at a time; creating it replaces the role the old link grants. A dry run validates the body and stops there.
//	@Id				create_invite
//	@Tags			Sharing
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without creating"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.CreateInviteRequest	true	"The invite to create"
//	@Success		201				{object}	v2model.Invite				"Created invite"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		403				{object}	v2model.Error				"The caller cannot share this space"
//	@Failure		404				{object}	v2model.Error				"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/invite [post]
func CreateInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.CreateInviteRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the invite body takes invite_type, permissions and share_within_space"), maxSpaceAdminRequestBody, "invite") {
			return
		}
		dryRun := isV2DryRun(c)
		invite, err := s.CreateInvite(c.Request.Context(), c.Param("space_id"), req, dryRun)
		if err != nil {
			RespondError(c, err)
			return
		}
		status := http.StatusCreated
		if dryRun {
			status = http.StatusOK
		}
		c.JSON(status, invite)
	}
}

// UpdateInviteHandler changes the role the space-wide invite grants
//
//	@Summary		Change the invite role
//	@Description	Changes the role of the space-wide ("anyone") invite. The cid and file key stay the same, so a link already shared keeps working. A member or guest invite keeps its role for life: revoke it and create the invite you want instead, which answers 409 here. A role that is already in place answers 403.
//	@Id				update_invite
//	@Tags			Sharing
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate and report without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.UpdateInviteRequest	true	"The role to grant"
//	@Success		200				{object}	v2model.InviteChange		"The new role"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		403				{object}	v2model.Error				"The caller cannot change the invite, or it already grants the requested role"
//	@Failure		404				{object}	v2model.Error				"No invite, or the space is not live"
//	@Failure		409				{object}	v2model.Error				"The space's invite is not the space-wide one, so its role cannot change in place"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/invite [patch]
func UpdateInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.UpdateInviteRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the invite update takes permissions"), maxSpaceAdminRequestBody, "invite") {
			return
		}
		change, err := s.UpdateInvite(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, change)
	}
}

// RevokeInviteHandler revokes the space invite
//
//	@Summary		Revoke the invite
//	@Description	The link stops working for everyone. Members already in the space are unaffected.
//	@Id				revoke_invite
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool					false	"Validate without revoking"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key replays the stored response"
//	@Success		200				{object}	v2model.AcceptResponse	"Invite revoked"
//	@Failure		403				{object}	v2model.Error			"The caller cannot revoke the invite"
//	@Failure		404				{object}	v2model.Error			"No invite, or the space is not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/invite [delete]
func RevokeInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.RevokeInvite(c.Request.Context(), c.Param("space_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// GetGuestInviteHandler reads the guest invite
//
//	@Summary		Get the guest invite
//	@Description	The guest link admits one person without an account on the network. A space with no guest invite is a 404.
//	@Id				get_guest_invite
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id	path		string				true	"Space id, from GET /v2/spaces"
//	@Success		200			{object}	v2model.GuestInvite	"The guest invite"
//	@Failure		404			{object}	v2model.Error		"No guest invite, or the space is not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/invite/guest [get]
func GetGuestInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		invite, err := s.GetGuestInvite(c.Request.Context(), c.Param("space_id"))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, invite)
	}
}

// ViewInviteHandler previews an invite link
//
//	@Summary		Preview an invite
//	@Description	Folds the link open without joining. The cid and file key are the credential; no space grant is needed yet, which is why the caller must hold the whole link.
//	@Id				view_invite
//	@Tags			Sharing
//	@Produce		json
//	@Param			invite_cid	path		string				true	"Invite cid, the part before the fragment"
//	@Param			file_key	query		string				true	"Invite file key, the fragment after the cid"
//	@Success		200			{object}	v2model.InviteView	"The space the invite opens"
//	@Failure		400			{object}	v2model.Error		"Missing file key"
//	@Failure		404			{object}	v2model.Error		"The invite is unknown or revoked"
//	@Security		bearerauth
//	@Router			/v2/invites/{invite_cid} [get]
func ViewInviteHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		view, err := s.ViewInvite(c.Request.Context(), c.Param("invite_cid"), c.Query("file_key"))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

// JoinSpaceHandler joins a space through an invite
//
//	@Summary		Join a space
//	@Description	The join is asynchronous: the answer says the request was accepted, and the space appears in GET /v2/spaces once the owner approves (or at once for an anyone invite). A dry run validates the body and stops there.
//	@Id				join_space
//	@Tags			Sharing
//	@Accept			json
//	@Produce		json
//	@Param			invite_cid		path		string					true	"Invite cid, the part before the fragment"
//	@Param			dry_run			query		bool					false	"Validate the body without joining"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.JoinSpaceRequest	true	"The invite file key, and optionally the space and network ids"
//	@Success		202				{object}	v2model.AcceptResponse	"Join request accepted"
//	@Failure		400				{object}	v2model.Error			"Validation failure"
//	@Failure		404				{object}	v2model.Error			"The invite is unknown or revoked"
//	@Security		bearerauth
//	@Router			/v2/invites/{invite_cid}/join [post]
func JoinSpaceHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.JoinSpaceRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the join body takes invite_file_key, and optionally space_id and network_id"), maxSpaceAdminRequestBody, "join") {
			return
		}
		dryRun := isV2DryRun(c)
		result, err := s.JoinSpace(c.Request.Context(), c.Param("invite_cid"), req, dryRun)
		if err != nil {
			RespondError(c, err)
			return
		}
		status := http.StatusAccepted
		if dryRun {
			status = http.StatusOK
		}
		c.JSON(status, result)
	}
}

// AddMembersHandler invites identities at a role
//
//	@Summary		Invite members
//	@Description	Mints or reuses the space invite at the given role; the caller then shares the link. An identity already in the space is not re-invited.
//	@Id				add_members
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without inviting"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.AddMembersRequest	true	"The identities and the role"
//	@Success		200				{object}	v2model.AcceptResponse		"Invites sent"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		403				{object}	v2model.Error				"The caller cannot invite members"
//	@Failure		404				{object}	v2model.Error				"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/members [post]
func AddMembersHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.AddMembersRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the member list takes identities and permissions"), maxSpaceAdminRequestBody, "member list") {
			return
		}
		result, err := s.AddMembers(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// UpdateMemberPermissionsHandler changes members' roles
//
//	@Summary		Change member roles
//	@Description	One request carries every change; a role change for an identity not in the space is a 404.
//	@Id				update_member_permissions
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.UpdateMembersRequest	true	"The role changes"
//	@Success		200				{object}	v2model.AcceptResponse			"Roles changed"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller cannot change roles"
//	@Failure		404				{object}	v2model.Error					"Space or member not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/members [patch]
func UpdateMemberPermissionsHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.UpdateMembersRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the update takes changes, each an identity and its new permissions"), maxSpaceAdminRequestBody, "member changes") {
			return
		}
		result, err := s.UpdateMemberPermissions(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// RemoveMembersHandler removes members
//
//	@Summary		Remove members
//	@Description	The identities lose access at once. Removing the owner is refused; transfer ownership first.
//	@Id				remove_members
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without removing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.RemoveMembersRequest	true	"The identities to remove"
//	@Success		200				{object}	v2model.AcceptResponse			"Members removed"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller cannot remove members"
//	@Failure		404				{object}	v2model.Error					"Space or member not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/members/remove [post]
func RemoveMembersHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.RemoveMembersRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the removal takes identities"), maxSpaceAdminRequestBody, "member removal") {
			return
		}
		result, err := s.RemoveMembers(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ApproveMemberHandler approves a join request
//
//	@Summary		Approve a join request
//	@Description	The identity is admitted at the given role. A request that is not pending is a 404.
//	@Id				approve_member
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without approving"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.ApproveMemberRequest	true	"The identity and the role to grant"
//	@Success		200				{object}	v2model.AcceptResponse			"Request approved"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller cannot approve requests"
//	@Failure		404				{object}	v2model.Error					"No pending request for that identity"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/members/approve [post]
func ApproveMemberHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.ApproveMemberRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the approval takes identity and permissions"), maxSpaceAdminRequestBody, "member approval") {
			return
		}
		result, err := s.ApproveMember(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// DeclineMemberHandler declines a join request
//
//	@Summary		Decline a join request
//	@Description	The identity is told the request was declined and gains no access.
//	@Id				decline_member
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without declining"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.DeclineMemberRequest	true	"The identity to decline"
//	@Success		200				{object}	v2model.AcceptResponse			"Request declined"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller cannot decline requests"
//	@Failure		404				{object}	v2model.Error					"No pending request for that identity"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/members/decline [post]
func DeclineMemberHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.DeclineMemberRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the decline takes identity"), maxSpaceAdminRequestBody, "member decline") {
			return
		}
		result, err := s.DeclineMember(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ApproveLeaveHandler approves leave requests
//
//	@Summary		Approve leave requests
//	@Description	The identities are removed from the space, as if they had left after the owner's approval.
//	@Id				approve_leave
//	@Tags			Members
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without approving"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.LeaveApproveRequest	true	"The identities whose leave is approved"
//	@Success		200				{object}	v2model.AcceptResponse			"Leave approved"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller cannot approve leave"
//	@Failure		404				{object}	v2model.Error					"No pending leave for those identities"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/leave/approve [post]
func ApproveLeaveHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.LeaveApproveRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the approval takes identities"), maxSpaceAdminRequestBody, "leave approval") {
			return
		}
		result, err := s.ApproveLeave(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// CancelJoinHandler withdraws a pending join
//
//	@Summary		Cancel a pending join
//	@Description	Withdraws this account's join request for a space it has not been admitted to yet.
//	@Id				cancel_join
//	@Tags			Members
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool					false	"Validate without cancelling"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key replays the stored response"
//	@Success		200				{object}	v2model.AcceptResponse	"Join cancelled"
//	@Failure		404				{object}	v2model.Error			"No pending join for this space"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/join/cancel [post]
func CancelJoinHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.CancelJoin(c.Request.Context(), c.Param("space_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ShareSpaceHandler makes a space shareable
//
//	@Summary		Share a space
//	@Description	Makes a private space shareable so an invite can be minted. A space type that cannot be shared is a 409.
//	@Id				share_space
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool					false	"Validate without sharing"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key replays the stored response"
//	@Success		200				{object}	v2model.AcceptResponse	"Space is shareable"
//	@Failure		403				{object}	v2model.Error			"The caller cannot share this space"
//	@Failure		404				{object}	v2model.Error			"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/sharing [post]
func ShareSpaceHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.ShareSpace(c.Request.Context(), c.Param("space_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// StopSharingSpaceHandler stops sharing a space
//
//	@Summary		Stop sharing a space
//	@Description	The invite stops working and the space is private again. Members already in the space stay.
//	@Id				stop_sharing_space
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool					false	"Validate without stopping"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key replays the stored response"
//	@Success		200				{object}	v2model.AcceptResponse	"Space is private"
//	@Failure		403				{object}	v2model.Error			"The caller cannot stop sharing"
//	@Failure		404				{object}	v2model.Error			"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/sharing [delete]
func StopSharingSpaceHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.StopSharingSpace(c.Request.Context(), c.Param("space_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ChangeSpaceOwnershipHandler transfers space ownership
//
//	@Summary		Transfer ownership
//	@Description	Irreversible. The new owner must already be a member. old_owner_permissions decides what the caller keeps, and no_permissions leaves the space.
//	@Id				change_space_ownership
//	@Tags			Sharing
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string							true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool							false	"Validate the body without transferring"
//	@Param			Idempotency-Key	header		string							false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.ChangeOwnershipRequest	true	"The new owner and the caller's remaining role"
//	@Success		200				{object}	v2model.AcceptResponse			"Ownership transferred"
//	@Failure		400				{object}	v2model.Error					"Validation failure"
//	@Failure		403				{object}	v2model.Error					"The caller is not the owner"
//	@Failure		404				{object}	v2model.Error					"Space or identity not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/ownership [post]
func ChangeSpaceOwnershipHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.ChangeOwnershipRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the transfer takes new_owner_identity and old_owner_permissions"), maxSpaceAdminRequestBody, "ownership") {
			return
		}
		result, err := s.ChangeSpaceOwnership(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// DeleteSpaceHandler deletes a space
//
//	@Summary		Delete a space
//	@Description	Irreversible. The space and its local data are removed for every member; the answer reports the deletion timestamp.
//	@Id				delete_space
//	@Tags			Sharing
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate without deleting"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key replays the stored response"
//	@Success		200				{object}	v2model.DeleteSpaceResponse	"Deleted, with the timestamp"
//	@Failure		403				{object}	v2model.Error				"The caller is not the owner"
//	@Failure		404				{object}	v2model.Error				"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id} [delete]
func DeleteSpaceHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.DeleteSpace(c.Request.Context(), c.Param("space_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetSpaceHomepageHandler sets a space's homepage
//
//	@Summary		Set the space homepage
//	@Description	The homepage is the object (or widget) the space opens on. An empty homepage is a 400.
//	@Id				set_space_homepage
//	@Tags			Spaces
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetHomepageRequest	true	"The homepage object id"
//	@Success		200				{object}	v2model.AcceptResponse		"Homepage set"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		403				{object}	v2model.Error				"The caller cannot change the homepage"
//	@Failure		404				{object}	v2model.Error				"Space not found or not live"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/homepage [put]
func SetSpaceHomepageHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetHomepageRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the homepage body takes homepage"), maxSpaceAdminRequestBody, "homepage") {
			return
		}
		result, err := s.SetSpaceHomepage(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
