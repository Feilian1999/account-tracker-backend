package main

import (
	"log"
	"net/http"
	"os"

	"github.com/feilian1999/account-tracker-backend/internal/app"
)

func main() {
	app.GetRouter() // init DB + routes before taking traffic

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Local server starting on http://localhost:%s\n", port)
	// app.ServeHTTP, not router.Run: Vercel can run this file as a Go server
	// behind the vercel.json rewrite, and the original path must be restored.
	log.Fatal(http.ListenAndServe(":"+port, http.HandlerFunc(app.ServeHTTP)))
}
