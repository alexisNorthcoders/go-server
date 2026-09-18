package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"go-server/utils"
)

func init() {
	os.Setenv("DATABASE_SECRET", "test-secret-key")
	models.InitDB()
}

func TestPostAuthenticatedScoreHandler(t *testing.T) {
	userID := "test-user-auth-" + time.Now().Format("20060102150405")
	token, _ := utils.GenerateToken("testuser", userID)

	req := AuthenticatedScoreRequest{
		Token:    token,
		ClientID: "test-client-1",
		Score:    100,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	PostAuthenticatedScoreHandler(response, request)

	assert.Equal(t, http.StatusOK, response.Code)

	var resp map[string]string
	json.NewDecoder(response.Body).Decode(&resp)
	assert.Equal(t, "Score added", resp["message"])

	// Verify score was stored
	scores, err := models.GetScoresForUser(userID)
	assert.NoError(t, err)
	assert.Greater(t, len(scores), 0)
	assert.Equal(t, 100, scores[0].Score)
}

func TestPostAnonymousScoreHandler(t *testing.T) {
	clientID := "anon-client-" + time.Now().Format("20060102150405.000")

	req := AnonymousScoreRequest{
		ClientID: clientID,
		Score:    150,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/anonymous", bytes.NewReader(body))
	response := httptest.NewRecorder()

	PostAnonymousScoreHandler(response, request)

	assert.Equal(t, http.StatusOK, response.Code)

	var resp map[string]string
	json.NewDecoder(response.Body).Decode(&resp)
	assert.Equal(t, "Anonymous score added", resp["message"])

	// Verify score was stored
	scores, err := models.GetAnonymousScoresForClient(clientID)
	assert.NoError(t, err)
	assert.Greater(t, len(scores), 0)
	assert.Equal(t, 150, scores[0].Score)
}

func TestGetUserScoresWithPathHandler(t *testing.T) {
	userID := "test-user-path-" + time.Now().Format("20060102150405.000")

	// Add some test scores
	models.AddScore(userID, 100)
	models.AddScore(userID, 200)

	request := httptest.NewRequest("GET", "/scores/"+userID, nil)
	response := httptest.NewRecorder()

	GetUserScoresWithPathHandler(response, request, userID)

	assert.Equal(t, http.StatusOK, response.Code)

	var scores []models.Score
	json.NewDecoder(response.Body).Decode(&scores)
	assert.Greater(t, len(scores), 0)
	// Check that we have the scores we added (may have others from other tests)
	foundScores := 0
	for _, s := range scores {
		if s.Score == 100 || s.Score == 200 {
			foundScores++
		}
	}
	assert.GreaterOrEqual(t, foundScores, 2)
}

func TestGetAnonymousScoresHandler(t *testing.T) {
	clientID := "anon-client-test-" + time.Now().Format("20060102150405.000")

	// Add some test scores
	models.AddAnonymousScore(clientID, 50)
	models.AddAnonymousScore(clientID, 75)

	request := httptest.NewRequest("GET", "/scores/anonymous/"+clientID, nil)
	response := httptest.NewRecorder()

	GetAnonymousScoresHandler(response, request, clientID)

	assert.Equal(t, http.StatusOK, response.Code)

	var scores []models.Score
	json.NewDecoder(response.Body).Decode(&scores)
	assert.Greater(t, len(scores), 0)
	for _, s := range scores {
		assert.NotNil(t, s.ClientID)
		assert.Equal(t, clientID, *s.ClientID)
	}
}

func TestPostAuthenticatedScoreHandlerInvalidRequest(t *testing.T) {
	token, _ := utils.GenerateToken("test-user", "test-user-id")

	request := httptest.NewRequest("POST", "/scores", bytes.NewReader([]byte("invalid json")))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	PostAuthenticatedScoreHandler(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestPostAnonymousScoreHandlerMissingClientID(t *testing.T) {
	req := AnonymousScoreRequest{
		Score: 100,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/anonymous", bytes.NewReader(body))
	response := httptest.NewRecorder()

	PostAnonymousScoreHandler(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestMigrateScoresHandler(t *testing.T) {
	clientID := "migrate-client-" + time.Now().Format("20060102150405.000")
	userID := "migrate-user-" + time.Now().Format("20060102150405")
	token, _ := utils.GenerateToken("testuser", userID)

	// Add some anonymous scores
	models.AddAnonymousScore(clientID, 100)
	models.AddAnonymousScore(clientID, 200)

	// Verify anonymous scores exist
	anonScores, _ := models.GetAnonymousScoresForClient(clientID)
	assert.Greater(t, len(anonScores), 0)
	expectedCount := len(anonScores)

	// Migrate scores
	req := MigrateScoresRequest{
		UserID: userID,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/migrate/"+clientID, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	MigrateScoresHandler(response, request, clientID)

	assert.Equal(t, http.StatusOK, response.Code)

	var resp map[string]interface{}
	json.NewDecoder(response.Body).Decode(&resp)
	assert.Equal(t, "Scores migrated successfully", resp["message"])
	assert.Equal(t, float64(expectedCount), resp["count"])

	// Verify scores are now associated with user
	userScores, err := models.GetScoresForUser(userID)
	assert.NoError(t, err)
	assert.Equal(t, expectedCount, len(userScores))

	// Verify scores no longer have client_id
	for _, s := range userScores {
		assert.Nil(t, s.ClientID)
		assert.NotNil(t, s.UserID)
		assert.Equal(t, userID, *s.UserID)
	}

	// Verify anonymous scores no longer exist for this client
	anonScoresAfter, _ := models.GetAnonymousScoresForClient(clientID)
	assert.Equal(t, 0, len(anonScoresAfter))
}

func TestMigrateScoresHandlerNoScores(t *testing.T) {
	clientID := "migrate-client-noscore-" + time.Now().Format("20060102150405.000")
	userID := "migrate-user-noscore-" + time.Now().Format("20060102150405")
	token, _ := utils.GenerateToken("testuser", userID)

	req := MigrateScoresRequest{
		UserID: userID,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/migrate/"+clientID, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	MigrateScoresHandler(response, request, clientID)

	assert.Equal(t, http.StatusOK, response.Code)

	var resp map[string]interface{}
	json.NewDecoder(response.Body).Decode(&resp)
	assert.Equal(t, float64(0), resp["count"])
}

func TestMigrateScoresHandlerUnauthorized(t *testing.T) {
	clientID := "migrate-client-unauth-" + time.Now().Format("20060102150405.000")
	userID := "migrate-user-unauth-" + time.Now().Format("20060102150405")

	req := MigrateScoresRequest{
		UserID: userID,
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/migrate/"+clientID, bytes.NewReader(body))
	// Intentionally no Authorization header
	response := httptest.NewRecorder()

	MigrateScoresHandler(response, request, clientID)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestMigrateScoresHandlerUserMismatch(t *testing.T) {
	clientID := "migrate-client-mismatch-" + time.Now().Format("20060102150405.000")
	userID := "migrate-user-mismatch-" + time.Now().Format("20060102150405")
	token, _ := utils.GenerateToken("testuser", userID)

	req := MigrateScoresRequest{
		UserID: "different-user-id",
	}

	body, _ := json.Marshal(req)
	request := httptest.NewRequest("POST", "/scores/migrate/"+clientID, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	MigrateScoresHandler(response, request, clientID)

	assert.Equal(t, http.StatusForbidden, response.Code)
}

// Helper function to create a test user
func createTestUser(t *testing.T) (models.User, string) {
	return createTestUserWithID(t, "test-user-123")
}

func createTestUserWithID(t *testing.T, id string) (models.User, string) {
	user := models.User{
		ID:       id,
		Username: "testuser-" + id,
		Password: "hashedpassword",
	}

	token, err := utils.GenerateToken(user.Username, user.ID)
	assert.NoError(t, err)

	return user, token
}
