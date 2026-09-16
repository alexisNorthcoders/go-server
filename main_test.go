package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert" // Considera usar assert para uma saída de teste mais clara
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
