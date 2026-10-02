package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"go-server/models"
)

type rankedSide struct {
	AccountID string   `json:"accountId"`
	StandInID string   `json:"standInId"`
	Rating    *float64 `json:"rating"`
}

type rankedResultRequest struct {
	ResultID string     `json:"resultId"`
	A        rankedSide `json:"a"`
	B        rankedSide `json:"b"`
	// Outcome is "a" or "b" for the winning side, or "draw".
	Outcome string `json:"outcome"`
	// Forfeit means the losing side forfeited. It cannot be a draw.
	Forfeit bool `json:"forfeit"`
}

func (s rankedSide) toModel() (models.Side, bool) {
	if (s.AccountID == "") == (s.StandInID == "") {
		return models.Side{}, false
	}
	side := models.Side{AccountID: s.AccountID, StandInID: s.StandInID}
	if s.StandInID != "" {
		if s.Rating == nil || *s.Rating < 0 || *s.Rating > 4000 {
			return models.Side{}, false
		}
		side.Rating = *s.Rating
	}
	return side, true
}

func PostRankedResultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !botResultsAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req rankedResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.ResultID == "" || len(req.ResultID) > 100 {
		http.Error(w, "Invalid resultId", http.StatusBadRequest)
		return
	}
	a, okA := req.A.toModel()
	b, okB := req.B.toModel()
	if !okA || !okB {
		http.Error(w, "Invalid sides: each needs an accountId, or a standInId with a rating", http.StatusBadRequest)
		return
	}
	switch req.Outcome {
	case "a", "b", "draw":
	default:
		http.Error(w, "Invalid outcome: must be a, b or draw", http.StatusBadRequest)
		return
	}
	if req.Forfeit && req.Outcome == "draw" {
		http.Error(w, "A forfeit cannot be a draw", http.StatusBadRequest)
		return
	}

	resp, err := models.RecordRankedResult(models.RankedResult{
		ResultID: req.ResultID, A: a, B: b, Outcome: models.RankedOutcome(req.Outcome), Forfeit: req.Forfeit,
	})
	switch {
	case errors.Is(err, models.ErrBadSides):
		http.Error(w, "Invalid sides", http.StatusBadRequest)
		return
	case errors.Is(err, models.ErrUnknownAccount):
		http.Error(w, "Unknown account", http.StatusNotFound)
		return
	case err != nil:
		log.Printf("RecordRankedResult failed: %v", err)
		http.Error(w, "Failed to record result", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

// RatingHandler returns one Account's Rating and Ranked match count.
func RatingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("userId")
	if id == "" {
		http.Error(w, "Missing userId", http.StatusBadRequest)
		return
	}
	rating, err := models.GetRating(id)
	if errors.Is(err, models.ErrUnknownAccount) {
		http.Error(w, "Unknown account", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to get rating", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(rating)
}
