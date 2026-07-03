package middleware

import (
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// allowedOrigins returns every origin the frontend can be served from.
// It no longer depends on gin.Mode() (GIN_MODE is not set on Vercel, so the
// old code silently ran the debug-only allowlist in production). Extra origins
// can be added via the comma-separated CORS_ORIGINS env var.
func allowedOrigins() []string {
	origins := []string{
		"http://localhost:5173",                    // Vite dev server
		"http://localhost:4173",                    // Vite preview
		"https://account-tracker-psi.vercel.app",   // production web
		"capacitor://localhost",                    // Capacitor iOS
		"http://localhost",                         // Capacitor Android (http scheme)
		"https://localhost",                        // Capacitor Android (https scheme)
	}
	if extra := os.Getenv("CORS_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
	}
	return origins
}

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowOrigins = allowedOrigins()
	config.AllowCredentials = true
	config.AllowMethods = []string{"POST", "OPTIONS", "GET", "PUT", "DELETE"}
	config.AllowHeaders = []string{
		"Content-Type",
		"Content-Length",
		"Accept-Encoding",
		"X-CSRF-Token",
		"Authorization",
		"accept",
		"origin",
		"Cache-Control",
		"X-Requested-With",
	}

	return cors.New(config)
}
