package tools

import (
	"fmt"
	"strings"
)

type QueryBuilder struct {
	values map[string]uint
	where  map[string]uint
	args   []any
	pos    uint
}

func NewQueryBuilder(pk string, val any) *QueryBuilder {
	return &QueryBuilder{
		values: make(map[string]uint),
		where: map[string]uint{pk: 1},
		args: []any{val},
		pos: 2,
	}
}

func (qb *QueryBuilder) Set(field string, value any) {
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

func (qb *QueryBuilder) BuildSelect(table string, select_fields []string) string {
	w := make([]string, 0)

	for key, val := range qb.where {
		w = append(w, fmt.Sprintf("%s = $%d", key, val))
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
