package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"golang.org/x/crypto/bcrypt"
)

var testIPCounter atomic.Int64

// freshIP gives each helper call its own client IP so the per-IP limiters
// don't see the whole test suite as one client.
func freshIP() string {
	n := testIPCounter.Add(1)
	return fmt.Sprintf("198.18.%d.%d:1", n/250, n%250+1)
}

func registerRaw(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "/register", strings.NewReader(body))
	request.RemoteAddr = freshIP()
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

func loginRaw(body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "/login", strings.NewReader(body))
	request.RemoteAddr = freshIP()
	response := httptest.NewRecorder()
	LoginHandler(response, request)
	return response
}

func login(username, password string) *httptest.ResponseRecorder {
	return loginRaw(`{"username":` + jsonString(username) + `,"password":` + jsonString(password) + `}`)
}

func TestLoginRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"empty body":     "",
		"malformed JSON": "{not json",
		"oversized body": `{"username":"x","password":"` + strings.Repeat("a", 5000) + `"}`,
		"empty username": `{"username":"","password":"password123"}`,
		"blank username": `{"username":"   ","password":"password123"}`,
		"empty password": `{"username":"someone","password":""}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, loginRaw(body).Code)
		})
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	name := uniqueName("login-fail")
	assert.Equal(t, http.StatusOK, register(name, "password123").Code)

	unknown := login(uniqueName("nobody"), "password123")
	wrong := login(name, "wrong-password")
	long := login(name, strings.Repeat("a", 73))

	assert.Equal(t, http.StatusUnauthorized, unknown.Code)
	for _, got := range []*httptest.ResponseRecorder{wrong, long} {
		assert.Equal(t, unknown.Code, got.Code)
		assert.Equal(t, unknown.Body.String(), got.Body.String())
	}
	assert.Equal(t, "Invalid username or password\n", unknown.Body.String())
}

func TestLoginTrimsUsername(t *testing.T) {
	name := uniqueName("login-trim")
	assert.Equal(t, http.StatusOK, register(name, "password123").Code)
	assert.Equal(t, http.StatusOK, login("  "+name+"\t ", "password123").Code)
}

func TestLoginAcceptsLegacyShortPassword(t *testing.T) {
	name := uniqueName("legacy")
	hash, err := bcrypt.GenerateFromPassword([]byte("abc"), bcrypt.MinCost)
	assert.NoError(t, err)
	assert.NoError(t, models.CreateUser(name, string(hash)))
	assert.Equal(t, http.StatusOK, login(name, "abc").Code)
}

func TestLoginTokenVerifiesAsAccount(t *testing.T) {
	name := uniqueName("login-ok")
	assert.Equal(t, http.StatusOK, register(name, "password123").Code)

	w := login(name, "password123")
	assert.Equal(t, http.StatusOK, w.Code)
	var out map[string]string
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "Login successful!", out["message"])
	assert.NotEmpty(t, out["userId"])

	r := httptest.NewRequest("POST", "/verify-token", nil)
	r.Header.Set("Authorization", "Bearer "+out["accessToken"])
	v := httptest.NewRecorder()
	ValidateHandler(v, r)
	assert.Equal(t, http.StatusOK, v.Code)
	var verified map[string]any
	assert.NoError(t, json.Unmarshal(v.Body.Bytes(), &verified))
	assert.Equal(t, "account", verified["kind"])
}

func TestLoginRateLimited(t *testing.T) {
	assertRateLimited(t, "203.0.113.40", 10, func(realIP, xff string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(`{"username":"nobody-rl","password":"whatever1"}`))
		fromNginx(r, realIP, xff)
		w := httptest.NewRecorder()
		LoginHandler(w, r)
		return w
	})
}

func TestRegisterRateLimitedBeforeParsing(t *testing.T) {
	do := func(realIP, xff, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/register", strings.NewReader(body))
		fromNginx(r, realIP, xff)
		w := httptest.NewRecorder()
		RegisterHandler(w, r)
		return w
	}
	validBody := func() string {
		return `{"username":` + jsonString(uniqueName("rl-reg-")) + `,"password":"password123"}`
	}
	ip := "203.0.113.50"
	for i := 0; i < 5; i++ {
		assert.Equal(t, http.StatusOK, do(ip, "", validBody()).Code, "request %d", i+1)
	}
	before := userCount(t)
	resp := do(ip, "", validBody())
	assert.Equal(t, http.StatusTooManyRequests, resp.Code)
	assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":"Rate limit exceeded. Try again later."}`, resp.Body.String())
	assert.Equal(t, http.StatusTooManyRequests, do(ip, "198.51.100.7", validBody()).Code)
	assert.Equal(t, before, userCount(t))
	assert.Equal(t, http.StatusOK, do(ip+".other", "", validBody()).Code)
}
