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
		if v == "true" {
			returns = append(returns, true)
			continue
		}
		if v == "false" {
			returns = append(returns, false)
			continue
		}
		if asInt, err := strconv.ParseInt(v, 10, 64); err == nil {
			returns = append(returns, asInt)
			continue
		}
		if asFloat, err := strconv.ParseFloat(v, 64); err == nil {
			returns = append(returns, asFloat)
			continue
		}
	}
	if len(returns) > 1 {
		return returns
	}
	return returns[0]
}

// GetChecksum extracts the "checksum" query parameter from the request URL.
// Returns an empty string if the parameter is absent or the URL cannot be parsed.
func GetChecksum(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	if len(r.URL.Query()) < 1 {
		return ""
	}
	return r.URL.Query().Get("checksum")
}
