package datamodel

import (
	"path/filepath"
	"testing"

	"lotusforge.au/api-server/models"
)

// Every model config in the repo must pass validation. A failure only shows as a
// warning at startup, so catch it here instead.
func TestShippedModelConfigsValidate(t *testing.T) {
	files, err := filepath.Glob("../config/*/*.yaml")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}

	validated := 0
	for _, f := range files {
		m, err := LoadYAMLIntoModel[models.DataModel](f)
		// server.yaml and function files are not models
		if err != nil || m.Name == nil || m.Fields == nil {
			continue
		}
		validated++
		if err := ValidateDataModel(*m); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if validated == 0 {
		t.Fatalf("No model configs found to validate")
	}
}
