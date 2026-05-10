package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"lotusforge.au/api-server/models"
)

type QueryBuilder struct {
	values       map[string]uint
	where        map[string]uint
	whereExtras  []string
	args         []any
	fields       []string
	groups       []string
	pos          uint
	wheremod     map[string]string
	query        string
	logger       *slog.Logger
	limit        int
	offset       int
	sort         []string
}

type SetCallback interface {
	Set(field string, value any)
}

// NewQueryBuilder creates a fresh query builder with no values or clauses set.
func NewQueryBuilder(logger *slog.Logger) *QueryBuilder {
	return &QueryBuilder{
		values:      make(map[string]uint),
		where:       make(map[string]uint),
		whereExtras: []string{},
		args:        []any{},
		fields:      []string{},
		groups:      []string{},
		pos:         1,
		wheremod:    make(map[string]string),
		query:       "",
		logger:      logger,
		limit:       0,
		offset:      0,
		sort:        []string{},
	}
}

func (qb *QueryBuilder) GetArgs() []any { return qb.args }

func (qb *QueryBuilder) GetPage() int {
	if qb.offset == 0 {
		return 1
	}
	return qb.offset/qb.limit + 1
}

func (qb *QueryBuilder) HasFields() bool { return len(qb.fields) > 0 }

func (qb *QueryBuilder) GetFields() []string { return qb.fields }
func (qb *QueryBuilder) GetValues() []any    { return qb.args }

func (qb *QueryBuilder) GetPageSize() int { return qb.offset }

func (qb *QueryBuilder) GetArgsAsString() string {
	args_string := []string{}
	for _, v := range qb.args {
		val := reflect.ValueOf(v)
		if val.Kind() == reflect.Ptr {
			args_string = append(args_string, fmt.Sprintf("%v", val.Elem()))
		} else {
			args_string = append(args_string, fmt.Sprintf("%v", val))
		}
	}
	return strings.Join(args_string, ", ")
}

func (qb *QueryBuilder) SetLimit(i int)  { qb.limit = i }
func (qb *QueryBuilder) SetOffset(i int) { if i > 0 { qb.offset = i } }

func (qb *QueryBuilder) SetValue(field string, value any) {
	_, exists := qb.values[field]
	if !exists {
		qb.values[field] = qb.pos
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.args[qb.values[field]-1] = value
	}
}

func (qb *QueryBuilder) innerSaveValue(value any) int {
	if value == nil {
		return -1
	}
	qb.args = append(qb.args, value)
	qb.pos++
	return int(qb.pos - 1)
}

func (qb *QueryBuilder) SaveArbitraryValue(value any) int {
	return qb.innerSaveValue(value)
}

func (qb *QueryBuilder) innerSetWhere(field string, value any, mod string) {
	if value == nil {
		return
	}
	_, exists := qb.where[field]
	if !exists {
		qb.where[field] = qb.pos
		qb.wheremod[field] = mod
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.args[qb.values[field]] = value
		qb.wheremod[field] = mod
	}
}

// AppendWhere adds a free-form WHERE clause that does NOT deduplicate by field name.
// Use this for range queries on the same column (e.g. changed_at >= $1 AND changed_at <= $2).
func (qb *QueryBuilder) AppendWhere(field, mod string, value any) {
	if value == nil {
		return
	}
	qb.args = append(qb.args, value)
	qb.whereExtras = append(qb.whereExtras, fmt.Sprintf("%s %s $%d", field, mod, qb.pos))
	qb.pos++
}

func (qb *QueryBuilder) SetWhereAbsolute(field string, value any) {
	if value == nil {
		return
	}
	_, exists := qb.where[field]
	if !exists {
		qb.where[field] = qb.pos
		qb.wheremod[field] = "="
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.logger.Debug("Field already exists in where map")
		qb.args[qb.values[field]] = value
		qb.wheremod[field] = "="
	}
}

