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
