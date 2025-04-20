package handlers

import (
	"encoding/json"
	"go-server/models"
	"go-server/utils"
	"net/http"
)

type ScoreRequest struct {
	UserID string `json:"userId"`
	Score  int    `json:"score"`
}

func AddScoreHandler(w http.ResponseWriter, r *http.Request) {

	userID, err := utils.GetUserIDFromToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var req struct {
		Score int `json:"score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Score == 0 {
		http.Error(w, "Invalid score", http.StatusBadRequest)
		return
	}

	if err := models.AddScore(userID, req.Score); err != nil {
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
	scores, err := models.GetTopScores(20)
	if err != nil {
		http.Error(w, "Failed to get high scores", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(scores)
}
