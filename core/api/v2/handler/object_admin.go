package v2handler

// object_admin.go holds the object administration handlers. Each is a thin
// wrapper: the service owns validation, the grant backstop and the RPC
// mapping. Every mutation honors ?dry_run=true and runs behind the
// route-level Idempotency-Key middleware.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	v2service "github.com/anyproto/anytype-heart/core/api/v2/service"
)

// maxObjectAdminRequestBody caps administration bodies. 1 MiB is room for a
// large batch of ids.
const maxObjectAdminRequestBody = 1 << 20

// DuplicateObjectHandler duplicates an object
//
//	@Summary		Duplicate an object
//	@Description	Creates a copy with a new id and the same type and content; the source is left untouched.
//	@Id				duplicate_object
//	@Tags			Objects
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string					true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool					false	"Validate without committing"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key with the same body replays the stored response"
//	@Success		201				{object}	v2model.CreateResult	"The copy: its id and type"
//	@Failure		400				{object}	v2model.Error			"Validation failure"
//	@Failure		404				{object}	v2model.Error			"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/duplicate [post]
func DuplicateObjectHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.DuplicateObject(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusCreated, result)
	}
}

// SetObjectsArchivedHandler archives or restores objects
//
//	@Summary		Archive or restore objects
//	@Description	The bulk form of DELETE on an object: the same creator-provenance gate and user-content allowlist run for every id, and archived=false takes objects back out of the Bin. A batch is not atomic; the answer lists what applied and what refused.
//	@Id				set_objects_archived
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string								true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool								false	"Validate the batch without committing"
//	@Param			Idempotency-Key	header		string								false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectsArchivedRequest	true	"The objects and the target state"
//	@Success		200				{object}	v2model.BulkResult					"Applied ids and refusals"
//	@Failure		400				{object}	v2model.Error						"Validation failure"
//	@Failure		403				{object}	v2model.Error						"The caller may archive only objects it created"
//	@Failure		404				{object}	v2model.Error						"Space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/archive [post]
func SetObjectsArchivedHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectsArchivedRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the archive body takes ids and archived"), maxObjectAdminRequestBody, "archive") {
			return
		}
		result, err := s.SetObjectsArchived(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectsTypeHandler changes the type of many objects
//
//	@Summary		Change the type of many objects
//	@Description	Each object goes through the same type change a PATCH set_type op makes, including the layout conversion table. The answer lists the objects that converted and the refusals beside them.
//	@Id				set_objects_type
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string							true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool							false	"Validate the batch without committing"
//	@Param			Idempotency-Key	header		string							false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectsTypeRequest	true	"The objects and the type they become"
//	@Success		200				{object}	v2model.BulkResult				"Converted ids and refusals"
//	@Failure		400				{object}	v2model.Error					"Validation failure, including an unknown type"
//	@Failure		404				{object}	v2model.Error					"Space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/type [patch]
func SetObjectsTypeHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectsTypeRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the type body takes ids and type"), maxObjectAdminRequestBody, "object type") {
			return
		}
		result, err := s.SetObjectsType(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectsPropertiesHandler sets properties on many objects
//
//	@Summary		Set properties on many objects
//	@Description	The body's set, unset, add and remove mean exactly what they mean in a set_properties op, applied to every id with that object's own checks. A batch is not atomic; the answer lists what applied and what refused.
//	@Id				set_objects_properties
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string								true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool								false	"Validate the batch without committing"
//	@Param			Idempotency-Key	header		string								false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectsPropertiesRequest	true	"The objects and the property edits"
//	@Success		200				{object}	v2model.BulkResult					"Updated ids and refusals"
//	@Failure		400				{object}	v2model.Error						"Validation failure"
//	@Failure		404				{object}	v2model.Error						"Space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects [patch]
func SetObjectsPropertiesHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectsPropertiesRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the bulk body takes ids plus set, unset, add or remove"), maxObjectAdminRequestBody, "object properties") {
			return
		}
		result, err := s.SetObjectsProperties(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectLayoutHandler changes an object's layout
//
//	@Summary		Change an object's layout
//	@Description	Layout is what the object looks like, not what it is: a note becomes a page without changing type. Sets and collections get their content from POST …/convert, not from a layout change.
//	@Id				set_object_layout
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectLayoutRequest	true	"The layout name"
//	@Success		200				{object}	v2model.AcceptResponse		"Layout changed"
//	@Failure		400				{object}	v2model.Error				"Unknown layout name"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/layout [patch]
func SetObjectLayoutHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectLayoutRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the layout body takes layout"), maxObjectAdminRequestBody, "layout") {
			return
		}
		result, err := s.SetObjectLayout(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ConvertObjectHandler turns an object into a set or a collection
//
//	@Summary		Convert an object into a set or collection
//	@Description	A set is based on one or more types and answers the query; a collection is assembled by adding items, so it takes no source. Conversion is one way: the object keeps its id.
//	@Id				convert_object
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.ConvertObjectRequest	true	"The target and a set's source"
//	@Success		200				{object}	v2model.AcceptResponse		"Converted"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/convert [post]
func ConvertObjectHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.ConvertObjectRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the convert body takes to and, for a set, source"), maxObjectAdminRequestBody, "conversion") {
			return
		}
		result, err := s.ConvertObject(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectSourceHandler replaces the source of a set
//
//	@Summary		Change a set's source
//	@Description	Replaces the types a set is based on. A source the space does not hold, or a source that yields no viewable content, is refused rather than written.
//	@Id				set_object_source
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectSourceRequest	true	"The types the set is based on"
//	@Success		200				{object}	v2model.AcceptResponse		"Source replaced"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/source [patch]
func SetObjectSourceHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectSourceRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the source body takes source"), maxObjectAdminRequestBody, "set source") {
			return
		}
		result, err := s.SetObjectSource(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// RefreshObjectHandler refetches a bookmark's link
//
//	@Summary		Refresh a bookmark
//	@Description	Fetches the object's stored source link again and updates its title, description and image. The fetch runs after the answer, so the 202 says only that it started; read the object back to see the result.
//	@Id				refresh_object
//	@Tags			Objects
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string					true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool					false	"Validate without starting the fetch"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key with the same body replays the stored response"
//	@Success		202				{object}	v2model.AcceptResponse	"Fetch started"
//	@Failure		400				{object}	v2model.Error			"The object has no source link"
//	@Failure		404				{object}	v2model.Error			"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/refresh [post]
func RefreshObjectHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.RefreshObject(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, result)
	}
}

