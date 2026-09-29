package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"go-server/models"
)

// BotResultsSecretConfigured reports whether BOT_RESULTS_SECRET is set. When it
// is not, every report is refused.
func BotResultsSecretConfigured() bool {
	return os.Getenv("BOT_RESULTS_SECRET") != ""
}

// botResultsAuthorized checks the bearer token against BOT_RESULTS_SECRET in
// constant time. An unset secret authorizes nothing.
func botResultsAuthorized(r *http.Request) bool {
	secret := os.Getenv("BOT_RESULTS_SECRET")
	if secret == "" {
		return false
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	// Hash first so the comparison does not depend on the token's length.
	got := sha256.Sum256([]byte(token))
	want := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

type botResultRequest struct {
	ResultID string `json:"resultId"`
	BotID    string `json:"botId"`
	Mode     string `json:"mode"`
	Delay    *int   `json:"delay"`
	Outcome  string `json:"outcome"`
}

func PostBotResultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !botResultsAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req botResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.ResultID == "" || len(req.ResultID) > 100 {
		http.Error(w, "Invalid resultId", http.StatusBadRequest)
		return
	}
	if !models.ValidBotID(req.BotID) {
		http.Error(w, "Invalid botId", http.StatusBadRequest)
		return
	}
	mode, ok := parseModeOrReject(w, req.Mode)
	if !ok {
		return
	}
	if req.Delay == nil || *req.Delay < 0 || *req.Delay > 4 {
		http.Error(w, "Invalid delay: must be 0 to 4", http.StatusBadRequest)
		return
	}
	outcome, ok := models.ParseOutcome(req.Outcome)
	if !ok {
		http.Error(w, "Invalid outcome: must be win, loss or draw", http.StatusBadRequest)
		return
	}

	err := models.AddBotResult(models.BotResult{
		ResultID: req.ResultID, BotID: req.BotID, Mode: mode, Delay: *req.Delay, Outcome: outcome,
	})
	if err != nil {
		log.Printf("AddBotResult failed: %v", err)
		http.Error(w, "Failed to record result", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"message": "Result recorded"})
}

func BotRecordsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records, err := models.GetBotRecords(r.URL.Query().Get("botId"))
	if err != nil {
		http.Error(w, "Failed to get bot records", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(records)
}
