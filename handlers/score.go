package handlers

import (
	"encoding/json"
	"go-server/models"
	"go-server/utils"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

var anonymousScoreLimiter = utils.NewRateLimiter(10, 1*time.Minute)
var authenticatedScoreLimiter = utils.NewRateLimiter(10, 1*time.Minute)
var addScoreLimiter = utils.NewRateLimiter(10, 1*time.Minute)

// rejectIfRateLimited writes the shared 429 response and returns true when the
// request's client IP has exceeded the given limiter.
func rejectIfRateLimited(w http.ResponseWriter, r *http.Request, limiter *utils.RateLimiter) bool {
	if limiter.Allow(getClientIP(r)) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"Rate limit exceeded. Maximum 10 submissions per minute."}`, http.StatusTooManyRequests)
	return true
}

type ScoreRequest struct {
	UserID string `json:"userId"`
	Score  int    `json:"score"`
}

type AuthenticatedScoreRequest struct {
	Token    string `json:"token"`
	ClientID string `json:"clientId"`
	Score    int    `json:"score"`
	Mode     string `json:"mode"`
}

type AnonymousScoreRequest struct {
	ClientID string `json:"clientId"`
	Score    int    `json:"score"`
	Mode     string `json:"mode"`
}

// parseModeOrReject writes a 400 and returns false when s is not a known game
// mode. Every endpoint that saves or ranks scores requires one.
func parseModeOrReject(w http.ResponseWriter, s string) (models.Mode, bool) {
	mode, ok := models.ParseMode(s)
	if !ok {
		http.Error(w, "Invalid mode: must be timed or endless", http.StatusBadRequest)
	}
	return mode, ok
}

func AddScoreHandler(w http.ResponseWriter, r *http.Request) {
	if rejectIfRateLimited(w, r, addScoreLimiter) {
		return
	}

	userID, err := utils.GetUserIDFromToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var req struct {
		Score int    `json:"score"`
		Mode  string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Score == 0 {
		http.Error(w, "Invalid score", http.StatusBadRequest)
		return
	}
	mode, ok := parseModeOrReject(w, req.Mode)
	if !ok {
		return
	}

	if err := models.AddScore(userID, mode, req.Score); err != nil {
		http.Error(w, "Failed to add score", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Score added"})
}

func GetUserScoresHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		http.Error(w, "Missing userId", http.StatusBadRequest)
		return
	}

	scores, err := models.GetScoresForUser(userID)
	if err != nil {
		http.Error(w, "Could not fetch scores", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(scores)
}

func HighScoresHandler(w http.ResponseWriter, r *http.Request) {
	mode, ok := parseModeOrReject(w, r.URL.Query().Get("mode"))
	if !ok {
		return
	}
	scores, err := models.GetTopScores(mode, 20)
	if err != nil {
		http.Error(w, "Failed to get high scores", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(scores)
}

func LeaderboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	mode, ok := parseModeOrReject(w, r.URL.Query().Get("mode"))
	if !ok {
		return
	}
	scores, err := models.GetLeaderboard(mode, 100)
	if err != nil {
		http.Error(w, "Failed to get leaderboard", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(scores)
}

func ScoresHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if r.Method == http.MethodPost {
		if strings.Contains(path, "/anonymous") {
			PostAnonymousScoreHandler(w, r)
		} else {
			PostAuthenticatedScoreHandler(w, r)
		}
		return
	}

	if r.Method == http.MethodGet {
		if strings.Contains(path, "/anonymous/") {
			parts := strings.Split(path, "/")
			for i, part := range parts {
				if part == "anonymous" && i+1 < len(parts) {
					clientID := parts[i+1]
					GetAnonymousScoresHandler(w, r, clientID)
					return
				}
			}
		}
		parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
		if len(parts) > 1 && parts[len(parts)-1] != "" {
			userID := parts[len(parts)-1]
			GetUserScoresWithPathHandler(w, r, userID)
			return
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func PostAuthenticatedScoreHandler(w http.ResponseWriter, r *http.Request) {
	if rejectIfRateLimited(w, r, authenticatedScoreLimiter) {
		return
	}
	var req AuthenticatedScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Score == 0 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	userID, err := utils.GetUserIDFromToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	mode, ok := parseModeOrReject(w, req.Mode)
	if !ok {
		return
	}

	if err := models.AddScoreWithClientID(userID, req.ClientID, mode, req.Score); err != nil {
		http.Error(w, "Failed to add score", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Score added"})
}

func PostAnonymousScoreHandler(w http.ResponseWriter, r *http.Request) {
	// Rate limit: 10 requests per minute per IP
	if rejectIfRateLimited(w, r, anonymousScoreLimiter) {
		return
	}

	var req AnonymousScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Score == 0 || req.ClientID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	mode, ok := parseModeOrReject(w, req.Mode)
	if !ok {
		return
	}

	if err := models.AddAnonymousScore(req.ClientID, mode, req.Score); err != nil {
		log.Printf("AddAnonymousScore failed: %v", err)
		http.Error(w, "Failed to add score", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Anonymous score added"})
}

// getClientIP extracts the client IP from the request, accounting for proxies
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxied requests)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		ips := strings.Split(forwarded, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func GetUserScoresWithPathHandler(w http.ResponseWriter, r *http.Request, userID string) {
	scores, err := models.GetScoresForUser(userID)
	if err != nil {
		http.Error(w, "Could not fetch scores", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scores)
}

func GetAnonymousScoresHandler(w http.ResponseWriter, r *http.Request, clientID string) {
	scores, err := models.GetAnonymousScoresForClient(clientID)
	if err != nil {
		http.Error(w, "Could not fetch anonymous scores", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scores)
}

type MigrateScoresRequest struct {
	UserID string `json:"userId"`
}

func MigrateScoresHandler(w http.ResponseWriter, r *http.Request, clientID string) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MigrateScoresRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	userID, err := utils.GetUserIDFromToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	if userID != req.UserID {
		http.Error(w, "User ID mismatch", http.StatusForbidden)
		return
	}

	count, err := models.MigrateScores(clientID, userID)
	if err != nil {
		http.Error(w, "Failed to migrate scores", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Scores migrated successfully",
		"count":   count,
	})
}
