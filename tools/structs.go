package tools

func StructIsEmpty[T any](s *T, empty *T) bool {
	if s == empty {
		return true
	}
	return false
}
