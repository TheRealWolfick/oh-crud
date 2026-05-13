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

type Webhook struct {
	On_insert *string `yaml:"on-insert"`
	On_update *string `yaml:"on-update"`
	On_delete *string `yaml:"on-delete"`
	On_any    *string `yaml:"on-any"`
}

type DataModelFieldRules struct {
	Min *int         `yaml:"min"`
	Max *int         `yaml:"max"`
	Max_length *int  `yaml:"max-length"`
	Pattern *string  `yaml:"pattern"`
	Enum []string    `yaml:"enum"`
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
	Rules *DataModelFieldRules `yaml:"rules"`
}

// DataModelFieldPublicSchema is the public/exposed allowance of a datamodelfield.
type DataModelFieldPublicSchema struct {
	// Field metadata
	Type               string  `yaml:"type"`
	JSON               string  `yaml:"json"`
	Required           bool    `yaml:"required-on-insert"`
	Skip_insert        bool    `yaml:"skip-insert"`
	// Database metadata
	DB_type            string `yaml:"db-type"`
	Nullable           bool   `yaml:"nullable"`
	Default            string `yaml:"default"`
	// Atlas metadata
	Rules              *DataModelFieldRules `yaml:"rules"`
}

// End_pointsAllowed controls which HTTP methods are enabled for a given endpoint.
type End_pointsAllowed struct {
	GET          []string `yaml:"GET"`
	PUT          []string `yaml:"PUT"`
	POST         []string `yaml:"POST"`
	DELETE       []string `yaml:"DELETE"`
	PUT_GROUP    []string `yaml:"PUT-GROUP"`
	POST_GROUP   []string `yaml:"POST-GROUP"`
	DELETE_GROUP []string `yaml:"DELETE-GROUP"`
}

// DataModel is the top-level representation of a YAML config file.
type DataModel struct {
	// Model metadata
	Name                 *string  `yaml:"name"`
	Type                 *string  `yaml:"type"`
	Version              *string  `yaml:"version"`
	Track_history        *bool    `yaml:"track-history"`
	Track_history_field  *string  `yaml:"track-history-field"`
	Soft_delete          *bool    `yaml:"soft-delete"`
	Webhooks             *Webhook `yaml:"web-hooks"`
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

// DataModelPublicSchema is available item that can be sent back to the end user / exposed.
type DataModelPublicSchema struct {
	// Model metadata
	Name                 string                                `yaml:"name"`
	Version              string                                `yaml:"version"`
	// Database metadata
	Primary_key          string                                `yaml:"primary-key"`
	Unique_keys          [][]string                            `yaml:"-"`
	Fields               map[string]DataModelFieldPublicSchema `yaml:"fields"`
}

func ptr[T any](v T) *T { return &v }

func GetSoftDeleteConfig() *DataModelField {
	return &DataModelField{
		Type: ptr("bool"),
    JSON: ptr("deleted"),
		JSON_alias: []string{"is_deleted", "deleted_flag"},
    DB_type: ptr("boolean"),
    Default: ptr("false"),
		Skip_insert: ptr(true),
    Include_in_diff: ptr(false),
	}
}
