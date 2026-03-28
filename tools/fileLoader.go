package tools

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"lotusforge.au/api-server/models"
)

func LoadModel_YAML(path string) (*models.BaseModel, error) { 
	// If this is a yaml file
	if filepath.Ext(path) == ".yaml" {

		// Create new data model and read data into it
		model := models.NewBaseModel()
		
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
