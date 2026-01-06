package tools

import "net/http"

func GetIP(r *http.Request) string {
	ip := r.RemoteAddr
	
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
	}

	return ip
}

