package handler

import (
	"net/http"
	"github.com/feilian1999/account-tracker-backend/internal/app"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	app.GetRouter().ServeHTTP(w, r)
}
