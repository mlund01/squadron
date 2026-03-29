package functions

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// SchemaFunctions returns the map of HCL schema helper functions.
// These are registered in every EvalContext so config authors can use shorthand
// schema definitions instead of verbose field blocks.
//
// Primitives — string / number / integer / bool:
//
//	city    = string("Target city", true)               // required
//	region  = string("Region", { default = "us-east-1" }) // optional with default
//	count   = number("Result count")                    // optional, no default
//
// Lists — list(inner_type, description, required?):
//
//	tags    = list(string, "Labels to apply", true)
//	scores  = list(number, "Numeric scores")
//
// Maps — map(value_type, description, required?) — free-form, no field schema:
//
//	headers = map(string, "HTTP headers")
//	counts  = map(number, "Counts by category")
//
// Objects — object(properties, description, required?) — schematic, fields defined:
//
//	coord = object({
//	  lat = number("Latitude", true)
//	  lon = number("Longitude", true)
//	}, "Geographic coordinates", true)
//
//	items = list(object({
//	  id    = integer("Item ID", true)
//	  label = string("Item label")
//	}), "Order items", true)
func SchemaFunctions() map[string]function.Function {
	return map[string]function.Function{
		"string":  makePrimitiveFunc("string"),
		"number":  makePrimitiveFunc("number"),
		"integer": makePrimitiveFunc("integer"),
		"bool":    makePrimitiveFunc("bool"),
		"list":    makeListFunc(),
		"map":     makeMapFunc(),
		"object":  makeObjectFunc(),
	}
}

// SchemaTypeVars returns cty variable values for bare type references.
// These allow writing list(string, "desc") where string is a variable, not a function call.
// The same cty shape as a function call result but with empty description and required=false.
func SchemaTypeVars() map[string]cty.Value {
	return map[string]cty.Value{
		"string":  typeRef("string"),
		"number":  typeRef("number"),
		"integer": typeRef("integer"),
		"bool":    typeRef("bool"),
	}
}

// typeRef builds the cty.Value for a bare type reference (no description, not required).
func typeRef(kind string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"kind":        cty.StringVal(kind),
		"description": cty.StringVal(""),
		"required":    cty.BoolVal(false),
	})
}

// makePrimitiveFunc creates a schema helper function for a primitive type (string/number/integer/bool).
//
// Signature: kind(description, [bool_required | options_object]?)
//
//	string("desc")                          — optional field
//	string("desc", true)                    — required field
//	string("desc", { default = "high" })    — optional with default
//	string("desc", { secret = true })       — secret
func makePrimitiveFunc(kind string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "description", Type: cty.String},
		},
		VarParam: &function.Parameter{
			Name:        "options",
			Type:        cty.DynamicPseudoType,
			AllowMarked: false,
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			desc := args[0].AsString()
			required, extras, err := extractPrimitiveVarArgs(args[1:])
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s(): %w", kind, err)
			}
			return buildSchemaNode(kind, desc, required, extras), nil
		},
	})
}

// makeListFunc creates the list() schema helper function.
//
// Signature: list(inner_type, [description, [bool_required | options_object]?]?)
//
//	list(string, "Tags to apply")
//	list(number, "Scores", true)
//	list(object({ name = string("Name", true) }), "Items", true)
func makeListFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "inner_type", Type: cty.DynamicPseudoType},
		},
		VarParam: &function.Parameter{
			Name: "rest",
			Type: cty.DynamicPseudoType,
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			innerType := args[0]
			if err := validateSchemaNode(innerType, "list() first argument"); err != nil {
				return cty.NilVal, err
			}
			desc, required, extras, err := extractCompositeVarArgs(args[1:])
			if err != nil {
				return cty.NilVal, fmt.Errorf("list(): %w", err)
			}
			attrs := schemaNodeAttrs("list", desc, required, extras)
			attrs["items"] = innerType
			return cty.ObjectVal(attrs), nil
		},
	})
}

// makeMapFunc creates the map() schema helper function.
//
// Signature: map(value_type, [description, [bool_required | options_object]?]?)
//
//	map(string, "Arbitrary key-value pairs")
//	map(number, "Scores by category", true)
func makeMapFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "value_type", Type: cty.DynamicPseudoType},
		},
		VarParam: &function.Parameter{
			Name: "rest",
			Type: cty.DynamicPseudoType,
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			valueType := args[0]
			if err := validateSchemaNode(valueType, "map() first argument"); err != nil {
				return cty.NilVal, err
			}
			desc, required, extras, err := extractCompositeVarArgs(args[1:])
			if err != nil {
				return cty.NilVal, fmt.Errorf("map(): %w", err)
			}
			attrs := schemaNodeAttrs("map", desc, required, extras)
			attrs["items"] = valueType
			return cty.ObjectVal(attrs), nil
		},
	})
}

