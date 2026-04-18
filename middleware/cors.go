package middleware

import (
	"net/http"
	"slices"
	"strings"

	"lotusforge.au/api-server/models"
)


func Cors(cfg *models.DataModel, server_conf *models.SwappableServerConfig) func(http.Handler) http.Handler {
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
			if cfg.End_points_allowed != nil {
				if cfg.End_points_allowed.GET != nil && *cfg.End_points_allowed.GET           { allowed_methods = append(allowed_methods, "GET") }
				if cfg.End_points_allowed.POST != nil && *cfg.End_points_allowed.POST         { allowed_methods = append(allowed_methods, "POST") }
				if cfg.End_points_allowed.PUT != nil && *cfg.End_points_allowed.PUT           { allowed_methods = append(allowed_methods, "PUT") }
				if cfg.End_points_allowed.DELETE != nil && *cfg.End_points_allowed.DELETE     { allowed_methods = append(allowed_methods, "DELETE") }
				if cfg.End_points_allowed.POST_GROUP != nil && *cfg.End_points_allowed.POST_GROUP     { allowed_methods = append(allowed_methods, "POST-GROUP") }
				if cfg.End_points_allowed.PUT_GROUP != nil && *cfg.End_points_allowed.PUT_GROUP       { allowed_methods = append(allowed_methods, "PUT-GROUP") }
				if cfg.End_points_allowed.DELETE_GROUP != nil && *cfg.End_points_allowed.DELETE_GROUP { allowed_methods = append(allowed_methods, "DELETE-GROUP") }
			}
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowed_methods, ", "))
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(conf.CORS.Allowed_Headers, ", "))

			// Set allowed credentials
			if conf.CORS.Allow_Credentials != nil && *conf.CORS.Allow_Credentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			} else {
				w.Header().Set("Access-Control-Allow-Credentials", "false")
			}

			// Call the next function
			next.ServeHTTP(w, r)
		})
	}
}
