package store

import "lotusforge.au/api-server/models"

func ptr[T any](v T) *T { return &v }

// basicModel builds a minimal DataModel used across model-function tests.
func basicModel() models.DataModel {
	return models.DataModel{
		Name:        ptr("Test Model"),
		Version:     ptr("1.0.0"),
		Table_name:  ptr("test_table"),
		End_point:   ptr("test"),
		Primary_key: ptr("id"),
		End_points_allowed: &models.End_pointsAllowed{
			GET:  []string{},
			POST: []string{},
			PUT:  []string{},
		},
		Unique_keys: map[string]models.UniqueKey{
			"test_uq_code": {Fields: []string{"code"}},
		},
		Fields: map[string]models.DataModelField{
			"id": {
				Type:        ptr("int"),
				JSON:        ptr("id"),
				DB_type:     ptr("int"),
				Skip_insert: ptr(true),
			},
			"code": {
				Type:               ptr("string"),
				JSON:               ptr("code"),
				DB_type:            ptr("text"),
				Required_on_insert: ptr(true),
			},
			"name": {
				Type:     ptr("string"),
				JSON:     ptr("name"),
				DB_type:  ptr("text"),
				Nullable: ptr(true),
			},
			"count": {
				Type:     ptr("int"),
				JSON:     ptr("count"),
				DB_type:  ptr("int"),
				Nullable: ptr(true),
			},
			"active": {
				Type:    ptr("bool"),
				JSON:    ptr("active"),
				DB_type: ptr("boolean"),
				Default: ptr("true"),
			},
			"secret": {
				Type: 	 ptr("string"),
				DB_type: ptr("text"),
				Private: ptr(true),
				Rules: &models.DataModelFieldRules{
					Max_length: ptr(5),
				},
			},
		},
	}
}
