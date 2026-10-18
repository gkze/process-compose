package types

import "github.com/danielgtaylor/huma/v2"

// enumSchema is the HTTP API schema of a type whose JSON value is one of its
// constants. Types implement huma.SchemaProvider with it next to the constants
// they list.
func enumSchema(jsonType string, values ...any) *huma.Schema {
	return &huma.Schema{Type: jsonType, Enum: values}
}
