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
