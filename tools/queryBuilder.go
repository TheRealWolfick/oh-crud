package tools

import (
	"fmt"
	"reflect"
	"strings"
)

type QueryBuilder struct {
	values map[string]uint
	where  map[string]uint
	args   []any
	pos    uint
}

type SetCallback interface {
	Set(field string, value any)
}

func NewQueryBuilder(pk string, val any) *QueryBuilder {
	return &QueryBuilder{
		values: make(map[string]uint),
		where: map[string]uint{pk: 1},
		args: []any{val},
		pos: 2,
	}
}

func (qb *QueryBuilder) HasUpdates() bool {
	if len(qb.values) > 0 {
		return true
	}
	return false
}

func (qb *QueryBuilder) GetArgs() []any {
	return qb.args
}

func (qb *QueryBuilder) Set(field string, value any) {
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

func (qb *QueryBuilder) BuildInsert(table string, mod any) string {
	// Reset the QueryBuilder to a blank state
	qb = nil
	qb = &QueryBuilder{
		values: make(map[string]uint),
		where: map[string]uint{},
		args: []any{},
		pos: 1,
	}
	c := make([]string, 0)
	v := make([]string, 0)

	t := reflect.TypeOf(mod)
	vals := reflect.ValueOf(mod)

	if t.Kind() != reflect.Struct {
		return ""
	}

	for i := 0; i < t.NumField(); i++ {
		if vals.Field(i).Elem().IsValid() {
			c = append(c, t.Field(i).Tag.Get("db"))
			v = append(v, fmt.Sprintf("$%d", qb.pos))
			qb.pos++
			qb.args = append(qb.args, vals.Field(i).Elem())
		} else {
			empty_value, exists := t.Field(i).Tag.Lookup("none")
			if exists {
				v = append(v, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
		  	qb.args = append(qb.args, empty_value)
			}
		}
	} 

	fmt.Println(qb.args...)
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", table, strings.Join(c, ", "), fmt.Sprintf("(%s)",strings.Join(v, ", ")))
}


func (qb *QueryBuilder) BuildMultiInsert(table string, models []any) string {
	// Reset the QueryBuilder to a blank state
	qb = nil
	qb = &QueryBuilder{
		values: make(map[string]uint),
		where: map[string]uint{},
		args: []any{},
		pos: 1,
	}
	c := make([]string, 0)
	v := make([]string, 0)

	// Iterate through each model
	for model_pos, model := range models {
		
		local_v := make([]string, 0)
		t := reflect.TypeOf(model)
		vals := reflect.ValueOf(model)

		if t.Kind() != reflect.Struct {
			continue
		}

		// Iterate through each field of the model
		for i := 0; i < t.NumField(); i++ {

			// Check if it is a valid value (exists)
			if vals.Field(i).Elem().IsValid() {
				if model_pos == 0 {
					c = append(c, t.Field(i).Tag.Get("db"))
				}
				local_v = append(local_v, fmt.Sprintf("$%d", qb.pos))
				qb.pos++
				qb.args = append(qb.args, vals.Field(i).Elem())
			} else {
				// Read model's "none" default
				empty_value, exists := t.Field(i).Tag.Lookup("none")
				if exists {
					local_v = append(local_v, fmt.Sprintf("$%d", qb.pos))
					qb.pos++
			  	qb.args = append(qb.args, empty_value)
				}
			}
		} 

		v = append(v, fmt.Sprintf(("(%s)"), strings.Join(local_v, ", ")))
	}
	fmt.Println(qb.args...)

	return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s;", table, strings.Join(c, ", "), fmt.Sprintf("%s",strings.Join(v, ", ")))
}


func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	w := make([]string, 0)

	for key, val := range qb.where {
		if reflect.TypeOf(qb.args[val-1]).Kind() == reflect.Slice {
			w = append(w, fmt.Sprintf("%s IN $%d", key, val))
		} else {
			w = append(w, fmt.Sprintf("%s = $%d", key, val))
		}
	}

	return fmt.Sprintf("SELECT %s FROM %s WHERE %s;", strings.Join(select_fields, ", "), table, strings.Join(w, ", "))
}

func (qb *QueryBuilder) BuildUpdate(table string) string {
	w := make([]string, 0)
	v := make([]string, 0)

	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
	}

	for key, val := range qb.values {
		v = append(v, fmt.Sprintf("%s = $%d", key, val))
	}

	return fmt.Sprintf("UPDATE %s SET %s WHERE %s;", table, strings.Join(v, ", "), strings.Join(w, ", "))
}
