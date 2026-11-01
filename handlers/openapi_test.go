package handlers

import (
	"strings"
	"testing"

	"lotusforge.au/api-server/models"
)

func TestBuildOpenAPISpec_IncludesDeclarativeFunctions(t *testing.T) {
	dataModels := []models.DataModel{*diffResourceCfg()}
	fn := &models.FunctionDef{
		Name:        ptr("assets-by-building"),
		Service:     ptr("inventory"),
		Bound_to:    ptr("assets"),
		Description: ptr("Count assets per building."),
		Parameters: []models.Parameter{
			{Name: ptr("category"), Field: ptr("asset_category")},
		},
	}

	spec := buildOpenAPISpec(dataModels, []*models.FunctionDef{fn})

	path, ok := spec.Paths["/inventory/assets/fn/assets-by-building"]
	if !ok {
		t.Fatalf("expected a path for the declarative function, got paths: %v", mapKeys(spec.Paths))
	}
	if path.Get == nil {
		t.Fatalf("expected a GET operation for the function path")
	}
	if path.Get.Tags[0] != "inventory/asset-data" {
		t.Errorf("expected the function to be tagged with the bound model's service and name, got %v", path.Get.Tags)
	}
	if path.Get.Description != "Count assets per building." {
		t.Errorf("expected the function's own description to be used, got %q", path.Get.Description)
	}

	foundCategoryParam := false
	for _, p := range path.Get.Parameters {
		if p.Name == "category" {
			foundCategoryParam = true
		}
	}
	if !foundCategoryParam {
		t.Errorf("expected the function's declared parameter to appear, got %v", path.Get.Parameters)
	}
}

func TestBuildModelSchema_DescribesJsonSelectOverride(t *testing.T) {
	m := *diffModelCfg()
	schema := buildModelSchema(m)

	overridden := schema.Properties["missing_from_supplied"]
	if overridden == nil {
		t.Fatalf("expected a schema property for missing_from_supplied")
	}
	if !strings.Contains(overridden.Description, "jsonb_array_length(missing_from_supplied)") {
		t.Errorf("expected the description to name the select expression, got %q", overridden.Description)
	}
	if !strings.Contains(overridden.Description, "int") {
		t.Errorf("expected the description to name the effective GET type, got %q", overridden.Description)
	}

	plain := schema.Properties["diff_type"]
	if plain == nil {
		t.Fatalf("expected a schema property for diff_type")
	}
	if plain.Description != "" {
		t.Errorf("expected no override description on a plain field, got %q", plain.Description)
	}
}

func mapKeys(m map[string]oaPathItem) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Two services may each have a model of the same name. Their schemas and operation ids
// come from the qualified table names, so neither overwrites the other.
func TestBuildOpenAPISpec_SameModelNameInTwoServices(t *testing.T) {
	inv := *diffResourceCfg()
	inv.Table_name = ptr("inventory_assets")
	inv.End_points_allowed.GET = []string{"reader"}
	per := *diffResourceCfg()
	per.Service = ptr("permits")
	per.Table_name = ptr("permits_assets")
	per.End_points_allowed = &models.End_pointsAllowed{GET: []string{"reader"}}

	spec := buildOpenAPISpec([]models.DataModel{inv, per}, nil)

	for _, schema := range []string{"InventoryAssets", "PermitsAssets"} {
		if _, ok := spec.Components.Schemas[schema]; !ok {
			t.Errorf("expected schema %s, got %d schemas", schema, len(spec.Components.Schemas))
		}
	}
	a, b := spec.Paths["/inventory/assets"], spec.Paths["/permits/assets"]
	if a.Get == nil || b.Get == nil {
		t.Fatalf("expected a GET on both services' paths, got %v", mapKeys(spec.Paths))
	}
	if a.Get.OperationID == b.Get.OperationID {
		t.Errorf("operation ids collide: %q", a.Get.OperationID)
	}
	if a.Get.Tags[0] == b.Get.Tags[0] {
		t.Errorf("tags collide: %q", a.Get.Tags[0])
	}
}
