package main

import (
	"log"
	"os"

	"github.com/feilian1999/account-tracker-backend/internal/app"
)

func main() {
	r := app.GetRouter()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Local server starting on http://localhost:%s\n", port)
	r.Run(":" + port)
}
