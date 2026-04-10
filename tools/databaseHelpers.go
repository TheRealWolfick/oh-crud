package tools

import (
	"fmt"
	"net/http"
	"strings"

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


	// Read pagination details first, error early. Set limits for GET queries
	// Check if page and pagerows present
	var page_int int
	var page_size_int int
	page := r.FormValue("page")
	page_size := r.FormValue("page_size")
	if page == "" || page == "0" || IsInt(page) == false { 
		page_int = 1 
		qb.logger.Debug("Set page_int to default value of 1")
	} else { page_int = ConvertToInt(page)}
	if page_size == "" || page_size == "0" || IsInt(page_size) == false {
		page_size_int = 25
		qb.logger.Debug("Set page_size_int to default value of 25")
	} else { page_size_int = ConvertToInt(page_size) }
	if page_size_int < 0 {page_size_int = 0}
	if page_int < 0 {page_int = 0}
	if strings.ToLower(page) != "all" {
		qb.SetLimit(page_size_int)
		qb.SetOffset(page_size_int * (page_int - 1))
	}
	

	if len(r.URL.Query()) < 1 && page == "" && page_size == "" {
		qb.logger.Debug("No valid URL values passed skipping checks for fields")
		return nil
	}

	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.JSON == nil || *field_cfg.JSON == "" {
			continue
		}

		url_value := r.FormValue(*field_cfg.JSON)
		if url_value == "" {
			continue
		}

		if field_cfg.Type == nil {
			continue
		}
		dereferenced := DynamicValueDeref(field_cfg.Type)
		qb.logger.Debug("Dereferenced the field type", "field_type", dereferenced)
		if !dereferenced.IsValid() {
			return fmt.Errorf("invalid data type found in config %q", *cfg.Name)
		}
		field_type, err := DecodeDynamicFieldType(dereferenced.Interface().(string))
		if err != nil {
			return err
		}

		is_abs := field_cfg.Absolute_match != nil && *field_cfg.Absolute_match
		if is_abs {
			if ValidateValue(field_type, url_value) == false {
				continue
			}
			qb.SetWhereAbsolute(field_name, url_value)
		} else {
			qb.SetWhere(field_name, url_value, field_type)
		}
	}

	return nil
}

// DynamicGetDatabaseColumns returns DB column names (= YAML field names) from the DataModel config.
// Pass pk_only=true to return only the primary key column, req_only=true for required+PK columns,
// or both false for all columns.
func DynamicGetDatabaseColumns(cfg *models.DataModel, pk_only bool, req_only bool) []string {
	database_columns := []string{}
	pk := ""
	if cfg.Primary_key != nil {
		pk = *cfg.Primary_key
	}

	for field_name, field_cfg := range cfg.Fields {
		if pk_only || req_only {
			if pk_only {
				if field_name == pk {
					database_columns = append(database_columns, field_name)
				}
			} else {
				is_pk := field_name == pk
				is_req := field_cfg.Required_on_insert != nil && *field_cfg.Required_on_insert
				if is_pk || is_req {
					database_columns = append(database_columns, field_name)
				}
			}
		} else {
			database_columns = append(database_columns, field_name)
		}
	}
	return database_columns
}
