package tools

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"lotusforge.au/api-server/models"
)

func IsInt(val string) bool {
	_, err := strconv.Atoi(val)
	if err != nil {
		return false
	}
	return true
}

// ConvertToInt should ONLY be used when 100% confident the value is an integer (e.g. after IsInt).
func ConvertToInt(val string) int {
	i, _ := strconv.Atoi(val)
	return i
}

func ASCorDESC(s string) string {
	switch s {
	case "":
		return "ASC"
	case "desc", "DESC", "d", "D", "descending", "DESCENDING":
		return "DESC"
	}
	return "ASC"
}

func generateRandomString(string_length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

	return_str := make([]byte, string_length)
	_, err := rand.Read(return_str)

	if err != nil {
		return "", err
	}

	for i := range string_length {
		return_str[i] = chars[int(return_str[i])%len(chars)]
	}

	return string(return_str), nil
}

func Generate32CharString() (string, error) {
	return generateRandomString(32)
}

func ParseAggregateFuncString(field string, qb *QueryBuilder, cfg *models.DataModel) (string, bool) {
	if field == "count" {
		return "count(*)", true
	}

	// Skip if not a valid field
	if !strings.Contains(field, ":") { return "", false }

	// Process functions
	s := strings.Split(field, ":"); if len(s) != 2 { return "", false }
	fnc, sub_field := s[0], s[1]

	switch fnc {
	case "avg", "min", "max", "sum":
		f, allowed := CheckFieldGetValid(sub_field, cfg)
		if allowed { 
			return  fmt.Sprintf("%s(%s)", fnc, f), true
		}
	default:
		qb.logger.Debug("Invalid function passed into aggregate function", "func", fnc)
	}
	return "", false
}
