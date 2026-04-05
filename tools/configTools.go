package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
	"lotusforge.au/api-server/models"
)

func LoadModel_YAML(path string) (*models.DataModel, error) {
	// If this is a yaml file
	if filepath.Ext(path) == ".yaml" {

		// Create new data model and read data into it
		model := models.NewDataModel()

		file, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		err = yaml.Unmarshal(file, &model)
		if err != nil {
			return nil, err
		}

		return model, nil
	}
	return nil, errors.New("File passed was not a yaml file")
}

func DecodeDynamicFieldType(typ string) (reflect.Kind, error) {
	switch typ {
	case "int":
		return reflect.Int, nil
	case "float":
		return reflect.Float64, nil
	case "string":
		return reflect.String, nil
	case "bool":
		return reflect.Bool, nil
	}
	return reflect.Invalid, fmt.Errorf("Unsupported data format %s", typ)
}

// GetDiffComparatorKey returns the YAML field name (= DB column) of the diff comparator.
func GetDiffComparatorKey(cfg *models.DataModel) string {
	if cfg.Diff_comparator != nil && *cfg.Diff_comparator != "" {
		return *cfg.Diff_comparator
	}
	return ""
}

// BuildExcludeKeysFromConfig returns the set of DB column names (YAML field names) that should
// be excluded from diff comparison — those with include-in-diff: false.
func BuildExcludeKeysFromConfig(cfg *models.DataModel) map[string]bool {
	excluded := map[string]bool{}
	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.Include_in_diff != nil && !*field_cfg.Include_in_diff {
			excluded[field_name] = true
		}
	}
	return excluded
}

// GetInsertRequiredFields returns the JSON keys of all fields marked required-on-insert.
func GetInsertRequiredFields(cfg *models.DataModel) []string {
	req_fields := []string{}
	for _, field_cfg := range cfg.Fields {
		if field_cfg.Required_on_insert != nil && *field_cfg.Required_on_insert && field_cfg.JSON != nil {
			req_fields = append(req_fields, *field_cfg.JSON)
		}
	}
	return req_fields
}

// GetUpdateKeyOptions returns all valid "key sets" for update/delete row identification,
// expressed as JSON keys. A row is valid if it contains all fields of at least one option:
//   - Option 0: [primary_key_json_key]
//   - Option N: all JSON keys of unique_key_N's fields
func GetUpdateKeyOptions(cfg *models.DataModel) [][]string {
	options := [][]string{}

	// Primary key option
	if cfg.Primary_key != nil {
		if field, ok := cfg.Fields[*cfg.Primary_key]; ok && field.JSON != nil {
			options = append(options, []string{*field.JSON})
		}
	}

	// Unique key options
	for _, uk := range cfg.Unique_keys {
		option := []string{}
		valid := true
		for _, field_name := range uk.Fields {
			field, ok := cfg.Fields[field_name]
			if !ok || field.JSON == nil {
				valid = false
				break
			}
			option = append(option, *field.JSON)
		}
		if valid && len(option) > 0 {
			options = append(options, option)
		}
	}

	return options
}

// FindRowKeyFields locates the key fields (field names = DB column names) present in a coerced row
// that can be used as the WHERE clause for update or delete. Tries the primary key first,
// then each unique key set. Returns the field names and true if a complete key was found.
func FindRowKeyFields(row map[string]any, cfg *models.DataModel) ([]string, bool) {
	// Try primary key first
	if cfg.Primary_key != nil {
		if _, ok := row[*cfg.Primary_key]; ok {
			return []string{*cfg.Primary_key}, true
		}
	}

	// Try each unique key set
	for _, uk := range cfg.Unique_keys {
		all_present := true
		for _, field_name := range uk.Fields {
			if _, ok := row[field_name]; !ok {
				all_present = false
				break
			}
		}
		if all_present && len(uk.Fields) > 0 {
			return uk.Fields, true
		}
	}

	return nil, false
}