// setWhere is an internal helper that extracts comparison operators from string values
// and dispatches to the provided setter function with the correct type.
func setWhere(field string, value any, fieldType reflect.Kind, setFunc func(string, any, string)) {
	if value == nil {
		return
	}

	var mod_guess string
	var mod_guess2 string

	if reflect.TypeOf(value).Kind() == reflect.String {
		value_as_string := fmt.Sprintf("%s", value)

		if len(value_as_string) > 0 {
			mod_guess = fmt.Sprint(value_as_string[0:1])
			if len(value_as_string) > 1 {
				mod_guess2 = fmt.Sprint(value_as_string[0:2])
			}

			switch fieldType {
			case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				if mod_guess == "<" || mod_guess == ">" {
					if mod_guess2 == "<=" || mod_guess2 == ">=" {
						if value_as_int, err := strconv.ParseInt(value_as_string[2:], 10, 64); err == nil {
							setFunc(field, value_as_int, mod_guess2)
						}
					} else {
						if value_as_int, err := strconv.ParseInt(value_as_string[1:], 10, 64); err == nil {
							setFunc(field, value_as_int, mod_guess)
						}
					}
				} else {
					if value_as_int, err := strconv.ParseInt(value_as_string, 10, 64); err == nil {
						setFunc(field, value_as_int, "=")
					}
				}

			case reflect.Bool:
				if value_as_bool, err := strconv.ParseBool(value_as_string); err == nil {
					setFunc(field, value_as_bool, "=")
				}

			case reflect.Float32, reflect.Float64:
				if value_as_float, err := strconv.ParseFloat(value_as_string, 64); err == nil {
					setFunc(field, value_as_float, "=")
				}

			case reflect.Struct:
				var dateString string
				var operator string

				if mod_guess2 == "<=" || mod_guess2 == ">=" {
					operator = mod_guess2
					dateString = value_as_string[2:]
				} else if mod_guess == "<" || mod_guess == ">" {
					operator = mod_guess
					dateString = value_as_string[1:]
				} else {
					operator = "="
					dateString = value_as_string
				}

				parsedDate, err := parseDate(dateString)
				if err != nil {
					setFunc(field, value_as_string, "~*")
					return
				}
				setFunc(field, parsedDate, operator)

			default:
				setFunc(field, value_as_string, "~*")
			}
		}
	}
	if reflect.TypeOf(value).Kind() == reflect.Int {
		setFunc(field, value, "=")
	}
}

// parseDate tries common date formats, returning an error if none match.
func parseDate(dateStr string) (time.Time, error) {
	formats := []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"01/02/2006",
		"02/01/2006",
		time.RFC3339,
		time.RFC3339Nano,
	}
	var lastErr error
	for _, format := range formats {
		if parsed, err := time.Parse(format, dateStr); err == nil {
			return parsed, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}

func (qb *QueryBuilder) SetWhere(field string, value any, fieldType reflect.Kind) {
	setWhere(field, value, fieldType, qb.innerSetWhere)
}

func (qb *QueryBuilder) BuildInsert(table string, mod any) string {
	if qb.query != "" {
		return qb.query
	}

	t := reflect.TypeOf(mod)
	vals := reflect.ValueOf(mod)

	if vals.Kind() == reflect.Ptr {
		t = t.Elem()
		vals = vals.Elem()
	}
	if vals.Kind() != reflect.Struct {
		return ""
	}

	db_columns := make([]string, 0)
	value := make([]string, 0)

	for i := 0; i < t.NumField(); i++ {
		db_field := t.Field(i).Tag.Get("db")
		if db_field == "" || db_field == "-" {
			continue
		}
		db_columns = append(db_columns, db_field)
		field := vals.Field(i)

		if field.Kind() == reflect.Ptr && !field.IsNil() {
			value = append(value, fmt.Sprintf("$%d", qb.pos))
			qb.pos++
			qb.args = append(qb.args, field.Elem().Interface())
		} else if field.Kind() != reflect.Ptr && field.IsValid() {
			value = append(value, fmt.Sprintf("$%d", qb.pos))
			qb.pos++
			qb.args = append(qb.args, field.Interface())
		} else {
			empty_value, exists := t.Field(i).Tag.Lookup("none")
			if exists {
				if empty_value == "" {
					value = append(value, "''")
				} else {
					value = append(value, empty_value)
				}
			}
		}
	}

	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", table, strings.Join(db_columns, ", "), fmt.Sprintf("(%s)", strings.Join(value, ", ")))
	return qb.query
}

func (qb *QueryBuilder) BuildMultiInsert(cfg *models.DataModel, data []map[string]any) string {
	if qb.query != "" {
		qb.logger.Warn("Called BuildMultiInsert after a query had already been built")
		return qb.query
	}

	c := []string{} // Columns
	v := []string{} // Values
	insert_time := time.Now().UTC()

	// For each item to be inserted
	for pos, row := range data {
		// Store the values for the this item
		local_values := []string{}

		// Read through the fields / columns of the config
		for field_name, field_cfg := range cfg.Fields {
			// Skip if this field cannot be inserted
			if field_cfg.Skip_insert != nil && *field_cfg.Skip_insert {
				continue
			}
			// If this is the first row, save the column
			if pos == 0 { c = append(c, field_name) }

			// Read this column info from the row data. If this column is not present, it will be !ok
			val, ok := row[field_name]
			if ok {
				qb.args = append(qb.args, val)																		// Save the value into the query builder args list
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))   // Save the position of this value in the query builder args list
				qb.pos++																													// Increase the position marker for the next value
			} else {
				// Handle default values if it was not present
				if field_cfg.Default == nil {
					qb.args = append(qb.args, nil)
				} else if *field_cfg.Default == "" {
					qb.args = append(qb.args, "")
				} else if *field_cfg.Default == "now()" {
					qb.args = append(qb.args, insert_time.Format(time.RFC3339))
				} else {
					parsed, err := CoerceType(*field_cfg.Default, *field_cfg.Type)
					if err != nil {
						qb.logger.Debug("Parse of default value failed", "field_name", field_name, "field_type", *field_cfg.Type)
						qb.args = append(qb.args, nil)
					} else {
						qb.args = append(qb.args, parsed)
					}
				}
				// Save this item into the local value (row level) struture
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
			}
		}
		// Build the local (row level) sql
		v = append(v, fmt.Sprintf("(%s)", strings.Join(local_values, ", ")))
	}

	// Create the final sql by combining the columns and all the row level insert sql strings
	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", *cfg.Table_name, strings.Join(c, ", "), strings.Join(v, ", "))
	return qb.query
}

