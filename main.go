package main

import (
	"log"
	"net/http"
	"strings"

	"go-server/handlers"
	"go-server/models"
)

var version = "dev"

func main() {
	log.Printf("go-server version %s", version)

	if err := models.InitDB(); err != nil {
		log.Fatalf("Failed to init DB: %v", err)
	}
	defer models.DB.Close()

	http.HandleFunc("/register", logRequest(handlers.RegisterHandler, "/register"))
	http.HandleFunc("/login", logRequest(handlers.LoginHandler, "/login"))
	http.HandleFunc("/anonymous", logRequest(handlers.AnonymousHandler, "/anonymous"))
	http.HandleFunc("/logout", logRequest(handlers.LogoutHandler, "/logout"))
	http.HandleFunc("/verify-token", logRequest(handlers.ValidateHandler, "/verify-token"))
	http.HandleFunc("/add-score", logRequest(handlers.AddScoreHandler, "/add-score"))
	http.HandleFunc("/user-scores", logRequest(handlers.GetUserScoresHandler, "/user-scores"))
	http.HandleFunc("/high-scores", logRequest(handlers.HighScoresHandler, "/high-scores"))
	http.HandleFunc("/leaderboard", logRequest(handlers.LeaderboardHandler, "/leaderboard"))
	http.HandleFunc("/appearance", logRequest(handlers.AppearanceHandler, "/appearance"))
	http.HandleFunc("/scores/migrate/", logRequest(migrateScoresRouter, "/scores/migrate"))
	http.HandleFunc("/scores/", logRequest(handlers.ScoresHandler, "/scores"))
	http.HandleFunc("/scores", logRequest(handlers.ScoresHandler, "/scores"))

	log.Println("Server running on :8080")
	http.ListenAndServe(":8080", nil)
}

func migrateScoresRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) < 4 || parts[3] == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	clientID := parts[3]
	handlers.MigrateScoresHandler(w, r, clientID)
}

func logRequest(handler http.HandlerFunc, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Endpoint called: %s | Method: %s | RemoteAddr: %s", name, r.Method, r.RemoteAddr)
		handler(w, r)
	}
}
