package main

import (
	"log"
	"net/http"

	"go-server/handlers"
	"go-server/models"
)

func main() {
	if err := models.InitDB(); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer models.DB.Close()

	http.HandleFunc("/register", logRequest(handlers.RegisterHandler, "/register"))
	http.HandleFunc("/login", logRequest(handlers.LoginHandler, "/login"))
	http.HandleFunc("/anonymous", logRequest(handlers.AnonymousHandler, "/anonymous"))
	http.HandleFunc("/logout", logRequest(handlers.LogoutHandler, "/logout"))
	http.HandleFunc("/verify-token", logRequest(handlers.ValidateHandler, "/verify-token"))

	log.Println("Server running on :8080")
	http.ListenAndServe(":8080", nil)
}

func logRequest(handler http.HandlerFunc, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Endpoint called: %s | Method: %s | RemoteAddr: %s", name, r.Method, r.RemoteAddr)
		handler(w, r)
	}
}