// CreateObjectFromUrlHandler creates a bookmark from a link
//
//	@Summary		Create an object from a URL
//	@Description	Mints a bookmark object from a link, with the page's title, description and image already filled in. The fetch is synchronous. Set further properties afterwards with PATCH on the object or on many objects at once.
//	@Id				create_object_from_url
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string								true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool								false	"Validate the body without fetching"
//	@Param			Idempotency-Key	header		string								false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.CreateObjectFromUrlRequest	true	"The link and the object's type"
//	@Success		201				{object}	v2model.CreateResult				"The new bookmark: its id and type"
//	@Failure		400				{object}	v2model.Error						"Validation failure"
//	@Failure		404				{object}	v2model.Error						"Space or type not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/from-url [post]
func CreateObjectFromUrlHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.CreateObjectFromUrlRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the body takes url, type, add_page_content and template"), maxObjectAdminRequestBody, "object from url") {
			return
		}
		result, err := s.CreateObjectFromUrl(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusCreated, result)
	}
}

// SetObjectIconHandler sets an object's icon
//
//	@Summary		Set an object's icon
//	@Description	The icon is one of four variants chosen by format: an emoji, an image object in this space, a built-in icon name, or a flat colour. A colour tints the first three; it is a palette name (grey, yellow, orange, red, pink, purple, blue, ice, teal or lime).
//	@Id				set_object_icon
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.ObjectIconRequest	true	"The icon variant"
//	@Success		200				{object}	v2model.AcceptResponse		"Icon set"
//	@Failure		400				{object}	v2model.Error				"Unknown or incomplete icon"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/icon [put]
func SetObjectIconHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.ObjectIconRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the icon body takes format and the variant's member"), maxObjectAdminRequestBody, "icon") {
			return
		}
		result, err := s.SetObjectIcon(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectCoverHandler sets an object's cover
//
//	@Summary		Set an object's cover
//	@Description	The cover is an image object in this space, a flat colour, or a gradient. An image takes scale, x and y to position the crop.
//	@Id				set_object_cover
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Object id, from POST /v2/spaces/{space_id}/search or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.ObjectCoverRequest	true	"The cover variant"
//	@Success		200				{object}	v2model.AcceptResponse		"Cover set"
//	@Failure		400				{object}	v2model.Error				"Unknown or incomplete cover"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/cover [put]
func SetObjectCoverHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.ObjectCoverRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the cover body takes format and the variant's member"), maxObjectAdminRequestBody, "cover") {
			return
		}
		result, err := s.SetObjectCover(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetObjectItemsOrderHandler reorders a collection's items
//
//	@Summary		Reorder a collection's items
//	@Description	Replaces the item order of a collection with the given ids. Ids the collection does not hold are ignored; an item left out keeps its relative place after the listed ones.
//	@Id				set_object_items_order
//	@Tags			Objects
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string						true	"Space id, from GET /v2/spaces"
//	@Param			object_id		path		string						true	"Collection id, from POST /v2/spaces/{space_id}/collections or GET /v2/spaces/{space_id}/objects"
//	@Param			dry_run			query		bool						false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string						false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetObjectItemsRequest	true	"The new item order"
//	@Success		200				{object}	v2model.AcceptResponse		"Order saved"
//	@Failure		400				{object}	v2model.Error				"Validation failure"
//	@Failure		404				{object}	v2model.Error				"Object or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/objects/{object_id}/items [put]
func SetObjectItemsOrderHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetObjectItemsRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the item order body takes ids"), maxObjectAdminRequestBody, "item order") {
			return
		}
		result, err := s.SetObjectItemsOrder(c.Request.Context(), c.Param("space_id"), c.Param("object_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
