package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"go-server/utils"
)

func localRequest(method, path string, body any) *http.Request {
	r := postJSON(path, body, "")
	r.Method = method
	r.RemoteAddr = "127.0.0.1:5000"
	return r
}

func TestLocalOnly(t *testing.T) {
	ok := LocalOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	call := func(remote string, headers map[string]string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		ok(w, r)
		return w.Code
	}

	assert.Equal(t, http.StatusTeapot, call("127.0.0.1:1234", nil))
	assert.Equal(t, http.StatusTeapot, call("[::1]:1234", nil))
	assert.Equal(t, http.StatusForbidden, call("8.8.8.8:1234", nil))
	assert.Equal(t, http.StatusForbidden, call("192.0.2.10:1234", nil))
	// nginx connects from loopback on behalf of anyone.
	assert.Equal(t, http.StatusForbidden, call("127.0.0.1:1234", map[string]string{"X-Forwarded-For": "8.8.8.8"}))
	assert.Equal(t, http.StatusForbidden, call("127.0.0.1:1234", map[string]string{"X-Real-IP": "8.8.8.8"}))
}

func TestAmazonPrices(t *testing.T) {
	freshDB(t)
	const product = "https://www.amazon.co.uk/dp/B01D8KOZF4"
	last := func() string {
		r := httptest.NewRequest("GET", "/amazon-prices/last?url="+product, nil)
		w := httptest.NewRecorder()
		LastAmazonPriceHandler(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}
	record := func(price, at string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		PostAmazonPriceHandler(w, localRequest("POST", "/amazon-prices", map[string]string{
			"url": product, "title": "ELEGOO UNO R3", "price": price, "timestamp": at,
		}))
		return w
	}

	assert.JSONEq(t, `{"url":"`+product+`","lastPrice":null}`, last())

	w := record("£42.99", "2026-09-06T10:34:33.896Z")
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.JSONEq(t, `{"message":"Price recorded","id":1}`, w.Body.String())
	record("£39.99", "2026-09-07T10:00:00.000Z")
	record("£45.00", "2026-09-01T10:00:00.000Z")
	assert.JSONEq(t, `{"url":"`+product+`","lastPrice":"£39.99"}`, last())

	assert.Equal(t, http.StatusBadRequest, record("", "2026-09-07T10:00:00.000Z").Code)

	w = httptest.NewRecorder()
	LastAmazonPriceHandler(w, httptest.NewRequest("GET", "/amazon-prices/last", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func postZigzag(score any, origin string) *httptest.ResponseRecorder {
	r := postJSON("/zigzag/score", map[string]any{"score": score}, "")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	PostZigzagScoreHandler(w, r)
	return w
}

func getZigzag(t *testing.T) []models.ZigzagScore {
	t.Helper()
	w := httptest.NewRecorder()
	ZigzagScoresHandler(w, httptest.NewRequest("GET", "/zigzag/score", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	var scores []models.ZigzagScore
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &scores))
	return scores
}

// withZigzagLimit swaps in a limiter allowing n scores per client for the test.
func withZigzagLimit(t *testing.T, n int) {
	t.Helper()
	saved := zigzagScoreLimiter
	zigzagScoreLimiter = utils.NewRateLimiter(n, time.Minute)
	t.Cleanup(func() { zigzagScoreLimiter.Stop(); zigzagScoreLimiter = saved })
}

func TestZigzagScoresTopTenAscending(t *testing.T) {
	freshDB(t)
	withZigzagLimit(t, 100)
	assert.Empty(t, getZigzag(t))

	for _, s := range []int{500, 100, 1200, 300, 900, 50, 700, 1100, 200, 800, 1000, 600} {
		w := postZigzag(s, "https://alexisraspberry.duckdns.org")
		assert.Equal(t, http.StatusCreated, w.Code)
	}
	w := postZigzag(400, "http://raspberrypi.local")
	assert.JSONEq(t, `{"message":"Highscore added: 400"}`, w.Body.String())

	var got []int
	for _, s := range getZigzag(t) {
		got = append(got, s.Score)
		assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`, s.Timestamp)
	}
	// The game shows the last one as the high score.
	assert.Equal(t, []int{300, 400, 500, 600, 700, 800, 900, 1000, 1100, 1200}, got)
}

func TestZigzagScoreRefusals(t *testing.T) {
	freshDB(t)
	withZigzagLimit(t, 100)
	for _, origin := range []string{"", "https://evil.example", "http://raspberrypi.local.evil.example", "https://alexisraspberry.duckdns.org.evil.example"} {
		assert.Equal(t, http.StatusForbidden, postZigzag(100, origin).Code, origin)
	}

	r := postJSON("/zigzag/score", map[string]any{"score": 100}, "")
	r.Header.Set("Referer", "https://alexisraspberry.duckdns.org/zigzag/")
	w := httptest.NewRecorder()
	PostZigzagScoreHandler(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	const origin = "https://alexisraspberry.duckdns.org"
	for _, score := range []any{nil, 0, "", "abc", true, 1e300, -5, 1_000_001} {
		assert.Equal(t, http.StatusBadRequest, postZigzag(score, origin).Code, score)
	}
	assert.Equal(t, http.StatusCreated, postZigzag("250", origin).Code)
	assert.Equal(t, http.StatusCreated, postZigzag(12.7, origin).Code)

	var got []int
	for _, s := range getZigzag(t) {
		got = append(got, s.Score)
	}
	assert.Equal(t, []int{12, 100, 250}, got)
}

func TestZigzagScoreRateLimitedPerClient(t *testing.T) {
	freshDB(t)
	withZigzagLimit(t, 2)

	post := func(realIP, forwardedFor string) int {
		r := postJSON("/zigzag/score", map[string]any{"score": 100}, "")
		r.Header.Set("Origin", "https://alexisraspberry.duckdns.org")
		r.RemoteAddr = "127.0.0.1:5000" // nginx
		r.Header.Set("X-Real-IP", realIP)
		if forwardedFor != "" {
			r.Header.Set("X-Forwarded-For", forwardedFor)
		}
		w := httptest.NewRecorder()
		PostZigzagScoreHandler(w, r)
		return w.Code
	}

	assert.Equal(t, http.StatusCreated, post("203.0.113.1", ""))
	assert.Equal(t, http.StatusCreated, post("203.0.113.1", ""))
	// A made-up X-Forwarded-For does not get a fresh allowance.
	assert.Equal(t, http.StatusTooManyRequests, post("203.0.113.1", "198.51.100.7"))
	assert.Equal(t, http.StatusCreated, post("203.0.113.2", ""))
}

func TestZigzagClientIPTrustsXRealIPOnlyFromThisMachine(t *testing.T) {
	r := httptest.NewRequest("POST", "/zigzag/score", nil)
	r.Header.Set("X-Real-IP", "203.0.113.1")
	r.RemoteAddr = "127.0.0.1:5000"
	assert.Equal(t, "203.0.113.1", zigzagClientIP(r))
	r.RemoteAddr = "192.0.2.50:5000"
	assert.Equal(t, "192.0.2.50", zigzagClientIP(r))
}
