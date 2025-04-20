package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"go-server/models"
	"go-server/utils"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	json.NewDecoder(r.Body).Decode(&req)

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusInternalServerError)
		return
	}

	err = models.CreateUser(req.Username, string(hashed))
	if err != nil {
		http.Error(w, "User already exists or error saving user", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "User created successfully"})
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	json.NewDecoder(r.Body).Decode(&req)

	user, err := models.FindByUsername(req.Username)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		http.Error(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	token, err := utils.GenerateToken(user.Username, user.ID)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message":     "Login successful!",
		"accessToken": token,
		"userId":      user.ID,
	})
}

func AnonymousHandler(w http.ResponseWriter, r *http.Request) {
	userID := uuid.New().String()

	if err := models.CreateAnonymousUser(userID); err != nil {
		http.Error(w, "Failed to create anonymous user", http.StatusInternalServerError)
		return
	}

	token, err := utils.GenerateToken("anonymous", userID)
	if err != nil {
		http.Error(w, "Failed to generate anonymous token", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message":     "Anonymous login successful!",
		"accessToken": token,
		"userId":      userID,
	})
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Logout successful!",
	})
}

func ValidateHandler(w http.ResponseWriter, r *http.Request) {
	var token string

	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	if token == "" {
		var body struct {
			Token string `json:"token"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil || body.Token == "" {
			http.Error(w, "No token provided", http.StatusBadRequest)
			return
		}
		token = body.Token
	}

	claims, err := utils.ValidateToken(token)
	if err != nil {
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Token is valid",
		"user":      claims["user"],
		"userId":    claims["userId"],
		"expiresIn": claims["exp"],
	})
}