// makeObjectFunc creates the object() schema helper function.
//
// Signature: object(properties_object, [description, [bool_required | options_object]?]?)
//
//	coords = object({
//	  lat = number("Latitude", true)
//	  lon = number("Longitude", true)
//	}, "Geographic coordinates", true)
//
// When used as a type reference inside list(), description and required may be omitted:
//
//	items = list(object({ name = string("Name", true) }), "Item list")
func makeObjectFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "properties", Type: cty.DynamicPseudoType},
		},
		VarParam: &function.Parameter{
			Name: "rest",
			Type: cty.DynamicPseudoType,
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			props := args[0]
			if !props.Type().IsObjectType() {
				return cty.NilVal, fmt.Errorf(
					"object(): first argument must be an object literal { key = type(...) }, got %s",
					props.Type().FriendlyName(),
				)
			}
			desc, required, extras, err := extractCompositeVarArgs(args[1:])
			if err != nil {
				return cty.NilVal, fmt.Errorf("object(): %w", err)
			}
			attrs := schemaNodeAttrs("object", desc, required, extras)
			attrs["properties"] = props
			return cty.ObjectVal(attrs), nil
		},
	})
}

// ── internal helpers ──────────────────────────────────────────────────────────

// buildSchemaNode constructs a schema node cty.Value with the given kind, description,
// required flag, and any extra attributes (e.g. default, secret).
func buildSchemaNode(kind, desc string, required bool, extras map[string]cty.Value) cty.Value {
	attrs := schemaNodeAttrs(kind, desc, required, extras)
	return cty.ObjectVal(attrs)
}

// schemaNodeAttrs builds the attribute map for a schema node.
// Returns a mutable map suitable for adding "items" or "properties" before calling cty.ObjectVal.
func schemaNodeAttrs(kind, desc string, required bool, extras map[string]cty.Value) map[string]cty.Value {
	attrs := map[string]cty.Value{
		"kind":        cty.StringVal(kind),
		"description": cty.StringVal(desc),
		"required":    cty.BoolVal(required),
	}
	for k, v := range extras {
		attrs[k] = v
	}
	return attrs
}

// validateSchemaNode verifies that a cty.Value looks like a schema node
// (an object type with a "kind" attribute).
func validateSchemaNode(v cty.Value, label string) error {
	if !v.Type().IsObjectType() || !v.Type().HasAttribute("kind") {
		return fmt.Errorf(
			"%s must be a type reference (e.g. string, number, object({...})), got %s",
			label, v.Type().FriendlyName(),
		)
	}
	return nil
}

// extractPrimitiveVarArgs parses the variadic args for primitive functions.
// Expected pattern: [(bool | options_object)]?
func extractPrimitiveVarArgs(args []cty.Value) (required bool, extras map[string]cty.Value, err error) {
	extras = make(map[string]cty.Value)
	if len(args) == 0 {
		return false, extras, nil
	}
	arg := args[0]
	switch {
	case arg.Type() == cty.Bool:
		required = arg.True()
	case arg.Type().IsObjectType():
		required, extras = extractOptionsObject(arg)
	default:
		return false, nil, fmt.Errorf(
			"second argument must be a bool (required) or an options object { default = ..., secret = ... }, got %s",
			arg.Type().FriendlyName(),
		)
	}
	return required, extras, nil
}

// extractCompositeVarArgs parses the variadic args for composite functions (list/map/object).
// Expected pattern: [description_string?, (bool | options_object)?]
func extractCompositeVarArgs(args []cty.Value) (desc string, required bool, extras map[string]cty.Value, err error) {
	extras = make(map[string]cty.Value)
	idx := 0

	// First optional vararg: description string
	if idx < len(args) && args[idx].Type() == cty.String {
		desc = args[idx].AsString()
		idx++
	}

	// Second optional vararg: required bool or options object
	if idx < len(args) {
		arg := args[idx]
		switch {
		case arg.Type() == cty.Bool:
			required = arg.True()
		case arg.Type().IsObjectType():
			required, extras = extractOptionsObject(arg)
		default:
			return "", false, nil, fmt.Errorf(
				"expected bool (required) or options object, got %s",
				arg.Type().FriendlyName(),
			)
		}
	}
	return desc, required, extras, nil
}

// extractOptionsObject pulls required, default, and secret out of an options cty.Object.
func extractOptionsObject(obj cty.Value) (required bool, extras map[string]cty.Value) {
	extras = make(map[string]cty.Value)

	if obj.Type().HasAttribute("required") {
		if v := obj.GetAttr("required"); v.Type() == cty.Bool && !v.IsNull() {
			required = v.True()
		}
	}
	if obj.Type().HasAttribute("default") {
		if v := obj.GetAttr("default"); !v.IsNull() {
			extras["default"] = v
		}
	}
	if obj.Type().HasAttribute("secret") {
		if v := obj.GetAttr("secret"); !v.IsNull() {
			extras["secret"] = v
		}
	}
	return required, extras
}
