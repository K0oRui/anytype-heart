package v2model

// object_admin.go holds the request and response DTOs for object
// administration: the object-level verbs that act on a whole object
// (duplicate, archive/restore, layout, set/collection conversion, source,
// bookmark refresh, icon and cover) and the bulk verbs that act on many at
// once (archive, type, properties, collection order). The bulk verbs answer
// per object: a batch is not atomic, so what applied and what refused are
// both reported. Property values speak the same vocabulary as the
// set_properties op, because the service routes them through it.

// SetObjectsArchivedRequest is the body of POST
// /v2/spaces/{space_id}/objects/archive. It is the bulk form of DELETE
// /v2/spaces/{space_id}/objects/{object_id}: the same creator-provenance
// gate and the same allowlist run for every id. It is also the way back out
// of the Bin, since archived=false restores.
type SetObjectsArchivedRequest struct {
	Ids []string `json:"ids"`
	// Archived defaults to true. false restores (unarchives).
	Archived *bool `json:"archived,omitempty"`
}

// SetObjectsTypeRequest is the body of PATCH /v2/spaces/{space_id}/objects/type.
type SetObjectsTypeRequest struct {
	Ids []string `json:"ids"`
	// Type is the type every object becomes, by key, api key or name.
	Type string `json:"type"`
}

// SetObjectsPropertiesRequest is the body of PATCH /v2/spaces/{space_id}/objects.
// The four fields are the set_properties op's, so a value means exactly what
// it means in a PATCH body: set replaces, unset removes, add and remove edit
// a list-shaped property.
type SetObjectsPropertiesRequest struct {
	Ids    []string         `json:"ids"`
	Set    map[string]any   `json:"set,omitempty"`
	Unset  []string         `json:"unset,omitempty"`
	Add    map[string][]any `json:"add,omitempty"`
	Remove map[string][]any `json:"remove,omitempty"`
}

// BulkFailure is one object a bulk verb refused, with the refusal's message.
type BulkFailure struct {
	Id      string `json:"id"`
	Message string `json:"message"`
}

// BulkResult is the answer of a bulk verb: the ids it applied to and the
// refusals beside them. A batch is not atomic, so a 200 says the request ran,
// not that every object changed.
type BulkResult struct {
	Ids    []string      `json:"ids"`
	Failed []BulkFailure `json:"failed,omitempty"`
	DryRun bool          `json:"dry_run,omitempty"`
}

// SetObjectLayoutRequest is the body of PATCH
// /v2/spaces/{space_id}/objects/{object_id}/layout.
type SetObjectLayoutRequest struct {
	// Layout is a layout name, as the type schemas spell it: basic, todo,
	// note, profile, bookmark, set, collection, and the media layouts.
	Layout string `json:"layout"`
}

// ConvertObjectRequest is the body of POST
// /v2/spaces/{space_id}/objects/{object_id}/convert.
type ConvertObjectRequest struct {
	// To is "set" or "collection".
	To string `json:"to"`
	// Source names the type(s) a set is based on; required for "set",
	// refused for "collection".
	Source []string `json:"source,omitempty"`
}

// SetObjectSourceRequest is the body of PATCH
// /v2/spaces/{space_id}/objects/{object_id}/source: the types a set is based
// on, replacing the current ones.
type SetObjectSourceRequest struct {
	Source []string `json:"source"`
}

// ObjectIconRequest is the body of PUT
// /v2/spaces/{space_id}/objects/{object_id}/icon. The variant is chosen by
// format; a member the chosen variant does not use is ignored, and the format
// itself refuses a variant missing its required member.
type ObjectIconRequest struct {
	// Format is "emoji", "file", "icon" or "color".
	Format string `json:"format"`
	// Emoji is the glyph, for format=emoji.
	Emoji string `json:"emoji,omitempty"`
	// File is an image object id in this space, for format=file.
	File string `json:"file,omitempty"`
	// Name is a built-in icon name, for format=icon.
	Name string `json:"name,omitempty"`
	// Color tints the icon, or is the icon itself for format=color. It is a
	// palette name: grey, yellow, orange, red, pink, purple, blue, ice, teal
	// or lime, the same ten the app's picker offers.
	Color string `json:"color,omitempty"`
}

// ObjectCoverRequest is the body of PUT
// /v2/spaces/{space_id}/objects/{object_id}/cover.
type ObjectCoverRequest struct {
	// Format is "image", "color" or "gradient".
	Format string `json:"format"`
	// File is an image object id in this space, for format=image.
	File string `json:"file,omitempty"`
	// Color is a flat colour name, for format=color. The store keeps the name
	// as the app's picker spells it.
	Color string `json:"color,omitempty"`
	// Gradient is a gradient name, for format=gradient.
	Gradient string `json:"gradient,omitempty"`
	// Scale, X and Y position an image cover, for format=image.
	Scale float64 `json:"scale,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
}

// CreateObjectFromUrlRequest is the body of POST
// /v2/spaces/{space_id}/objects/from-url: a bookmark object minted from a
// link, with the link's title, description and image filled in.
type CreateObjectFromUrlRequest struct {
	Url string `json:"url"`
	// Type is the type the object takes; defaults to the bookmark type.
	Type string `json:"type,omitempty"`
	// AddPageContent stores the page's readable content as blocks.
	AddPageContent bool `json:"add_page_content,omitempty"`
	// Template is the template the object starts from.
	Template string `json:"template,omitempty"`
}

// SetObjectItemsRequest is the body of PUT
// /v2/spaces/{space_id}/objects/{object_id}/items: the complete new order of
// a collection's items.
type SetObjectItemsRequest struct {
	Ids []string `json:"ids"`
}
