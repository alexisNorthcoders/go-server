package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert" // Considera usar assert para uma saída de teste mais clara
	"go-server/models"
	"go-server/monitor"
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
	mon, err := monitor.New(monitor.Config{StorePath: "metrics.db"})
	assert.NoError(t, err)
	registerPiRoutes(mon)

	serve := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		http.DefaultServeMux.ServeHTTP(w, r)
		return w
	}

	assert.Equal(t, http.StatusOK, serve("GET", "/monitor/history?range=6h").Code)
	assert.Equal(t, "*", serve("GET", "/monitor/storage").Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, http.StatusOK, serve("GET", "/monitor/events").Code)
	assert.Equal(t, http.StatusNotFound, serve("GET", "/system-info/5").Code)
	assert.Equal(t, http.StatusOK, serve("GET", "/zigzag/score").Code)
	assert.Equal(t, http.StatusForbidden, serve("POST", "/zigzag/score").Code)
	assert.Equal(t, http.StatusBadRequest, serve("GET", "/amazon-prices/last").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, serve("DELETE", "/monitor/history").Code)

	docs := serve("GET", "/api-docs/")
	assert.Equal(t, http.StatusOK, docs.Code)
	assert.Contains(t, docs.Body.String(), "swagger-ui")
	assert.Contains(t, serve("GET", "/api-docs/openapi.yaml").Body.String(), "/zigzag/score:")
}

func TestAccountRoutes(t *testing.T) {
	wd, _ := os.Getwd()
	assert.NoError(t, os.Chdir(t.TempDir()))
	defer os.Chdir(wd)
	assert.NoError(t, models.InitDB())
	defer models.DB.Close()
	mux := http.NewServeMux()
	registerAccountRoutes(mux)

	serve := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	accounts := func() int {
		var n int
		assert.NoError(t, models.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n))
		return n
	}

	for _, path := range []string{"/register", "/login"} {
		for _, method := range []string{"GET", "OPTIONS", "PUT", "DELETE"} {
			w := serve(method, path, "")
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code, method+" "+path)
			assert.Equal(t, "POST", w.Header().Get("Allow"), method+" "+path)
		}
	}
	assert.Equal(t, 0, accounts())

	assert.Equal(t, http.StatusOK, serve("POST", "/register", `{"username":"alice","password":"password123"}`).Code)
	assert.Equal(t, 1, accounts())
	assert.Equal(t, http.StatusUnauthorized, serve("POST", "/login", `{"username":"nobody","password":"b"}`).Code)
}
