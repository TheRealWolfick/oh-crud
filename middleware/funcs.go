package middleware

func CompareFirst[T comparable](callback func(string, any), field string, old *T, new *T) {
	if new == nil {
		return
	}
	if old == nil || *old != *new {
		callback(field, *new)
	}
}