func (qb *QueryBuilder) BuildUpdateHistory(cfg *models.DataModel, old_values map[string]any, new_values map[string]any, user string) string {
  return qb.buildInsertHistory(cfg, old_values, []map[string]any{new_values}, user, "update", false)
}
func (qb *QueryBuilder) BuildMultiInsertHistory(cfg *models.DataModel, new_values []map[string]any, user string) string {
  return qb.buildInsertHistory(cfg, nil, new_values, user, "insert", true)
}
func (qb *QueryBuilder) buildInsertHistory(cfg *models.DataModel, old_values map[string]any, new_values []map[string]any, user string, t string, use_defaults bool) string {
	lgr := qb.logger.With("buildInsertHistory_type", t)
	if qb.query != "" {
		lgr.Warn("Called BuildMultiInsertHistory after a query had already been built")
		return qb.query
	}

	added_at := time.Now().UTC()
	default_fields := []string{}
	default_values := []any{}
	row_strings := []string{}
	old_vals := []byte{}
	pk_field := *cfg.Primary_key
	if cfg.Track_history_field != nil { pk_field = *cfg.Track_history_field }

	// Use the post-update value of the reference field as `record` whenever the caller
	// supplied it. The asset_data_history.record FK has ON UPDATE CASCADE, so existing
	// history rows have already been migrated by the time we insert. Falling back to
	// old_values keeps inserts (where old_values is empty) working.
	var record_field any
	if t == "update" {
		if v, ok := new_values[0][pk_field]; ok && v != nil {
			record_field = v
		} else {
			record_field = old_values[pk_field]
		}
	}

	// If it is an update, delete anything that isn't a change
	if t == "update" {
		// Delete any fields that haven't changed or are only in old values
		for k, v := range old_values {
			updated, ok := new_values[0][k]
			if ok && updated == v {
				delete(new_values[0], k)
				delete(old_values, k)
				continue
			}
			if !ok { delete(old_values, k) }
		}
		// Delete any values only in new values as well
		for k, _ := range new_values[0] {
			_, ok := old_values[k]; if !ok { delete(new_values[0], k) }
		}
	}

	// return early if there are no updated values
	if len(new_values[0]) == 0 { return "" }
	// Process old values
	if old_values != nil && t == "update" {
		// If old values is present, it is assumed there is only one item in new values as it is an update.

		temp_vals, err := json.Marshal(old_values) 
		if err != nil {
			lgr.Error("Error unmarshalling existing values", "error", err)
		}
		old_vals = temp_vals
	} else { old_vals = nil }

	if use_defaults {
		// Iterate through the fields of the config (to handle defaults)
		for field_name, field_cfg := range cfg.Fields {
			// Skip if not a default field or if it is a now() field as recalculating this will no longer be accurate. We are calculating this separately
			if field_cfg.Default == nil || *field_cfg.Default == "now()" || *field_cfg.Default == "" { continue }
			default_fields = append(default_fields, field_name)
			default_values = append(default_values, *field_cfg.Default)
		}
	}

	// For each item that was added, for updates, this will always be one
	for _, item := range new_values {
		// Build row level entry
		local_string := []string{}

		// Default 
		for i, field := range default_fields {
			_, ok := item[field]
			if ok { continue }
			// value was not included in the supplied data, so the default value must have been lost
			item[field] = default_values[i]
		}

		// Marshal the values to json
		item_json, err := json.Marshal(item)
		if err != nil {
			lgr.Error("Error marshalling item", "item", item)
		}

		// Add record to query builder
		if t == "update" {
			qb.args = append(qb.args, record_field)
		} else {
			qb.args = append(qb.args, item[pk_field])
		}
		local_string = append(local_string, fmt.Sprintf("$%d", qb.pos))
		qb.pos++

		// Add changed_by to string
		qb.args = append(qb.args, user)
		local_string = append(local_string, fmt.Sprintf("$%d", qb.pos))
		qb.pos++

		// Add changed_at to query builder
		qb.args = append(qb.args, added_at.Format(time.RFC3339))
		local_string = append(local_string, fmt.Sprintf("$%d", qb.pos))
		qb.pos++

		// Add old_values to query builder
		qb.args = append(qb.args, old_vals)
		local_string = append(local_string, fmt.Sprintf("$%d", qb.pos))
		qb.pos++

		// Add new_values to query builder
		qb.args = append(qb.args, item_json)
		local_string = append(local_string, fmt.Sprintf("$%d", qb.pos))
		qb.pos++

		// Add fully build local string to row strings
		row_strings = append(row_strings, fmt.Sprintf("(%s)",strings.Join(local_string, ", ")))
	}

	// Build query string
	qb.query = fmt.Sprintf("INSERT INTO %s (record, changed_by, changed_at, old_values, new_values) VALUES %s", fmt.Sprintf("%s_history",*cfg.Table_name), strings.Join(row_strings, ", "))
	return qb.query
}

