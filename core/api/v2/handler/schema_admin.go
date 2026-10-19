package v2handler

// schema_admin.go holds the property and type maintenance handlers. Each is a
// thin wrapper: the service owns validation, the grant backstop and the RPC
// mapping. Every mutation honors ?dry_run=true and runs behind the
// route-level Idempotency-Key middleware.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/anyproto/anytype-heart/core/api/pagination"
	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	v2service "github.com/anyproto/anytype-heart/core/api/v2/service"
)

// RemovePropertyOptionHandler removes one option from a select property
//
//	@Summary		Remove a property option
//	@Description	Deletes one option from a select or multiSelect property. An option objects still hold is refused with a 409 unless delete_in_objects is true; those objects then keep a value the property no longer offers. The option is named by its id or by its name, both served by the property's option list.
//	@Id				remove_property_option
//	@Tags			Properties
//	@Produce		json
//	@Param			space_id			path		string					true	"Space id, from GET /v2/spaces"
//	@Param			key					path		string					true	"Property key, from GET /v2/spaces/{space_id}/properties"
//	@Param			option				path		string					true	"Option id or name, from GET /v2/spaces/{space_id}/properties/{key}/options"
//	@Param			delete_in_objects	query		bool					false	"Remove the option even while objects hold it"
//	@Param			dry_run				query		bool					false	"Validate without removing the option"
//	@Param			Idempotency-Key		header		string					false	"Replay guard: the same key with the same body replays the stored response"
//	@Success		200					{object}	v2model.AcceptResponse	"Option removed"
//	@Failure		404					{object}	v2model.Error			"Property, option or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/properties/{key}/options/{option} [delete]
func RemovePropertyOptionHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.RemovePropertyOption(c.Request.Context(), c.Param("space_id"), c.Param("key"), c.Param("option"),
			c.Query("delete_in_objects") == "true", isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetPropertyOptionsOrderHandler sets the picker order of a property's options
//
//	@Summary		Set a property's option order
//	@Description	Replaces the option order of a select or multiSelect property with the given list. It is the whole order, so every option the property holds must be named once; an option left out is refused rather than left to interleave. The option list route sorts by name, and this is what the picker shows.
//	@Id				set_property_options_order
//	@Tags			Properties
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string									true	"Space id, from GET /v2/spaces"
//	@Param			key				path		string									true	"Property key, from GET /v2/spaces/{space_id}/properties"
//	@Param			dry_run			query		bool									false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string									false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetPropertyOptionsOrderRequest	true	"The new option order"
//	@Success		200				{object}	v2model.PropertyOptionsOrder			"The property's options in their stored order"
//	@Failure		400				{object}	v2model.Error							"Unknown, duplicated or missing option"
//	@Failure		404				{object}	v2model.Error							"Property or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/properties/{key}/options/order [put]
func SetPropertyOptionsOrderHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetPropertyOptionsOrderRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the order body takes options, every option of the property once"), maxObjectAdminRequestBody, "option order") {
			return
		}
		result, err := s.SetPropertyOptionsOrder(c.Request.Context(), c.Param("space_id"), c.Param("key"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// ListPropertiesWithValueHandler lists the properties carrying a value
//
//	@Summary		List properties carrying a value
//	@Description	Names the properties that hold the given value somewhere in the space, each with the number of objects holding it. It is the reverse of a read: the question "what points at this" an object id, a date object id or a text value asks. The value is matched as a string, so a number stored as a number is not found by its digits.
//	@Id				list_properties_with_value
//	@Tags			Properties
//	@Produce		json
//	@Param			space_id	path		string											true	"Space id, from GET /v2/spaces"
//	@Param			value		query		string											true	"The value to look for, as it is spelled in a property"
//	@Param			offset		query		int												false	"Items to skip"		default(0)
//	@Param			limit		query		int												false	"Items to return"	default(25)
//	@Success		200			{object}	v2model.ListResponse[v2model.RelationValueRow]	"Property rows with a count"
//	@Failure		400			{object}	v2model.Error									"Value is missing"
//	@Failure		404			{object}	v2model.Error									"Space not found or unavailable"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/properties/with-value [get]
func ListPropertiesWithValueHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		offset := c.GetInt(pagination.QueryParamOffset)
		limit := c.GetInt(pagination.QueryParamLimit)
		rows, total, hasMore, err := s.ListPropertiesWithValue(c.Request.Context(), c.Param("space_id"), c.Query("value"), offset, limit)
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, v2model.NewListResponse(rows, total, offset, limit, hasMore,
			"request the next offset"))
	}
}

