package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"go-server/models"
	"go-server/utils"
)

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var appearanceKeys = map[string]bool{"head": true, "body": true, "eyes": true}

// AppearanceHandler serves GET and PUT /appearance for the authenticated user.
func AppearanceHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := utils.GetUserIDFromToken(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		data, err := models.GetAppearance(userID)
		if errors.Is(err, models.ErrAppearanceNotFound) {
			http.Error(w, "No appearance saved", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "Failed to get appearance", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var doc map[string]string
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil || doc == nil {
			http.Error(w, "Invalid appearance", http.StatusBadRequest)
			return
		}
		for k, v := range doc {
			if !appearanceKeys[k] || !hexColour.MatchString(v) {
				http.Error(w, "Invalid appearance", http.StatusBadRequest)
				return
			}
		}
		data, _ := json.Marshal(doc)
		if err := models.SaveAppearance(userID, data); err != nil {
			http.Error(w, "Failed to save appearance", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
