package handler

import (
	"github.com/feilian1999/account-tracker-backend/internal/app"
	"net/http"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	app.GetRouter().ServeHTTP(w, r)
}
