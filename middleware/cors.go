package middleware

import (
	"net/http"
	"slices"
	"strings"

	"lotusforge.au/api-server/models"
)

func CorsAdmin(cfg *models.DataModel, server_conf *models.SwappableServerConfig) func(http.Handler) http.Handler {
	return cors(cfg, server_conf, true)
}

func CorsFunction(server_conf *models.SwappableServerConfig) func(http.Handler) http.Handler {
	return corsServerOnly(server_conf)
}

func Cors(cfg *models.DataModel, server_conf *models.SwappableServerConfig) func(http.Handler) http.Handler {
	return cors(cfg, server_conf, false)
}

func cors(cfg *models.DataModel, server_conf *models.SwappableServerConfig, is_admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract info
			origin := r.Header.Get("Origin")
			conf := server_conf.Get()
			
			// Set the allowed origin
			if slices.Contains(conf.CORS.Allowed_Origins, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else { w.Header().Set("Vary", "Origin") }

			// Set the allowed methods and headers
			var allowed_methods []string
			if is_admin {
				allowed_methods = append(allowed_methods, "GET", "POST")
			} else {
				// The group and diff permissions map onto real HTTP methods
				add := func(m string) { if !slices.Contains(allowed_methods, m) { allowed_methods = append(allowed_methods, m) } }
				if cfg.End_points_allowed != nil {
					if cfg.End_points_allowed.GET != nil            { add("GET") }
					if cfg.End_points_allowed.POST != nil           { add("POST") }
					if cfg.End_points_allowed.PUT != nil            { add("PUT") }
					if cfg.End_points_allowed.DELETE != nil         { add("DELETE") }
					if cfg.End_points_allowed.POST_GROUP != nil     { add("POST") }
					if cfg.End_points_allowed.PUT_GROUP != nil      { add("PUT") }
					if cfg.End_points_allowed.DELETE_GROUP != nil   { add("DELETE") }
					if cfg.End_points_allowed.DIFF != nil           { add("GET") }
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowed_methods, ", "))
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(conf.CORS.Allowed_Headers, ", "))
			setAllowCredentials(w, conf)

			// Call the next function
			next.ServeHTTP(w, r)
		})
	}
}


func corsServerOnly(server_conf *models.SwappableServerConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract info
			origin := r.Header.Get("Origin")
			conf := server_conf.Get()

			// Set the allowed origin
			if slices.Contains(conf.CORS.Allowed_Origins, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else { w.Header().Set("Vary", "Origin") }

			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(conf.CORS.Allowed_Headers, ", "))
			setAllowCredentials(w, conf)

			next.ServeHTTP(w, r)
		})
	}
}

// Set allowed credentials
func setAllowCredentials(w http.ResponseWriter, conf *models.ServerConfig) {
	if conf.CORS.Allow_Credentials != nil && *conf.CORS.Allow_Credentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	} else {
		w.Header().Set("Access-Control-Allow-Credentials", "false")
	}
}
