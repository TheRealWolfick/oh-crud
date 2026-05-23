package tools

import "slices"

func FilterValidStructValues[T comparable](s []T, valid []T) []T {
  v := []T{}

	for _, val := range s {
		if slices.Contains(valid, val) {v = append(v, val)}
	}

	return v
}
