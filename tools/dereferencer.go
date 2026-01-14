package tools

import (
	"encoding/json"
)

func DereferencedString[T any](model T) string {
    data, _ := json.MarshalIndent(model, "", "  ")
    return string(data)
}