// buildWhereClause assembles the WHERE fragment from qb.where and qb.whereExtras.
// Returns the empty string if neither is populated, otherwise " WHERE a AND b ...".
func (qb *QueryBuilder) buildWhereClause() string {
	if len(qb.where) == 0 && len(qb.whereExtras) == 0 {
		return ""
	}
	w := make([]string, 0, len(qb.where)+len(qb.whereExtras))
	for key, val := range qb.where {
		if reflect.TypeOf(qb.args[val-1]).Kind() == reflect.Slice {
			w = append(w, fmt.Sprintf("%s IN $%d", key, val))
		} else {
			w = append(w, fmt.Sprintf("%s %s $%d", key, qb.wheremod[key], val))
		}
	}
	w = append(w, qb.whereExtras...)
	return fmt.Sprintf(" WHERE %s", strings.Join(w, " AND "))
}

func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	sb := strings.Builder{}

	sb.WriteString(fmt.Sprintf("SELECT %s FROM %s", strings.Join(select_fields, ", "), table))
	sb.WriteString(qb.buildWhereClause())

	if len(qb.groups) > 0 {
		sb.WriteString(fmt.Sprintf(" GROUP BY %s", strings.Join(qb.groups, ", ")))
	}

	if len(qb.sort) > 0 {
		sb.WriteString(fmt.Sprintf(" ORDER BY %s", strings.Join(qb.sort, ", ")))
	}
	if qb.limit != 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %v", qb.limit))
	}
	if qb.offset != 0 {
		sb.WriteString(fmt.Sprintf(" OFFSET %v", qb.offset))
	}

	sb.WriteString(";")
	return sb.String()
}

