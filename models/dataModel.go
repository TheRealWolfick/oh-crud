package models

// ConfigError reports a validation error on a specific field.
type ConfigError struct {
	Field   string
	Message string
}

type ForeignKey struct {
	Fields        []string `yaml:"foreign-key-fields"`
	Target_table  *string  `yaml:"foreign-key-target-table"`
	Target_fields []string `yaml:"foreign-key-target-fields"`
	ON_UPDATE     *string  `yaml:"foreign-key-on-update"`
	ON_DELETE     *string  `yaml:"foreign-key-on-delete"`
}

type UniqueKey struct {
	Fields []string `yaml:"unique-key-fields"`
}

// DataModelField describes the schema for a single field within a DataModel.
type DataModelField struct {
	// Field metadata
	Type               *string  `yaml:"type"`
	JSON               *string  `yaml:"json"`
	JSON_alias         []string `yaml:"json-alias"`
	Include_in_diff    *bool    `yaml:"include-in-diff"`
	Required_on_insert *bool    `yaml:"required-on-insert"`
	Absolute_match     *bool    `yaml:"absolute-match"`
	Skip_insert        *bool    `yaml:"skip-insert"`
	// Database metadata
	DB_type  *string `yaml:"db-type"`
	Nullable *bool   `yaml:"nullable"`
	Default  *string `yaml:"default"`
	// Atlas metadata
	Migration *string `yaml:"migration"`
	Private *bool `yaml:"private"`
}

// End_pointsAllowed controls which HTTP methods are enabled for a given endpoint.
type End_pointsAllowed struct {
	GET          *bool `yaml:"GET"`
	PUT          *bool `yaml:"PUT"`
	POST         *bool `yaml:"POST"`
	DELETE       *bool `yaml:"DELETE"`
	PUT_GROUP    *bool `yaml:"PUT-GROUP"`
	POST_GROUP   *bool `yaml:"POST-GROUP"`
	DELETE_GROUP *bool `yaml:"DELETE-GROUP"`
}

// DataModel is the top-level representation of a YAML config file.
type DataModel struct {
	// Model metadata
	Name    *string `yaml:"name"`
	Type    *string `yaml:"type"`
	Version *string `yaml:"version"`
	// Database metadata
	Table_name          *string                   `yaml:"table-name"`
	End_point           *string                   `yaml:"end-point"`
	End_points_allowed  *End_pointsAllowed        `yaml:"end-points-allowed"`
	Allow_diff          *bool                     `yaml:"allow-diff"`
	Diff_comparator     *string                   `yaml:"diff-comparator"`
	Primary_key         *string                   `yaml:"primary-key"`
	Foreign_keys        map[string]ForeignKey     `yaml:"foreign-keys"`
	Unique_keys         map[string]UniqueKey      `yaml:"unique-keys"`
	Fields              map[string]DataModelField `yaml:"fields"`
}
