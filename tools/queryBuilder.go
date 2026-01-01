package tools

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

type QueryBuilder struct {
	values map[string]uint
	where  map[string]uint
	args   []any
	pos    uint
}

type BlankQueryBuilder struct {
	QueryBuilder
	query  string
}

type QueryBuildTool interface {
	GetArgs() []any
	GetArgsAsString() string
	SetValue(field string, value any)
	SetWhere(field string, value any)
}

type SetCallback interface {
	Set(field string, value any)
}


// Create new new QueryBuilder and insert the primary key to be
// used in any update statement
func NewQueryBuilder(pk string, val any) *QueryBuilder {
	return &QueryBuilder{
		values: make(map[string]uint),
		where: map[string]uint{pk: 1},
		args: []any{val},
		pos: 2,
	}
}


// Create a new blank query builder without a primary key where value.
// Primarily used when there won't be a WHERE clause in the SQL (INSERT)
func NewBlankQueryBuilder() *BlankQueryBuilder {
	return &BlankQueryBuilder{
		QueryBuilder: QueryBuilder{
			values: make(map[string]uint),
			where: make(map[string]uint),
			args: []any{},
			pos: 1,
		},
		query: "",
	}
}


// Return whether there are fields to be updated in QueryBuilder.
// To be used to only push updates to the database where necessary.
func (qb *QueryBuilder) HasUpdates() bool {
	if len(qb.values) > 0 {
		return true
	}
	return false
}


// Receive the args from the query builder
func (qb *QueryBuilder) GetArgs() []any {
	return qb.args
}


// Receive the args from the query builder
func (qb *BlankQueryBuilder) GetArgs() []any {
	return qb.args
}


// Return all the args as a string (debugging)
func (qb *QueryBuilder) GetArgsAsString() string {
	args_string := []string{}
	for _, v := range qb.args {
		args_string = append(args_string, fmt.Sprintf("%v", v))
	}
	return strings.Join(args_string, ", ")
}


// Return all the args as a string (debugging)
func (qb *BlankQueryBuilder) GetArgsAsString() string {
	args_string := []string{}
	for _, v := range qb.args {
		args_string = append(args_string, fmt.Sprintf("%v", v))
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
func (qb *BlankQueryBuilder) SetValue(field string, value any) {
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


// Add a field and value into the where clause
func (qb *QueryBuilder) SetWhere(field string, value any) {
	if value == nil {
		return
	}

	_, exists := qb.where[field]

	if !exists {
		qb.where[field] = qb.pos
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.args[qb.values[field]-1] = value
	}
}
func (qb *BlankQueryBuilder) SetWhere(field string, value any) {
	if value == nil {
		return
	}

	_, exists := qb.where[field]

	if !exists {
		qb.where[field] = qb.pos
		qb.args = append(qb.args, value)
		qb.pos++
	} else {
		qb.args[qb.values[field]-1] = value
	}
}


// Build the insert query, saving all the values into the args key to be 
// safely loaded into the sql.
func (qb *BlankQueryBuilder) BuildInsert(table string, mod any) string {
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


// Build a query to insert multiple entries. The slice of models must have all the keys as pointers and cannot use omitempty. Instead,
// the json tag none:"<string>" should be used to specify a default value (DEFAULT and NULL accepted as strings).
// Default values are not inserted as arguements, but inserted into the query directly.
func (qb *BlankQueryBuilder) BuildMultiInsert(table string, models []any) string {

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
	fmt.Print(qb.query)
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
			w = append(w, fmt.Sprintf("%s ~* $%d", key, val))
		}
	}

	return fmt.Sprintf("SELECT %s FROM %s WHERE %s;", strings.Join(select_fields, ", "), table, strings.Join(w, ", "))
} 

func (qb *BlankQueryBuilder) BuildUpdate(table string, r *http.Request, model interface{}) string {
	if qb.query != "" {
		return qb.query
	}

	// Read the url annd get the database columns that where is allowed on
	whereFields := r.URL.Query()
	prim_keys := GetPrimaryKeys(model)

	// Search for any of the main keys from the URL query for the where clause. Soft fail on no keys
	whereExists := false
	for _, key := range prim_keys {
		f, _ := reflect.TypeOf(model).FieldByName(key)
		whereval := whereFields.Get(f.Tag.Get("db"))

		if whereval != "" {
			qb.SetWhere(f.Tag.Get("db"), whereval)
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


func (qb *BlankQueryBuilder) BuildDelete(table string, model interface{}) string {
	if qb.query != "" {
		return qb.query
	}

	// Load the where values from the struct
	SetWhereFromStruct(qb, model)

	// Build the where values for the query
	w := make([]string, 0)

	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}

	qb.query = fmt.Sprintf("DELETE FROM %s WHERE %s;", table, strings.Join(w, " AND "))
	return qb.query
}
