package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"go-server/models"
	"go-server/utils"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

const maxRegisterBodyBytes = 4 << 10

// validateRegistration returns the trimmed username to store, or a message
// naming the first rule that failed.
func validateRegistration(username, password string) (string, string) {
	username = strings.TrimSpace(username)
	if n := utf8.RuneCountInString(username); n < 3 || n > 32 {
		return "", "Username must be 3-32 characters"
	}
	if strings.IndexFunc(username, unicode.IsControl) >= 0 {
		return "", "Username must not contain control characters"
	}
	if strings.EqualFold(username, "anonymous") {
		return "", "Username is reserved"
	}
	if len(password) < 8 || len(password) > 72 {
		return "", "Password must be 8-72 bytes"
	}
	return username, ""
}

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	username, problem := validateRegistration(req.Username, req.Password)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusInternalServerError)
		return
	}

	err = models.CreateUser(username, string(hashed))
	if err != nil {
		http.Error(w, "User already exists or error saving user", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "User created successfully"})
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// dummyPasswordHash is compared against when a username is unknown, so that
// path costs about as much as a wrong password.
func dummyPasswordHash() []byte {
	dummyHashOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)
	})
	return dummyHash
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	const invalidCredentials = "Invalid username or password"

	user, err := models.FindByUsername(username)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(req.Password))
		http.Error(w, invalidCredentials, http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		http.Error(w, invalidCredentials, http.StatusUnauthorized)
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

var anonymousTokenLimiter = utils.NewRateLimiter(10, 1*time.Minute)

func AnonymousHandler(w http.ResponseWriter, r *http.Request) {
	if rejectIfRateLimited(w, r, anonymousTokenLimiter) {
		return
	}

	userID := uuid.New().String()

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

	// A Guest token carries the username "anonymous", but the reliable test is
	// whether the userId is a registered user.
	userID, _ := claims["userId"].(string)
	isAccount, err := models.IsAccount(userID)
	if err != nil {
		http.Error(w, "Failed to verify token", http.StatusInternalServerError)
		return
	}
	kind := "guest"
	if isAccount {
		kind = "account"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Token is valid",
		"user":      claims["user"],
		"userId":    claims["userId"],
		"kind":      kind,
		"expiresIn": claims["exp"],
	})
}
