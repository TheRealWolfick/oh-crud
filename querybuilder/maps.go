package querybuilder

func SetValueFromMap(qb *QueryBuilder, m map[string]any) {
	setFromMap(m, qb.SetValue)
}

// SetValueAndWhereFromMap sets fields into the query builder.
// Fields whose names appear in where_fields go to SetWhereAbsolute; all others go to SetValue.
func SetValueAndWhereFromMap(qb *QueryBuilder, m map[string]any, where_fields []string) {
	where_set := map[string]bool{}
	for _, f := range where_fields {
		where_set[f] = true
	}
	for k, v := range m {
		if where_set[k] {
			qb.SetWhereAbsolute(k, v)
		} else {
			qb.SetValue(k, v)
		}
	}
}

// setFromMap is an internal helper that calls setFunc for every key-value pair in m.
func setFromMap(m map[string]any, setFunc func(string, any)) {
	for k, v := range m {
		setFunc(k, v)
	}
}
