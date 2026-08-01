package models

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Parameter declares a URL-exposed parameter for a function. ?Name=value adds
// a WHERE on the named model field at request time.
type Parameter struct {
	Name  *string `yaml:"name"`
	Field *string `yaml:"field"`
	// Op forces a comparison operator. When omitted the executor falls back to
	// the model field's type-driven inference (same logic as the standard GET
	// endpoint — supports >=, <=, ~*, etc.).
	Op       *string `yaml:"op"`
	Required *bool   `yaml:"required"`
}

// FunctionField is one entry in a function's `fields:` list. v1 supports only
// the bare-string form (Field set, no Expression). The mapping form
// {name, expression} is reserved for calculated fields and rejected at load.
type FunctionField struct {
	// Field is set when the YAML entry is a bare string (a model field reference).
	// It's also the explicit form: { field: asset_no }.
	Field string
	// Name is the alias for a calculated field. Reserved for future use.
	Name string
	// Expression is the SQL fragment for a calculated field. Reserved for future use.
	Expression string
}

// UnmarshalYAML accepts either a scalar string ("asset_no") or a mapping
// ({field: asset_no} or {name: alias, expression: ...}). The polymorphism keeps
// v1 YAMLs ergonomic while leaving room for calculated fields without a migration.
func (f *FunctionField) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		f.Field = node.Value
		return nil
	case yaml.MappingNode:
		var aux struct {
			Field      string `yaml:"field"`
			Name       string `yaml:"name"`
			Expression string `yaml:"expression"`
		}
		if err := node.Decode(&aux); err != nil {
			return err
		}
		f.Field = aux.Field
		f.Name = aux.Name
		f.Expression = aux.Expression
		return nil
	default:
		return fmt.Errorf("function field must be a string or mapping, got kind %d", node.Kind)
	}
}

// IsCalculated reports whether this entry describes a calculated field
// (out of scope for v1).
func (f *FunctionField) IsCalculated() bool {
	return f.Expression != ""
}

// FunctionDef is a declarative function loaded from config/functions/<name>.yaml.
// At request time it produces a parameterised SELECT against its bound model.
type FunctionDef struct {
	Name          *string         `yaml:"name"`
	Version       *string         `yaml:"version"`
	Bound_to      *string         `yaml:"bound-to"`
	Description   *string         `yaml:"description"`
	Where         map[string]any  `yaml:"where"`
	Parameters    []Parameter     `yaml:"parameters"`
	Fields        []FunctionField `yaml:"fields"`
	Group_by      []string        `yaml:"group-by"`
	Aggregate     []string        `yaml:"aggregate"`
	Sort_by       []string        `yaml:"sort-by"`
	Roles_allowed []string        `yaml:"roles-allowed"`
	Webhooks      *EventAction    `yaml:"web-hooks"`
}
