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
		
		// Need to do checks on if this is a valid config file

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

// Returns the YAML field name (= DB column name) of the diff comparator.
// Reads from the top-level diff-comparator config key.
// Falls back to the first PK field if diff-comparator is not set.
func GetDiffComparatorKey(cfg *models.DataModel) string {
	if cfg.Diff_Comparator != nil && *cfg.Diff_Comparator != "" {
		return *cfg.Diff_Comparator
	}
	// Fall back to first PK
	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.PK != nil && *field_cfg.PK {
			return field_name
		}
	}
	return ""
}

// Returns the set of DB column names (YAML keys) that should be excluded from diff
// comparison — those with diff:false. Fields with diff:nil are included.
func BuildExcludeKeysFromConfig(cfg *models.DataModel) map[string]bool {
	excluded := map[string]bool{}
	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.Diff != nil && !*field_cfg.Diff {
			excluded[field_name] = true
		}
	}
	return excluded
}

func GetRequiredJSONFields_FromConfig(cfg *models.DataModel, enforce_pk bool) []string {

	// Initiate slice of required fields
	req_fields := []string{}

	// Iterate through each config field and check if it is required
	for _, field_cfg := range cfg.Fields {
		switch enforce_pk {
		case true:
			if *field_cfg.PK {
				req_fields = append(req_fields, *field_cfg.JSON)
			}
		case false:
			if *field_cfg.Req || *field_cfg.PK {
				req_fields = append(req_fields, *field_cfg.JSON)
			}
		}
	}

	return req_fields
}

func CheckConfigIsValid(cfg models.DataModel) (bool, []models.ConfigError) {
	errors := []models.ConfigError{}

	if cfg.Name == nil || *cfg.Name == "" { errors = append(errors, models.ConfigError{Field: "Name", Message: "Missing 'name'"})} 
	if cfg.Version == nil { errors = append(errors, models.ConfigError{Field: "Version", Message: "Missing 'version'"})} 
	if cfg.Table_Name == nil { errors = append(errors, models.ConfigError{Field: "Table Name", Message: "Missing 'table-name'"})} 
	if cfg.End_Point == nil { errors = append(errors, models.ConfigError{Field: "End Point", Message: "Missing 'end-point'"})} 
	if cfg.Allow == nil { errors = append(errors, models.ConfigError{Field: "Allow", Message: "Missing 'allow' end points parameters"})} 
	if cfg.Fields == nil { errors = append(errors, models.ConfigError{Field: "Fields", Message: "Missing 'fields'"})} 
	
	// Diff validation
	if cfg.Allow_Diff != nil && *cfg.Allow_Diff == true {
		if cfg.Diff_Comparator == nil || *cfg.Diff_Comparator == "" {errors = append(errors, models.ConfigError{Field: "Diff Comparator", Message: "Diff allowed without a comparator"})}
	}

	// Return any errors if they exist
	if len(errors) > 0 { return false, errors}
	return true, nil
}

