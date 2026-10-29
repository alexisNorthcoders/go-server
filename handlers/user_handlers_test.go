package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
)

func registerRaw(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "/register", strings.NewReader(body))
	response := httptest.NewRecorder()
	RegisterHandler(response, request)
	return response
}

func register(username, password string) *httptest.ResponseRecorder {
	return registerRaw(`{"username":` + jsonString(username) + `,"password":` + jsonString(password) + `}`)
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteString(string("0123456789abcdef"[r>>4]))
			b.WriteString(string("0123456789abcdef"[r&0xf]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func userCount(t *testing.T) int {
	var n int
	assert.NoError(t, models.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n))
	return n
}

func uniqueName(prefix string) string {
	return prefix + time.Now().Format("20060102150405.000000")
}

func TestRegisterRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"empty body":      "",
		"malformed JSON":  "{not json",
		"oversized body":  `{"username":"` + uniqueName("big") + `","password":"` + strings.Repeat("a", 5000) + `"}`,
		"empty username":  `{"username":"","password":"password123"}`,
		"empty password":  `{"username":"validname","password":""}`,
		"missing fields":  `{}`,
		"whitespace name": `{"username":"     ","password":"password123"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			before := userCount(t)
			response := registerRaw(body)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, before, userCount(t))
		})
	}
}

func TestRegisterUsernameRules(t *testing.T) {
	cases := map[string]string{
		"too short":        "ab",
		"too long":         strings.Repeat("x", 33),
		"control char":     "bad\x07name" + uniqueName(""),
		"newline":          "bad\nname",
		"anonymous":        "anonymous",
		"Anonymous":        "Anonymous",
		"ANONYMOUS":        "ANONYMOUS",
		"padded anon":      "  anonymous ",
		"short after trim": "  ab  ",
	}
	for name, username := range cases {
		t.Run(name, func(t *testing.T) {
			before := userCount(t)
			response := register(username, "password123")
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), "Username")
			assert.Equal(t, before, userCount(t))
		})
	}

	t.Run("multibyte characters count once", func(t *testing.T) {
		username := strings.Repeat("é", 32)
		models.DB.Exec("DELETE FROM users WHERE username = ?", username)
		response := register(username, "password123")
		assert.Equal(t, http.StatusOK, response.Code)
		models.DB.Exec("DELETE FROM users WHERE username = ?", username)
	})
}

func TestRegisterPasswordRules(t *testing.T) {
	for name, password := range map[string]string{
		"too short": "1234567",
		"too long":  strings.Repeat("a", 73),
	} {
		t.Run(name, func(t *testing.T) {
			username := uniqueName("pw")
			response := register(username, password)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), "Password must be 8-72 bytes")
			_, err := models.FindByUsername(username)
			assert.Error(t, err)
		})
	}

	t.Run("72 bytes accepted and not trimmed", func(t *testing.T) {
		username := uniqueName("pw72")
		response := register(username, " "+strings.Repeat("a", 70)+" ")
		assert.Equal(t, http.StatusOK, response.Code)
	})
}

func TestRegisterStoresTrimmedUsername(t *testing.T) {
	username := uniqueName("trim")
	response := register("  "+username+"\t ", "password123")
	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"message":"User created successfully"}`, response.Body.String())

	user, err := models.FindByUsername(username)
	assert.NoError(t, err)
	assert.Equal(t, username, user.Username)
}

func TestRegisterAcceptsUnknownFieldsAndRejectsDuplicates(t *testing.T) {
	username := uniqueName("dup")
	response := registerRaw(`{"username":"` + username + `","password":"password123","extra":1}`)
	assert.Equal(t, http.StatusOK, response.Code)

	response = register(username, "password123")
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "User already exists or error saving user")
}
