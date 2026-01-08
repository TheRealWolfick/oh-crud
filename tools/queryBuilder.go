package tools

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

type QueryBuilder struct {
	values map[string]uint
	where  map[string]uint
	args   []any
	pos    uint
	wheremod map[string]string
	query  string
}


type SetCallback interface {
	Set(field string, value any)
}


// Create a new blank query builder without a primary key where value.
// Primarily used when there won't be a WHERE clause in the SQL (INSERT)
func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{
		values: make(map[string]uint),
		where: make(map[string]uint),
		args: []any{},
		pos: 1,
		wheremod: make(map[string]string),
		query: "",
	}
}


// Receive the args from the query builder
func (qb *QueryBuilder) GetArgs() []any {
	return qb.args
}


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


// Save a field and value into the query builder. Intended to use with
// updating fields (set only the relevant fields)
func (qb *QueryBuilder) SetValue(field string, value any) {
	if value == nil {
		return
	}

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

	_, exists := qb.where[field]

	if !exists {
		qb.where[field] = qb.pos
		qb.wheremod[field] = mod
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.args[qb.values[field]-1] = value
		qb.wheremod[field] = mod
	}
}



// Wrapper for directly interfacing with the innerSetWhere function
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
		qb.args[qb.values[field]-1] = value
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
			case reflect.Int, reflect.Int32, reflect.Int64:
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

			default:
				setFunc(field, value_as_string, "~*")
			}
		}
	}
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
	if reflect.TypeOf(mod).Kind() != reflect.Struct {
		return ""
	}

	//
	db_columns := make([]string, 0)
	value := make([]string, 0)

	t := reflect.TypeOf(mod)
	vals := reflect.ValueOf(mod)

	if t.Kind() != reflect.Struct {
		return ""
	}

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


// Build a query to insert multiple entries. The slice of models must have all the keys as pointers and cannot omit fields. Instead,
// the json tag none:"<string>" should be used to specify a default value (DEFAULT and NULL accepted as strings).
// Default values are not inserted as arguements, but inserted into the query directly.
func (qb *QueryBuilder) BuildMultiInsert(table string, models []any) string {

	// Early return
	if qb.query != "" {
		return qb.query
	}

	// Initiate columns and values
	c := make([]string, 0)
	v := make([]string, 0)

	// Iterate through each model
	for model_pos, model := range models {

		local_values := make([]string, 0)
		typ := reflect.TypeOf(model)
		vals := reflect.ValueOf(model)

		if typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}

		// Iterate through each field of the model
		for i := 0; i < typ.NumField(); i++ {
			database_column_name := typ.Field(i).Tag.Get("db")
			if database_column_name == "" || database_column_name == "-" {
				continue
			}

			if model_pos == 0 {
				c = append(c, database_column_name)
			}

			field_val := vals.Field(i)
			if field_val.Kind() == reflect.Ptr {
				field_val = field_val.Elem()
			}

			// Check if it is a valid value (exists)
			if field_val.IsValid() {
				local_values = append(local_values, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
				qb.args = append(qb.args, vals.Field(i).Interface())
			} else {
				// Read model's "none" default
				empty_value, exists := typ.Field(i).Tag.Lookup("none")
				if exists {
					if empty_value == "" {
						local_values = append(local_values, "''")
					} else {
						local_values = append(local_values, empty_value)
					}
				}
			}
		} 

		v = append(v, fmt.Sprintf(("(%s)"), strings.Join(local_values, ", ")))
	}

	qb.query = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", table, strings.Join(c, ", "), fmt.Sprintf("%s",strings.Join(v, ", ")))
	return qb.query
}


// Build the query to select from the database.
// Must supply the table name to be selected from and what fields are required. The fields must be in a slice, even if it is only one value.
func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	if len(qb.where) < 1 {
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

	return fmt.Sprintf("SELECT %s FROM %s WHERE %s;", strings.Join(select_fields, ", "), table, strings.Join(w, " AND "))
} 


// Build the query to update a single field in the database
func (qb *QueryBuilder) BuildUpdate(table string, r *http.Request, model interface{}) string {
	if qb.query != "" {
		return qb.query
	}

	// Read the url and get the database columns that where is allowed on
	whereFields := r.URL.Query()
	prim_keys := GetPrimaryKeys(model)

	// Search for any of the main keys from the URL query for the where clause. Soft fail on no keys
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

	// Construct the where and value clauses, where must be an exact match
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


// Build the query to delete a resource from the database
func (qb *QueryBuilder) BuildDelete(table string, model interface{}) string {
	if qb.query != "" {
		return qb.query
	}

	// Load the where values from the struct
	SetWhereFromStruct(qb, model)

	// Build the where values for the query
	w := make([]string, 0)

	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s %s $%d", key, qb.wheremod[key], val))
	}

	qb.query = fmt.Sprintf("DELETE FROM %s WHERE %s;", table, strings.Join(w, " AND "))
	return qb.query
}

func (qb *QueryBuilder) HasUpdates() bool {
	return len(qb.values) > 0
}
