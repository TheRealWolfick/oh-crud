package models

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// DataModelField describes the schema for a single field within a DataModel.
// Values are populated from the corresponding YAML config file.
type DataModelField struct {
	Type          *string  `yaml:"type"`
	JSON          *string  `yaml:"json"`
	DB            *string  `yaml:"db"`
	PK            *bool    `yaml:"pk"`
	Req           *bool    `yaml:"req"`
	None          *string  `yaml:"none"`
	Diff          *bool    `yaml:"diff"`
	Custom_Where  *string  `yaml:"custom-where"`
	Absolute      *bool    `yaml:"absolute"`
	Skip_Insert   *bool    `yaml:"skip-insert"`
}

// DataModelAllow controls which HTTP methods are enabled for a given endpoint.
type DataModelAllow struct {
	Get           bool  `yaml:"GET"`
	Put           bool  `yaml:"PUT"`
	Post 	        bool  `yaml:"POST"`
	Delete        bool  `yaml:"DELETE"`
	Put_Group     bool  `yaml:"PUT-GROUP"`
	Post_Group    bool  `yaml:"POST-GROUP"`
	Delete_Group  bool  `yaml:"DELETE-GROUP"`
}

// DataModel is the top-level representation of a YAML config file.
// Each file in config/base-models/ and config/special-models/ is loaded into one of these.
type DataModel struct {
	Name              *string                    `yaml:"name"`
	Table_Name        *string                    `yaml:"table-name"`
  End_Point         *string                    `yaml:"end-point"`
	Allow             *DataModelAllow            `yaml:"allow"`
	Default_Where     map[string]any             `yaml:"default-where"`
	Overwrite_Select  []string                   `yaml:"overwrite-select"`
	Allow_Diff        *bool                      `yaml:"allow-diff"`
	Diff_Comparator   *string                    `yaml:"diff-comparator"`
	Custom_With       *string                    `yaml:"custom-with"`
	Fields            map[string]DataModelField  `yaml:"fields"`
}

func NewDataModel() *DataModel {
	return &DataModel{}
}

// DecodeAndCoerce validates and type-coerces a raw JSON map against the DataModel config.
// Returns a new map keyed by YAML field name (= DB column name) with all values cast to
// their declared types. Returns an error if any required or PK field is missing.
func DecodeAndCoerce(raw map[string]any, cfg *DataModel, enforce_req bool, enforce_pk bool) (map[string]any, error) {
	row_data := map[string]any{}

	for field_name, field_cfg := range cfg.Fields {
		val, exists := raw[*field_cfg.JSON]

		if enforce_pk && !exists && *field_cfg.PK {
			return nil, fmt.Errorf("Missing required primary key field: %s", *field_cfg.JSON)
		}
		if enforce_req && !exists && (*field_cfg.Req || *field_cfg.PK) {
			return nil, fmt.Errorf("Missing required field: %s", *field_cfg.JSON)
		}
		if !exists {continue}

		coerced_val, err := CoerceType(val, *field_cfg.Type)
		if err != nil {
			return nil, err
		}

		row_data[field_name] = coerced_val
	}
	return row_data, nil
}


func DecodeAndCoerceFromDB(raw map[string]any, cfg *DataModel) (map[string]any, error) {
    row_data := map[string]any{}

    for field_name, field_cfg := range cfg.Fields {
        if field_cfg.DB == nil || *field_cfg.DB == "" || *field_cfg.DB == "-" {
            continue
        }

        // Look up by DB column name, not JSON name
        val, exists := raw[*field_cfg.DB]
        if !exists {
            continue
        }

        coerced_val, err := CoerceType(val, *field_cfg.Type)
        if err != nil {
            return nil, err
        }

        // Key the output by field_name (DB column) to match stored map keys
        row_data[field_name] = coerced_val
    }
    return row_data, nil
}

// CoerceType converts a raw value to the named Go type ("int", "float", "string", "bool").
// Returns nil for nil inputs and an error if the conversion fails.
func CoerceType(raw any, type_name string) (any, error) {
	if raw == nil { return nil, nil }
	switch type_name {
	case "int":
		switch v := raw.(type) {
		case int:
			return v, nil
		case float64:
			return int(v), nil
		case string:
			if v == "" { return nil, nil }
			return strconv.Atoi(v)
		case pgtype.Numeric:
			if !v.Valid { return nil, nil }
			f, err := v.Float64Value()
			if err != nil || !f.Valid { return nil, nil }
			return int(f.Float64), nil
		}
	case "float":
		switch v := raw.(type) {
		case float64:
			return v, nil
		case int:
			return float64(v), nil
		case string:
			if v == "" { return nil, nil }
			return strconv.ParseFloat(v, 64)
		case pgtype.Numeric:
			if !v.Valid { return nil, nil }
			f, err := v.Float64Value()
			if err != nil || !f.Valid { return nil, nil }
			return f.Float64, nil
		}
	case "string":
		switch v := raw.(type) {
		case string:
			return v, nil
		case pgtype.Text:
			if !v.Valid { return nil, nil }
			return v.String, nil
		case pgtype.Timestamptz:
			if !v.Valid { return nil, nil }
			return v.Time.UTC().Format(time.RFC3339), nil
		case time.Time:
			return v.UTC().Format(time.RFC3339), nil
		default:
			return fmt.Sprintf("%v", v), nil
		}
	case "bool":
		switch v := raw.(type) {
		case bool:
			return v, nil
		case float64:
			return v != 0, nil
		case string:
			if v == "" { return nil, nil }
			return strconv.ParseBool(v)
		}
	}
	return nil, fmt.Errorf("could not convert %v (%T) into type %s", raw, raw, type_name)
}
