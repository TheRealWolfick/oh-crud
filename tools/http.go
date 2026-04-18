package tools

import (
	"net/http"
)

func GetIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
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
