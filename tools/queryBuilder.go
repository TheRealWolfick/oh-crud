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
	values map[string]uint
	where  map[string]uint
	args   []any
	pos    uint
	wheremod map[string]string
	query  string
	logger *slog.Logger
	limit  int
	offset int
}


type SetCallback interface {
	Set(field string, value any)
}


// Create a new blank query builder without a primary key where value.
// Primarily used when there won't be a WHERE clause in the SQL (INSERT)
func NewQueryBuilder(logger *slog.Logger) *QueryBuilder {
	return &QueryBuilder{
		values: make(map[string]uint),
		where: make(map[string]uint),
		args: []any{},
		pos: 1,
		wheremod: make(map[string]string),
		query: "",
		logger: logger,
		limit: 0,
		offset: 0,
	}
}


// Receive the args from the query builder
func (qb *QueryBuilder) GetArgs() []any {
	return qb.args
}

func (qb *QueryBuilder) GetPage() int { 
	if qb.offset == 0 { return 1 }
	return qb.offset / qb.limit + 1
}
func (qb *QueryBuilder) GetPageSize() int { return qb.offset }

// Return all the args as a string (debugging)
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

// Save the limit for the query
func (qb *QueryBuilder) SetLimit(i int) {
	qb.limit = i
}

// Save the offset / page for the query
func (qb *QueryBuilder) SetOffset(i int) {
	if i > 0 {qb.offset = i}
}

// Save a field and value into the query builder. Intended to use with
// updating fields (set only the relevant fields)
func (qb *QueryBuilder) SetValue(field string, value any) {
	// Check to make sure it isn't already in the updates
	_, exists := qb.values[field]
 
	if !exists {
		qb.values[field] = qb.pos
		qb.args = append(qb.args, value)
		qb.pos++	
	} else {
		qb.args[qb.values[field]-1] = value
	}
}


// Inner function for saving an arbitrary value and returning its position
func (qb *QueryBuilder) innerSaveValue(value any) int {
	if value == nil {
		return -1
	}

	qb.args = append(qb.args, value)
	qb.pos++
	return int(qb.pos-1)
}

func (qb *QueryBuilder) SaveArbitraryValue(value any) int {
	return qb.innerSaveValue(value)
}

// Inner function for setting the where value with the mod to apply to the query. Can only be called by internal functions
func (qb *QueryBuilder) innerSetWhere(field string, value any, mod string) {
	if value == nil {
		return
	}

	qb.logger.Debug("Setting where value. Will increment qb.args if it does not exist in eqisting where args", "field", field, "value", value)
	_, exists := qb.where[field]

	if !exists {
		qb.logger.Debug("Doesn't exist currently in qb map!")
		qb.where[field] = qb.pos
		qb.wheremod[field] = mod
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.logger.Debug("Exists in qb map!")
		qb.args[qb.values[field]] = value
		qb.wheremod[field] = mod
	}
}


// Wrapper for directly interfacing with the innerSetWhere function
func (qb *QueryBuilder) SetWhereAbsolute(field string, value any) {
	if value == nil {
		return
	}

	qb.logger.Debug("Setting absolute where value. Will increment qb.args if it does not exist in eqisting where args", "field", field, "value", value)
	_, exists := qb.where[field]

	if !exists {
		qb.logger.Debug("Doesn't exist currently in qb map!")
		qb.where[field] = qb.pos
		qb.wheremod[field] = "="
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.logger.Debug("Exists in qb map!")
		qb.args[qb.values[field]] = value
		qb.wheremod[field] = "="
	}
}