func (qb *QueryBuilder) BuildCount(table string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM %s;", table)
}

// BuildCountWithWhere is like BuildCount but applies the same WHERE clauses as BuildSelect.
// It shares qb.args, so the same args slice must be passed to the count query.
func (qb *QueryBuilder) BuildCountWithWhere(table string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM %s%s;", table, qb.buildWhereClause())
}

// BuildUpdate builds a parameterized UPDATE query from the where and value clauses
// already set on the query builder.
func (qb *QueryBuilder) BuildUpdate(cfg *models.DataModel) string {
	if qb.query != "" {
		qb.logger.Warn("Called BuildUpdate_Dynamic after query had already been built")
		return qb.query
	}

	w := make([]string, 0)
	v := make([]string, 0)
	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}
	for key, val := range qb.values {
		v = append(v, fmt.Sprintf("%s = $%d", key, val))
	}

	qb.query = fmt.Sprintf("UPDATE %s SET %s WHERE %s;", *cfg.Table_name, strings.Join(v, ", "), strings.Join(w, " AND "))
	return qb.query
}

func (qb *QueryBuilder) HasUpdates() bool { return len(qb.values) > 0 }

// BuildDelete builds a parameterized DELETE query from the where clauses already set.
func (qb *QueryBuilder) BuildDelete(cfg *models.DataModel) string {
	if qb.query != "" {
		qb.logger.Warn("Called BuildDelete_Dynamic after query had already been built")
		return qb.query
	}

	w := make([]string, 0)
	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}

	qb.query = fmt.Sprintf("DELETE FROM %s WHERE %s;", *cfg.Table_name, strings.Join(w, " AND "))
	return qb.query
}

// FieldResolver maps a user-supplied field token (URL param, sort key, fields list)
// to its real database column name. Returns ("", false) if the token isn't allowed.
type FieldResolver func(string) (string, bool)

// ApplyPagination reads `page` and `page_size` and sets limit/offset.
// `page=all` disables pagination. Defaults: page=1, page_size=25.
// Exported because the function executor (handlers/functionHandler.go) reuses it.
func (qb *QueryBuilder) ApplyPagination(r *http.Request) {
	page := r.FormValue("page")
	page_size := r.FormValue("page_size")

	page_int := 1
	if page != "" && page != "0" && IsInt(page) {
		page_int = ConvertToInt(page)
	}
	page_size_int := 25
	if page_size != "" && page_size != "0" && IsInt(page_size) {
		page_size_int = ConvertToInt(page_size)
	}
	if page_size_int < 0 { page_size_int = 0 }
	if page_int < 0 { page_int = 0 }

	if strings.ToLower(page) != "all" {
		qb.SetLimit(page_size_int)
		qb.SetOffset(page_size_int * (page_int - 1))
	}
}

// processFieldSelect reads the `fields` URL param and adds each token that resolves
// to a real column to qb.fields.
func (qb *QueryBuilder) processFieldSelect(r *http.Request, resolve FieldResolver) {
	raw := r.FormValue("fields")
	if raw == "" { return }
	for field := range strings.SplitSeq(raw, ",") {
		if f, ok := resolve(field); ok {
			qb.fields = append(qb.fields, f)
		}
	}
}

// processSort reads `sort_by` ("col,col2~desc,...") and appends valid entries to qb.sort.
// Default direction is ASC. Tokens that don't resolve are silently skipped.
func (qb *QueryBuilder) processSort(r *http.Request, resolve FieldResolver) {
	raw := r.FormValue("sort_by")
	if raw == "" { return }
	for s := range strings.SplitSeq(raw, ",") {
		temp := strings.Split(s, "~")
		field_name, found := resolve(strings.Trim(temp[0], " "))
		if !found { continue }
		if len(temp) > 1 {
			qb.sort = append(qb.sort, fmt.Sprintf("%s %s", field_name, ASCorDESC(temp[1])))
		} else {
			qb.sort = append(qb.sort, fmt.Sprintf("%s %s", field_name, "ASC"))
		}
	}
}

