package tools

import (
	"fmt"
	"net/http"

	"lotusforge.au/api-server/models"
)

// GetChecksum extracts the "checksum" query parameter from the request URL.
// Returns an empty string if the parameter is absent or the URL cannot be parsed.
func GetChecksum(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	if len(r.URL.Query()) < 1 {
		return ""
	}
	return r.URL.Query().Get("checksum")
}

// DynamicSetWhereFromURL reads URL query parameters and applies matching WHERE clauses
// to the query builder based on the DataModel field config.
// Absolute fields use SetWhereAbsolute; all others use the typed SetWhere.
func DynamicSetWhereFromURL(qb *QueryBuilder, r *http.Request, cfg *models.DataModel) error {
	qb.logger.Debug("Setting where values from URL")
	if err := r.ParseForm(); err != nil {
		return err
	}

	if len(r.URL.Query()) < 1 {
		qb.logger.Debug("No valid URL values passed")
		return nil
	}

	for _, field_cfg := range cfg.Fields {
		if field_cfg.DB == nil || *field_cfg.DB == "" || *field_cfg.DB == "-" { continue }

		url_value := r.FormValue(*field_cfg.JSON)
		if url_value == "" { continue }

		dereferenced := DynamicValueDeref(field_cfg.Type)
		qb.logger.Debug("Dereferenced the field type", "field_type", dereferenced)
		if !dereferenced.IsValid() {
			return fmt.Errorf("invalid data type found in config %q", *cfg.Name)
		}
		field_type, err := DecodeDynamicFieldType(dereferenced.Interface().(string))
		if err != nil { return err }

		is_abs := *field_cfg.Absolute
		if is_abs {
			if ValidateValue(field_type, url_value) == false {
				continue
			}
			if field_cfg.Custom_Where != nil {
				qb.SetWhereAbsolute(*field_cfg.Custom_Where, url_value)
			} else {
				qb.SetWhereAbsolute(*field_cfg.DB, url_value)
			}
		} else {
			if field_cfg.Custom_Where != nil {
				qb.SetWhere(*field_cfg.Custom_Where, url_value, field_type)
			} else {
				qb.SetWhere(*field_cfg.DB, url_value, field_type)
			}
		}
	}

	return nil
}

// DynamicGetDatabaseColumns returns DB column names from the DataModel config.
// Pass pk_only=true to return only primary key columns, req_only=true for required+PK,
// or both false for all columns.
func DynamicGetDatabaseColumns(cfg *models.DataModel, pk_only bool, req_only bool) []string {
	database_columns := []string{}
	for _, field_cfg := range cfg.Fields {
		if pk_only || req_only {
			if pk_only {
				if *field_cfg.PK { database_columns = append(database_columns, *field_cfg.DB) }
			} else {
				if *field_cfg.Req || *field_cfg.PK { database_columns = append(database_columns, *field_cfg.DB) }
			}
		} else {
			database_columns = append(database_columns, *field_cfg.DB)
		}
		database_columns = append(database_columns, *field_cfg.DB)
	}
	return database_columns
}