// ListTypeConflictsHandler lists the properties a type does not recommend
//
//	@Summary		List a type's conflicting properties
//	@Description	The properties objects of this type carry that the type does not recommend. It is the read half of the app's conflict notice, and the repair is the resolve route beside it.
//	@Id				list_type_conflicts
//	@Tags			Types
//	@Produce		json
//	@Param			space_id	path		string										true	"Space id, from GET /v2/spaces"
//	@Param			type		path		string										true	"Type key, from GET /v2/spaces/{space_id}/types"
//	@Param			offset		query		int											false	"Items to skip"		default(0)
//	@Param			limit		query		int											false	"Items to return"	default(25)
//	@Success		200			{object}	v2model.ListResponse[v2model.PropertyRow]	"Conflicting property rows"
//	@Failure		404			{object}	v2model.Error								"Type or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/types/{type}/conflicts [get]
func ListTypeConflictsHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		offset := c.GetInt(pagination.QueryParamOffset)
		limit := c.GetInt(pagination.QueryParamLimit)
		rows, total, hasMore, err := s.ListTypeConflicts(c.Request.Context(), c.Param("space_id"), c.Param("type"), offset, limit)
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, v2model.NewListResponse(rows, total, offset, limit, hasMore,
			"request the next offset"))
	}
}

// ResolveTypeConflictsHandler aligns a type's objects with the type
//
//	@Summary		Resolve a type's conflicts
//	@Description	Brings every object of this type in line with the type's current layout and recommended properties. It is the repair for what the conflict read reports, and it is the caller's to run after a type was edited.
//	@Id				resolve_type_conflicts
//	@Tags			Types
//	@Produce		json
//	@Param			space_id		path		string					true	"Space id, from GET /v2/spaces"
//	@Param			type			path		string					true	"Type key, from GET /v2/spaces/{space_id}/types"
//	@Param			dry_run			query		bool					false	"Validate without aligning anything"
//	@Param			Idempotency-Key	header		string					false	"Replay guard: the same key with the same body replays the stored response"
//	@Success		200				{object}	v2model.AcceptResponse	"Objects aligned"
//	@Failure		404				{object}	v2model.Error			"Type or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/types/{type}/resolve [post]
func ResolveTypeConflictsHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := s.ResolveTypeConflicts(c.Request.Context(), c.Param("space_id"), c.Param("type"), isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// SetTypesOrderHandler sets the space's type order
//
//	@Summary		Set the type order
//	@Description	The types listed come first, in the given order, and the rest keep their relative order. This is the order the app offers types in, and it is written as one list so one unknown spelling refuses the whole order.
//	@Id				set_types_order
//	@Tags			Types
//	@Accept			json
//	@Produce		json
//	@Param			space_id		path		string							true	"Space id, from GET /v2/spaces"
//	@Param			dry_run			query		bool							false	"Validate the body without committing"
//	@Param			Idempotency-Key	header		string							false	"Replay guard: the same key with the same body replays the stored response"
//	@Param			request			body		v2model.SetTypesOrderRequest	true	"The types that move, in their new order"
//	@Success		200				{object}	v2model.TypesOrder				"The types that moved, in their new order"
//	@Failure		400				{object}	v2model.Error					"Unknown or duplicated type"
//	@Failure		404				{object}	v2model.Error					"Type or space not found"
//	@Security		bearerauth
//	@Router			/v2/spaces/{space_id}/types/order [put]
func SetTypesOrderHandler(s *v2service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v2model.SetTypesOrderRequest
		if !decodeStrictJSONBody(c, &req, v2model.Plain("the order body takes types"), maxObjectAdminRequestBody, "type order") {
			return
		}
		result, err := s.SetTypesOrder(c.Request.Context(), c.Param("space_id"), req, isV2DryRun(c))
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