// processWhereFromConfig walks every field in the model and adds a WHERE clause for any
// URL param matching the field's JSON name. Honors absolute-match and field type for
// operator inference.
func (qb *QueryBuilder) processWhereFromConfig(r *http.Request, cfg *models.DataModel) error {
	for field_name, field_cfg := range cfg.Fields {
		if field_cfg.JSON == nil || *field_cfg.JSON == "" { continue }
		url_value := r.FormValue(*field_cfg.JSON)
		if url_value == "" { continue }
		if field_cfg.Type == nil { continue }
		dereferenced := ValueDeref(field_cfg.Type)
		if !dereferenced.IsValid() {
			return fmt.Errorf("invalid data type found in config %q", *cfg.Name)
		}
		field_type, err := DecodeFieldType(dereferenced.Interface().(string))
		if err != nil { return err }

		is_abs := field_cfg.Absolute_match != nil && *field_cfg.Absolute_match
		if is_abs {
			if !ValidateValue(field_type, url_value) { continue }
			qb.SetWhereAbsolute(field_name, url_value)
		} else {
			qb.SetWhere(field_name, url_value, field_type)
		}
	}
	return nil
}

// AggregateSpec captures the inputs that shape a query's SELECT/GROUP BY/ORDER BY,
// independent of where they came from (URL params or a declarative function YAML).
type AggregateSpec struct {
	Fields    []string // plain field tokens added to SELECT (no aggregate)
	GroupBy   []string // field tokens for GROUP BY
	Aggregate []string // aggregate tokens (count, sum:f, avg:f, min:f, max:f)
	SortBy    []string // sort tokens (col or col~desc)
}

// IsEmpty reports whether the spec produces any SQL contribution.
func (s AggregateSpec) IsEmpty() bool {
	return len(s.Fields) == 0 && len(s.GroupBy) == 0 && len(s.Aggregate) == 0 && len(s.SortBy) == 0
}

// IsAggregating reports whether the spec triggers GROUP BY / aggregate function output.
func (s AggregateSpec) IsAggregating() bool {
	return len(s.GroupBy) > 0 || len(s.Aggregate) > 0
}

// parseAggregateFromURL builds an AggregateSpec from URL params on the standard
// aggregate route (group_by=, aggregate=, sort_by=).
func parseAggregateFromURL(r *http.Request) AggregateSpec {
	return AggregateSpec{
		GroupBy:   splitCSV(r.FormValue("group_by")),
		Aggregate: splitCSV(r.FormValue("aggregate")),
		SortBy:    splitCSV(r.FormValue("sort_by")),
	}
}

// splitCSV returns nil for empty input so callers can use len() == 0 cleanly.
func splitCSV(s string) []string {
	if s == "" { return nil }
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" { out = append(out, t) }
	}
	return out
}

// ApplyAggregateSpec resolves field tokens against the model and populates
// qb.fields / qb.groups / qb.sort. Tokens that don't resolve are silently
// skipped (consistent with the existing aggregate URL behaviour).
func (qb *QueryBuilder) ApplyAggregateSpec(spec AggregateSpec, cfg *models.DataModel) error {
	// 1. Plain SELECT fields.
	for _, field := range spec.Fields {
		f, allowed := CheckFieldGetValid(field, cfg)
		if !allowed { continue }
		if !slices.Contains(qb.fields, f) {
			qb.fields = append(qb.fields, f)
		}
	}

	// 2. GROUP BY columns — also added to SELECT (PostgreSQL requires it).
	for _, field := range spec.GroupBy {
		f, allowed := CheckFieldGetValid(field, cfg)
		if !allowed { continue }
		if !slices.Contains(qb.groups, f) {
			qb.groups = append(qb.groups, f)
		}
		if !slices.Contains(qb.fields, f) {
			qb.fields = append(qb.fields, f)
		}
	}

	// 3. Aggregate function expressions — added to SELECT.
	for _, field := range spec.Aggregate {
		parsed, valid := ParseAggregateFuncString(field, qb, cfg)
		if !valid { continue }
		if slices.Contains(qb.fields, parsed) { continue }
		qb.fields = append(qb.fields, parsed)
	}

	// 4. Sort. Tokens may name an aggregate (resolved via ParseAggregateFuncString),
	//    a group-by column, or a plain field — but the resolved column must already
	//    appear in the SELECT list.
	for _, field := range spec.SortBy {
		sort_slice := strings.Split(field, "~")
		token := strings.TrimSpace(sort_slice[0])
		parsed, valid := ParseAggregateFuncString(token, qb, cfg)
		if !valid {
			if !slices.Contains(qb.groups, token) { continue }
			parsed = token
		}
		if !slices.Contains(qb.fields, parsed) { continue }
		if len(sort_slice) > 1 {
			qb.sort = append(qb.sort, fmt.Sprintf("%s %s", sort_slice[0], ASCorDESC(sort_slice[1])))
		} else {
			qb.sort = append(qb.sort, fmt.Sprintf("%s %s", sort_slice[0], "ASC"))
		}
	}
	return nil
}

