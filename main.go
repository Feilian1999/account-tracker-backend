package main

import (
	"log"
	"os"

	"github.com/feilian1999/account-tracker-backend/internal/app"
)

// The only entry point, locally and on Vercel: the Go framework preset
// (vercel.json) runs this server and hands it every request at its own path.
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server listening on :%s\n", port)
	log.Fatal(app.GetRouter().Run(":" + port))
}
