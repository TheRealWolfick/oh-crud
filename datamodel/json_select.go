package datamodel

import (
	"fmt"

	"lotusforge.au/api-server/models"
)

// ValidateJsonSelectOverride extracts a field override type. Primarily for
// jsonb > int abstraction.
func ValidateJsonSelectOverride(cfg *models.DataModelField) (FieldKind, bool) {
	if cfg.JSON_select == nil { return "", false }
	if cfg.JSON_select.Action == nil || *cfg.JSON_select.Action == "" { return "", false }
	if cfg.JSON_select.ApplyOn == nil || *cfg.JSON_select.ApplyOn == "" { return "", false }
	if cfg.JSON_select.TreatAs == nil || *cfg.JSON_select.TreatAs == "" { return "", false }
	updated_type, err := DecodeFieldType(*cfg.JSON_select.TreatAs)
	if err != nil {
		return "", false
	}
	return updated_type, true
}

// JsonSelectExpr returns the bare SQL expression a json-select abstraction maps a jsonb
// column to (e.g. "jsonb_array_length(col)" for a "count" action) — used by both the
// SELECT list (aliased back to the field's JSON key, see ConvertFieldJsonSelect in
// config.go) and the WHERE clause (used as-is, since aliases aren't valid there).
// Returns ("", false) if no valid abstraction applies to this field.
func JsonSelectExpr(cfg *models.DataModelField) (string, bool) {
	if cfg.DB_type != nil && *cfg.DB_type != "jsonb" { return "", false }
	_, valid := ValidateJsonSelectOverride(cfg)
	if !valid { return "", false }

	switch *cfg.JSON_select.Action {
	case "count":
		return fmt.Sprintf("jsonb_array_length(%s)", *cfg.JSON), true
	default:
		return "", false
	}
}
