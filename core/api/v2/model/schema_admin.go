package v2model

// schema_admin.go holds the request and response DTOs for property and type
// administration: removing and reordering a property's select options,
// finding the properties that carry a value, and the type conflict and
// ordering verbs. An option is addressed by its id or by its name, and the
// option list route serves both.

// SetPropertyOptionsOrderRequest is the body of PUT
// /v2/spaces/{space_id}/properties/{key}/options/order: every option of the
// property, in the order the picker should show. It is the whole order, not a
// fragment: an option left out keeps its own place and interleaves with the
// ones given, so the route refuses a partial list rather than guess at the
// caller's intent.
type SetPropertyOptionsOrderRequest struct {
	Options []string `json:"options"`
}

// PropertyOptionsOrder is the answer of the option order write: the
// property's options as the store holds them after the write.
type PropertyOptionsOrder struct {
	DryRun  bool        `json:"dry_run,omitempty"`
	Options []OptionRow `json:"options"`
}

// RelationValueRow is one property that carries a searched value, with the
// number of objects in the space that carry it. A key that no live property
// answers to is still reported: the count is real, and hiding it would hide a
// match.
type RelationValueRow struct {
	Key    string `json:"key"`
	Name   string `json:"name,omitempty"`
	Format string `json:"format,omitempty"`
	Count  int64  `json:"count"`
}

// SetTypesOrderRequest is the body of PUT /v2/spaces/{space_id}/types/order.
// The types listed come first in the given order; the rest keep their
// relative order, so a caller can move a few without naming every type in the
// space.
type SetTypesOrderRequest struct {
	Types []string `json:"types"`
}

// TypesOrder is the answer of the type order write: the types that moved, in
// their new order.
type TypesOrder struct {
	DryRun bool      `json:"dry_run,omitempty"`
	Types  []TypeRow `json:"types"`
}
