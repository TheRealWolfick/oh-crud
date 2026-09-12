package models

// ConfigError reports a validation error on a specific field.
type ConfigError struct {
	Field   string
	Message string
}

type ForeignKey struct {
	Fields                   []string `yaml:"foreign-key-fields"`
	Target_table             *string  `yaml:"foreign-key-target-table"`
	Target_fields            []string `yaml:"foreign-key-target-fields"`
	Target_field_description *string  `yaml:"foreign-key-target-field-description"`
	ON_UPDATE                *string  `yaml:"foreign-key-on-update"`
	ON_DELETE                *string  `yaml:"foreign-key-on-delete"`
}

type UniqueKey struct {
	Fields []string `yaml:"unique-key-fields"`
}

type EventStatus struct {
	Queued  []string `yaml:"queued"`
	Start   []string `yaml:"start"`
	Success []string `yaml:"success"`
	Warn    []string `yaml:"warn"`
	Fail    []string `yaml:"fail"`
	Error   []string `yaml:"error"`
	All     []string `yaml:"all"`
}

type EventAction struct {
	On_get    *EventStatus `yaml:"on-get"`
	On_insert *EventStatus `yaml:"on-insert"`
	On_update *EventStatus `yaml:"on-update"`
	On_delete *EventStatus `yaml:"on-delete"`
	On_any    *EventStatus `yaml:"on-any"`
}

type DataModelFieldRules struct {
	Min *int         `yaml:"min"`
	Max *int         `yaml:"max"`
	Max_length *int  `yaml:"max-length"`
	Pattern *string  `yaml:"pattern"`
	Enum []string    `yaml:"enum"`
}

type DataModelJsonSelect struct {
	Action *string    `yaml:"action"`
	ApplyOn *string  `yaml:"apply-on"`
	TreatAs *string     `yaml:"treat-as"`
}

// DataModelField describes the schema for a single field within a DataModel.
type DataModelField struct {
	// Field metadata
	Type               *string              `yaml:"type"`
	JSON               *string              `yaml:"json"`
	JSON_alias         []string             `yaml:"json-alias"`
	Include_in_diff    *bool                `yaml:"include-in-diff"`
	Required_on_insert *bool                `yaml:"required-on-insert"`
	Absolute_match     *bool                `yaml:"absolute-match"`
	Skip_insert        *bool                `yaml:"skip-insert"`
	Description        *string              `yaml:"description"`
	// Database metadata
	DB_type            *string              `yaml:"db-type"`
	Nullable           *bool                `yaml:"nullable"`
	Default            *string              `yaml:"default"`
	// Atlas metadata
	Migration          *string              `yaml:"migration"`
	// Rules metadata
	Private            *bool                `yaml:"private"`
	Rules              *DataModelFieldRules `yaml:"rules"`
	JSON_select        *DataModelJsonSelect `yaml:"json-select"`
	Meta               map[string]any       `yaml:"meta"`
}

// DataModelFieldPublicSchema is the public/exposed allowance of a datamodelfield.
type DataModelFieldPublicSchema struct {
	// Field metadata
	Type               string               `yaml:"type"`
	JSON               string               `yaml:"json"`
	Required           bool                 `yaml:"required-on-insert"`
	Skip_insert        bool                 `yaml:"skip-insert"`
	Description        *string              `yaml:"description"`
	// Database metadata
	DB_type            string               `yaml:"db-type"`
	Nullable           bool                 `yaml:"nullable"`
	Default            string               `yaml:"default"`
	// Atlas metadata
	Rules              *DataModelFieldRules `yaml:"rules"`
	// Rules metadata
	Meta               map[string]any       `yaml:"meta"`
	// Select_override describes a json-select abstraction on this field, if configured
	// (see DataModelJsonSelect). Nil for every field that isn't abstracted.
	Select_override    *DataModelJsonSelect `yaml:"json-select"`
	// Select_expression is the SQL expression GET responses actually select for this
	// field when Select_override is set (e.g. "jsonb_array_length(missing_from_supplied)"),
	// aliased back onto Select_override.TreatAs rather than the field's stored Type/DB_type.
	// Empty when Select_override is nil.
	Select_expression  string               `yaml:"select-expression"`
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
	DIFF         []string `yaml:"DIFF"`
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
	Webhooks             *EventAction `yaml:"web-hooks"`
	// Database metadata
	Table_name          *string                   `yaml:"table-name"`
	End_point           *string                   `yaml:"end-point"`
	End_points_allowed  *End_pointsAllowed        `yaml:"end-points-allowed"`
	Allow_diff          *bool                     `yaml:"allow-diff"`
	Diff_comparator     *string                   `yaml:"diff-comparator"`
	Primary_key         *string                  `yaml:"primary-key"`
	Foreign_keys        map[string]ForeignKey     `yaml:"foreign-keys"`
	Unique_keys         map[string]UniqueKey      `yaml:"unique-keys"`
	Fields              map[string]DataModelField `yaml:"fields"`
	Admin_roles         []string                  `yaml:"admin-roles"`
	Filepath            *string                   `yaml:"-"`
}

// DataModelPublicSchema is available item that can be sent back to the end user / exposed.
type DataModelPublicSchema struct {
	// Model metadata
	Name                 string                                `yaml:"name"`
	Version              string                                `yaml:"version"`
	// Database metadata
	Table_name           string                                `yaml:"table_name"`
	Primary_key          string                                `yaml:"primary-key"`
	Foreign_keys         []ForeignKey                          `yaml:"-"`
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
