package tools

import (
	"net/http"
	"strconv"
)

func GetIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	
	if ip == "" {
		ip = r.RemoteAddr
	}

	return ip
}

func ConvertURLValToAny(val []string) any {
	returns := []any{}
	for _, v := range val {
		if v == "true" {returns = append(returns, true); continue}
		if v == "false" {returns = append(returns, false); continue}
		if asInt, err := strconv.ParseInt(v, 10, 64); err == nil {returns = append(returns, asInt); continue}
		if asFloat, err := strconv.ParseFloat(v, 64); err == nil {returns = append(returns, asFloat); continue}
	}
	if len(returns) > 1 {return returns}
	return returns[0]
}

func IsInt(val string) bool {
	_, err := strconv.Atoi(val)
	if err != nil { return false } 
	return true
}

// To ONLY be used when 100% confident it is a string. i.e after IsInt
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
