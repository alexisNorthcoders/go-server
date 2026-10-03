package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert" // Considera usar assert para uma saída de teste mais clara
	"go-server/models"
)

func TestLogRequest(t *testing.T) {
	// Create a buffer to capture log output
	var buffer bytes.Buffer
	log.SetOutput(&buffer)
	defer func() {
		log.SetOutput(io.Discard) // Clean up after test
	}()

	// Create a mock handler
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Hello, World!")
	})

	// Create a logged endpoint
	logger := logRequest(mockHandler, "/hello")

	// Create a test request
	request := httptest.NewRequest("GET", "/hello", nil)
	request.RemoteAddr = "127.0.0.1:1234" // Important: default RemoteAddr might be blank
	response := httptest.NewRecorder()

	// Serve the request
	logger.ServeHTTP(response, request)

	// Check response body
	assert.Equal(t, "Hello, World!", response.Body.String())

	// Check log output
	logOutput := buffer.String()
	expectedLog := "Endpoint called: /hello | Method: GET | RemoteAddr: 127.0.0.1"
	assert.Contains(t, logOutput, expectedLog)
}

func TestPiRoutes(t *testing.T) {
	wd, _ := os.Getwd()
	assert.NoError(t, os.Chdir(t.TempDir()))
	defer os.Chdir(wd)
	assert.NoError(t, models.InitDB())
	defer models.DB.Close()
	registerPiRoutes()

	serve := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		http.DefaultServeMux.ServeHTTP(w, r)
		return w
	}

	assert.Equal(t, http.StatusOK, serve("GET", "/system-info/5").Code)
	assert.Equal(t, "application/json", serve("GET", "/system-info/5").Header().Get("Content-Type"))
	assert.Equal(t, http.StatusOK, serve("GET", "/zigzag/score").Code)
	assert.Equal(t, http.StatusForbidden, serve("POST", "/zigzag/score").Code)
	assert.Equal(t, http.StatusBadRequest, serve("GET", "/amazon-prices/last").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, serve("DELETE", "/system-info/5").Code)

	docs := serve("GET", "/api-docs/")
	assert.Equal(t, http.StatusOK, docs.Code)
	assert.Contains(t, docs.Body.String(), "swagger-ui")
	assert.Contains(t, serve("GET", "/api-docs/openapi.yaml").Body.String(), "/zigzag/score:")
}