// processAggregate handles the GET /{endpoint}/fn/aggregate route — group_by +
// aggregate + sort_by, all keyed off model fields. Thin wrapper around
// parseAggregateFromURL + ApplyAggregateSpec.
func (qb *QueryBuilder) processAggregate(r *http.Request, cfg *models.DataModel) error {
	if r.Method != "GET" { return nil }
	spec := parseAggregateFromURL(r)
	if spec.IsEmpty() {
		return fmt.Errorf("can't call aggregate with no aggregate methods")
	}
	return qb.ApplyAggregateSpec(spec, cfg)
}

// ProcessURLParams reads URL query parameters and applies matching WHERE clauses
// to the query builder based on the DataModel field config. Dispatches on the
// {function} path value.
func (qb *QueryBuilder) ProcessURLParams(r *http.Request, cfg *models.DataModel) error {
	qb.logger.Debug("Setting where values from URL")
	if err := r.ParseForm(); err != nil {
		return err
	}

	switch r.PathValue("function") {
	case "aggregate":
		return qb.processAggregate(r, cfg)
	case "":
		qb.ApplyPagination(r)
		qb.processFieldSelect(r, func(f string) (string, bool) { return CheckFieldGetValid(f, cfg) })
		if err := qb.processWhereFromConfig(r, cfg); err != nil {
			return err
		}
		qb.processSort(r, func(f string) (string, bool) { return CheckFieldExists(strings.Trim(f, " "), cfg) })
		return nil
	default:
		return fmt.Errorf("invalid function %q", r.PathValue("function"))
	}
}

// HistoryColumns is the fixed set of columns on every <table>_history table.
var HistoryColumns = []string{"change_id", "record", "changed_by", "changed_at", "old_values", "new_values"}

// ProcessHistoryURLParams reads URL query parameters for the history endpoint.
// Supports: page, page_size, fields, changed_by, from, to (range on changed_at), sort_by.
// Defaults to ORDER BY changed_at DESC if the caller didn't specify.
func (qb *QueryBuilder) ProcessHistoryURLParams(r *http.Request) error {
	qb.logger.Debug("Setting where values from URL for history")
	if err := r.ParseForm(); err != nil {
		return err
	}

	allowed := make(map[string]struct{}, len(HistoryColumns))
	for _, c := range HistoryColumns { allowed[c] = struct{}{} }
	resolve := func(name string) (string, bool) {
		name = strings.Trim(name, " ")
		if _, ok := allowed[name]; ok { return name, true }
		return "", false
	}

	qb.ApplyPagination(r)
	qb.processFieldSelect(r, resolve)

	if changedBy := r.FormValue("changed_by"); changedBy != "" {
		qb.SetWhereAbsolute("changed_by", changedBy)
	}
	if from := r.FormValue("from"); from != "" {
		t, err := parseDate(from)
		if err != nil {
			return fmt.Errorf("invalid 'from' date %q: %v", from, err)
		}
		qb.AppendWhere("changed_at", ">=", t)
	}
	if to := r.FormValue("to"); to != "" {
		t, err := parseDate(to)
		if err != nil {
			return fmt.Errorf("invalid 'to' date %q: %v", to, err)
		}
		qb.AppendWhere("changed_at", "<=", t)
	}

	qb.processSort(r, resolve)
	if len(qb.sort) == 0 {
		qb.sort = append(qb.sort, "changed_at DESC")
	}
	return nil
}