// Helper wrapper function for setting Where values in a query builder. setFunc should be the innerSetWhere func
func setWhere(field string, value any, fieldType reflect.Kind, setFunc func(string, any, string)) {
	if value == nil {
		return
	}

	var mod_guess string
	var mod_guess2 string
  
	// Determine the field type and extract mod based on that
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
						} else {
							return
						}
					} else {
						if value_as_int, err := strconv.ParseInt(value_as_string[1:], 10, 64); err == nil {
							setFunc(field, value_as_int, mod_guess)
						} else {
							return
						}
					}
				} else {
					if value_as_int, err := strconv.ParseInt(value_as_string, 10, 64); err == nil {
						setFunc(field, value_as_int, "=")
					} else {
						return
					}
				}

			case reflect.Bool:
				if value_as_bool, err := strconv.ParseBool(value_as_string); err == nil {
					setFunc(field, value_as_bool, "=")
				} else {
					return
				}

			case reflect.Float32, reflect.Float64:
				if value_as_float, err := strconv.ParseFloat(value_as_string, 64); err == nil {
					setFunc(field, value_as_float, "=")
				} else {
					return
				}

			case reflect.Struct:
				// Handle time.Time fields
				var dateString string
				var operator string

				// Check for comparison operators
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

				// Try to parse the date with common formats
				parsedDate, err := parseDate(dateString)
				if err != nil {
					// If it's not a valid date, treat as string comparison
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

// parseDate tries to parse a date string using common formats
func parseDate(dateStr string) (time.Time, error) {
	// Common date formats to try
	formats := []string{
		"2006-01-02",                // ISO date (YYYY-MM-DD)
		"2006-01-02T15:04:05Z07:00", // ISO 8601 with timezone
		"2006-01-02T15:04:05",       // ISO 8601 without timezone
		"2006-01-02 15:04:05",       // DateTime with space
		"01/02/2006",                // US format (MM/DD/YYYY)
		"02/01/2006",                // EU format (DD/MM/YYYY)
		time.RFC3339,                // RFC3339
		time.RFC3339Nano,            // RFC3339 with nanoseconds
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


// Add a field and value into the where clause
func (qb *QueryBuilder) SetWhere(field string, value any, fieldType reflect.Kind) {
	setWhere(field, value, fieldType, qb.innerSetWhere)
}



// Build the insert query, saving all the values into the args key to be 
// safely loaded into the sql.
func (qb *QueryBuilder) BuildInsert(table string, mod any) string {
	// Early return
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
		// Test to ensure it has a db field
		db_field := t.Field(i).Tag.Get("db")
		if db_field == "" || db_field == "-" {
			continue
		}

		db_columns = append(db_columns, db_field)

		field := vals.Field(i)

		// Check if field is a pointer and not nil
		if field.Kind() == reflect.Ptr && !field.IsNil() {
			value = append(value, fmt.Sprintf("$%d", qb.pos))
			qb.pos++
			// Use .Interface() to get the actual value
			qb.args = append(qb.args, field.Elem().Interface())
		} else if field.Kind() != reflect.Ptr && field.IsValid() {
			// Non-pointer field
			value = append(value, fmt.Sprintf("$%d", qb.pos))
			qb.pos++
			qb.args = append(qb.args, field.Interface())
		} else {
			// Field is nil or invalid, use default from "none" tag
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

	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", table, strings.Join(db_columns, ", "), fmt.Sprintf("(%s)",strings.Join(value, ", ")))

	return qb.query
}


func (qb *QueryBuilder) BuildMultiInsert(cfg *models.DataModel, data []map[string]any) string {

	// Early return if query has already been built
	if qb.query != "" {
		qb.logger.Warn("Called BuildMultiInsert_Dynamic after a query had already been built")
		return qb.query
	}
	
	// BUILD MULTI INSERT QUERY //

	// Initiate columns
	c := []string{}
	v := []string{}
	insert_time := time.Now().UTC()

	// Iterate through each row to be inserted
	for pos, row := range data {

		qb.logger.Debug("Inserting row", "pos", pos, "row", fmt.Sprint(row))
		local_values := []string{}

		// Iterate through each column of the model
		for field_name, field_cfg := range cfg.Fields {

			if field_cfg.Skip_insert != nil && *field_cfg.Skip_insert {
				qb.logger.Debug("Field set to skip insert", "field", field_name)
				continue
			}

			// If this is the first row, add the column names to columns list
			// field_name is the DB column name in the new structure
			if pos == 0 { c = append(c, field_name) }

			// Coerced rows are keyed by field_name (= DB column name)
			val, ok := row[field_name]
			if ok {
				// Value was supplied
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
				qb.args = append(qb.args, val)
			} else {
				if field_cfg.Default == nil {
					// No default specified — send NULL
					qb.args = append(qb.args, nil)
				} else if *field_cfg.Default == "" {
					// Explicit blank string default
					qb.args = append(qb.args, "")
				} else if *field_cfg.Default == "now()" {
					qb.args = append(qb.args, insert_time.Format(time.RFC3339))
				} else {
					// Parse the none value to the correct type
					parsed, err := models.CoerceType(*field_cfg.Default, *field_cfg.Type)
					if err != nil {
						qb.logger.Debug("Parse of none type failed in insert!", "field_name", field_name, "field_type", *field_cfg.Type)
						qb.args = append(qb.args, nil)
					} else {
						qb.args = append(qb.args, parsed)
					}
				}
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
			}
		}
		// Append all the value positions and default values to the values slice
		v = append(v, fmt.Sprintf(("(%s)"), strings.Join(local_values, ", ")))
	}
	// Build the query, save it into the query builder, and return it for use.
	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", *cfg.Table_name, strings.Join(c, ", "), fmt.Sprintf("%s",strings.Join(v, ", ")))
	fmt.Println(qb.query)
	return qb.query
}


// Build the query to select from the database.
// Must supply the table name to be selected from and what fields are required. The fields must be in a slice, even if it is only one value.
func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	if len(qb.where) < 1 {
		qb.logger.Debug(fmt.Sprintf("Limit: %v", qb.limit))
		qb.logger.Debug(fmt.Sprintf("Offset: %v", qb.offset))
		if qb.limit != 0 { 
			if qb.offset != 0 {return fmt.Sprintf("SELECT %s FROM %s LIMIT %v OFFSET %v;", strings.Join(select_fields, ", "), table, qb.limit, qb.offset)}
			return fmt.Sprintf("SELECT %s FROM %s LIMIT %v;", strings.Join(select_fields, ", "), table, qb.limit) }
		return fmt.Sprintf("SELECT %s FROM %s;", strings.Join(select_fields, ", "), table)
	}
	// Initiate slice for where values. Default will be primary key
	w := make([]string, 0)

	for key, val := range qb.where {
		if reflect.TypeOf(qb.args[val-1]).Kind() == reflect.Slice {
			w = append(w, fmt.Sprintf("%s IN $%d", key, val))
		} else {
			w = append(w, fmt.Sprintf("%s %s $%d", key, qb.wheremod[key], val))
		}
	}

	if qb.limit != 0 { 
		if qb.offset != 0 {return fmt.Sprintf( "SELECT %s FROM %s LIMIT %v OFFSET %v;", strings.Join(select_fields, ", "), table, qb.limit, qb.offset) }
		return fmt.Sprintf("SELECT %s FROM %s LIMIT %v;", strings.Join(select_fields, ", "), table, qb.limit)
	}
	qb.logger.Debug(fmt.Sprintf("Limit: %v", qb.limit))
	qb.logger.Debug(fmt.Sprintf("Offset: %v", qb.offset))
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s;", strings.Join(select_fields, ", "), table, strings.Join(w, " AND "))
} 


func (qb *QueryBuilder) BuildCount(table string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM %s;", table)
}


// BuildUpdate builds a parameterized UPDATE query for a struct-typed model.
// WHERE clauses are extracted from URL query parameters matching the model's primary key fields.
// Returns an empty string if no primary key values are found in the URL.
func (qb *QueryBuilder) BuildUpdate(table string, r *http.Request, model interface{}) string {
	if qb.query != "" {
		return qb.query
	}

	whereFields := r.URL.Query()
	prim_keys := GetPrimaryKeys(model)
	whereExists := false

	for _, key := range prim_keys {
		f, _ := reflect.TypeOf(model).FieldByName(key)
		whereval := whereFields.Get(f.Tag.Get("db"))
		if whereval != "" {
			qb.innerSetWhere(f.Tag.Get("db"), whereval, "=")
			whereExists = true
		}
	}

	if !whereExists {
		return ""
	}

	w := make([]string, 0)
	v := make([]string, 0)

	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}
	for key, val := range qb.values {
		v = append(v, fmt.Sprintf("%s = $%d", key, val))
	}

	qb.query = fmt.Sprintf("UPDATE %s SET %s WHERE %s;", table, strings.Join(v, ", "), strings.Join(w, " AND "))
	return qb.query
}

// BuildUpdate_Dynamic builds a parameterized UPDATE query from the where and value clauses
// already set on the query builder.
func (qb *QueryBuilder) BuildUpdate_Dynamic(cfg *models.DataModel) string {
	if qb.query != "" {
		qb.logger.Warn("Called build update after query had already been built!")
		return qb.query
	}

	// Construct the where and value clauses, where must be an exact match
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

func (qb *QueryBuilder) HasUpdates() bool {
	return len(qb.values) > 0
}


// Build a parameterized DELETE query using the where clauses already set on the query builder.
func (qb *QueryBuilder) BuildDelete_Dynamic(cfg *models.DataModel) string {
	if qb.query != "" {
		qb.logger.Warn("Called build delete after query had already been built!")
		return qb.query
	}

	w := make([]string, 0)
	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}

	qb.query = fmt.Sprintf("DELETE FROM %s WHERE %s;", *cfg.Table_name, strings.Join(w, " AND "))
	return qb.query
}
