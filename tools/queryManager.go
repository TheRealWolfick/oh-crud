package tools

import (
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"lotusforge.au/api-server/models"
)

type QueryBuilder struct {
	values   map[string]uint
	where    map[string]uint
	args     []any
	fields   []string
	pos      uint
	wheremod map[string]string
	query    string
	logger   *slog.Logger
	limit    int
	offset   int
	sort     []string
}

type SetCallback interface {
	Set(field string, value any)
}

// NewQueryBuilder creates a fresh query builder with no values or clauses set.
func NewQueryBuilder(logger *slog.Logger) *QueryBuilder {
	return &QueryBuilder{
		values:   make(map[string]uint),
		where:    make(map[string]uint),
		args:     []any{},
		fields:   []string{},
		pos:      1,
		wheremod: make(map[string]string),
		query:    "",
		logger:   logger,
		limit:    0,
		offset:   0,
		sort:     []string{},
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
	qb.logger.Debug("Setting where value", "field", field, "value", value)
	_, exists := qb.where[field]
	if !exists {
		qb.where[field] = qb.pos
		qb.wheremod[field] = mod
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.logger.Debug("Field already exists in where map")
		qb.args[qb.values[field]] = value
		qb.wheremod[field] = mod
	}
}

func (qb *QueryBuilder) SetWhereAbsolute(field string, value any) {
	if value == nil {
		return
	}
	qb.logger.Debug("Setting absolute where value", "field", field, "value", value)
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

	c := []string{}
	v := []string{}
	insert_time := time.Now().UTC()

	for pos, row := range data {
		qb.logger.Debug("Inserting row", "pos", pos, "row", fmt.Sprint(row))
		local_values := []string{}

		for field_name, field_cfg := range cfg.Fields {
			if field_cfg.Skip_insert != nil && *field_cfg.Skip_insert {
				qb.logger.Debug("Field set to skip insert", "field", field_name)
				continue
			}
			if pos == 0 {
				c = append(c, field_name)
			}

			val, ok := row[field_name]
			if ok {
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
				qb.args = append(qb.args, val)
			} else {
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
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
			}
		}
		v = append(v, fmt.Sprintf("(%s)", strings.Join(local_values, ", ")))
	}

	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", *cfg.Table_name, strings.Join(c, ", "), strings.Join(v, ", "))
	fmt.Println(qb.query)
	return qb.query
}

func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	sb := strings.Builder{}
	
	sb.WriteString(fmt.Sprintf("SELECT %s FROM %s", strings.Join(select_fields, ", "), table))

	if len(qb.where) > 0 {
		w := make([]string, 0)
		for key, val := range qb.where {
			if reflect.TypeOf(qb.args[val-1]).Kind() == reflect.Slice {
				w = append(w, fmt.Sprintf("%s IN $%d", key, val))
			} else {
				w = append(w, fmt.Sprintf("%s %s $%d", key, qb.wheremod[key], val))
			}
		}
		sb.WriteString(fmt.Sprintf(" WHERE %s", strings.Join(w, " AND ")))
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

// ProcessURLParams reads URL query parameters and applies matching WHERE clauses
// to the query builder based on the DataModel field config.
func (qb *QueryBuilder) ProcessURLParams(r *http.Request, cfg *models.DataModel) error {
	qb.logger.Debug("Setting where values from URL")
	if err := r.ParseForm(); err != nil {
		return err
	}

	var page_int int
	var page_size_int int
	page := r.FormValue("page")
	page_size := r.FormValue("page_size")
	fields := r.FormValue("fields")

	// Page logic
	if page == "" || page == "0" || !IsInt(page) {
		page_int = 1
		qb.logger.Debug("Set page_int to default value of 1")
	} else {
		page_int = ConvertToInt(page)
	}
	if page_size == "" || page_size == "0" || !IsInt(page_size) {
		page_size_int = 25
		qb.logger.Debug("Set page_size_int to default value of 25")
	} else {
		page_size_int = ConvertToInt(page_size)
	}
	if page_size_int < 0 {
		page_size_int = 0
	}
	if page_int < 0 {
		page_int = 0
	}
	if strings.ToLower(page) != "all" {
		qb.SetLimit(page_size_int)
		qb.SetOffset(page_size_int * (page_int - 1))
	}

	// Fields logic
	if fields != "" {
		fields_slice := strings.Split(fields, ",")
		for _, field := range fields_slice {
			f, allowed := CheckFieldGetValid(field, cfg)
			if allowed { qb.fields = append(qb.fields, f) }
		}
	}

	// Sort logic
	sort_string := r.FormValue("sort_by")
	if sort_string != "" {
		temp_strings := strings.Split(sort_string, ",")
		for _, s := range temp_strings {
			temp := strings.Split(s, "~")
			field_name, found := CheckFieldExists(strings.Trim(temp[0], " "), cfg)
			if found {
				if len(temp) > 1 {
					qb.sort = append(qb.sort, fmt.Sprintf("%s %s", field_name, ASCorDESC(temp[1])))
				} else {
					qb.sort = append(qb.sort, fmt.Sprintf("%s %s", field_name, "ASC"))
				}
				qb.logger.Debug("Appended a sort column", "field_name", field_name)
			}
		}
	}

	if len(r.URL.Query()) < 1 && page == "" && page_size == "" {
		qb.logger.Debug("No valid URL values passed, skipping field checks")
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
		dereferenced := ValueDeref(field_cfg.Type)
		qb.logger.Debug("Dereferenced the field type", "field_type", dereferenced)
		if !dereferenced.IsValid() {
			return fmt.Errorf("invalid data type found in config %q", *cfg.Name)
		}
		field_type, err := DecodeFieldType(dereferenced.Interface().(string))
		if err != nil {
			return err
		}

		is_abs := field_cfg.Absolute_match != nil && *field_cfg.Absolute_match
		if is_abs {
			if !ValidateValue(field_type, url_value) {
				continue
			}
			qb.SetWhereAbsolute(field_name, url_value)
		} else {
			qb.SetWhere(field_name, url_value, field_type)
		}
	}

	return nil
}

