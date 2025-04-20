package handlers

import (
	"encoding/json"
	"go-server/models"
	"go-server/utils"
	"net/http"
	"strings"
)

type ScoreRequest struct {
	UserID string `json:"userId"`
	Score  int    `json:"score"`
}

func AddScoreHandler(w http.ResponseWriter, r *http.Request) {

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := utils.ValidateToken(tokenString)
	if err != nil {
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	userID, ok := claims["userId"].(string)
	if !ok || userID == "" {
		http.Error(w, "Invalid token claims", http.StatusUnauthorized)
		return
	}

	var req struct {
		Score int `json:"score"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.Score == 0 {
		http.Error(w, "Invalid score", http.StatusBadRequest)
		return
	}

	err = models.AddScore(userID, req.Score)
	if err != nil {
		http.Error(w, "Could not add score", http.StatusInternalServerError)
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
