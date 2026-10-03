package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go-server/models"
)

func localRequest(method, path string, body any) *http.Request {
	r := postJSON(path, body, "")
	r.Method = method
	r.RemoteAddr = "127.0.0.1:5000"
	return r
}

// piHealthReading is what pi_health posts every minute.
func piHealthReading() map[string]any {
	return map[string]any{
		"temperature":  "70.8°C",
		"cpuUsage":     "26%",
		"memoryUsage":  map[string]any{"usedMemory": 3070.09, "totalMemory": 4045},
		"diskUsage":    map[string]any{"used": "54G", "available": "169G"},
		"diskActivity": map[string]any{"readSpeed": 2008, "writeSpeed": 83996},
	}
}

func getSystemInfo(t *testing.T, limit string) []map[string]any {
	t.Helper()
	r := httptest.NewRequest("GET", "/system-info/"+limit, nil)
	r.SetPathValue("limit", limit)
	w := httptest.NewRecorder()
	SystemInfoHandler(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
	var records []map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &records))
	return records
}

func TestSystemInfoKeepsReadingsAsSent(t *testing.T) {
	freshDB(t)

	w := httptest.NewRecorder()
	PostSystemInfoHandler(w, localRequest("POST", "/system-info", piHealthReading()))
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.JSONEq(t, `{"message":"Record added successfully","recordId":1}`, w.Body.String())

	records := getSystemInfo(t, "60")
	assert.Len(t, records, 1)
	rec := records[0]
	assert.Equal(t, "70.8°C", rec["temperature"])
	assert.Equal(t, "26%", rec["cpu_usage"])
	assert.Equal(t, 3070.09, rec["memory_used"])
	assert.Equal(t, 4045.0, rec["memory_total"])
	assert.Equal(t, "54G", rec["disk_used"])
	assert.Equal(t, "169G", rec["disk_available"])
	assert.Equal(t, 2008.0, rec["disk_read_speed"])
	assert.Equal(t, 83996.0, rec["disk_write_speed"])
	// The stored text, not a reformatted time.
	assert.Regexp(t, `^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`, rec["timestamp"])
}

func TestSystemInfoMissingFieldsRefused(t *testing.T) {
	freshDB(t)
	for _, field := range []string{"temperature", "cpuUsage", "memoryUsage", "diskUsage", "diskActivity"} {
		body := piHealthReading()
		delete(body, field)
		w := httptest.NewRecorder()
		PostSystemInfoHandler(w, localRequest("POST", "/system-info", body))
		assert.Equal(t, http.StatusBadRequest, w.Code, field)
	}
	body := piHealthReading()
	body["cpuUsage"] = ""
	w := httptest.NewRecorder()
	PostSystemInfoHandler(w, localRequest("POST", "/system-info", body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, getSystemInfo(t, "60"))
}

func TestSystemInfoLimit(t *testing.T) {
	for in, want := range map[string]int{
		"": 60, "abc": 60, "0": 60, "-5": 60, "1": 1, "500": 500, "10080": 10080, "99999": 10080,
	} {
		assert.Equal(t, want, systemInfoLimit(in), in)
	}

	freshDB(t)
	for range 3 {
		w := httptest.NewRecorder()
		PostSystemInfoHandler(w, localRequest("POST", "/system-info", piHealthReading()))
	}
	assert.Len(t, getSystemInfo(t, "2"), 2)
	assert.Len(t, getSystemInfo(t, "0"), 3)
}

func TestSystemInfoStreamSendsReadingsAtOnce(t *testing.T) {
	freshDB(t)
	w := httptest.NewRecorder()
	PostSystemInfoHandler(w, localRequest("POST", "/system-info", piHealthReading()))

	// A client that has already gone gets the first event and nothing more.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/system-info/sse?limit=5", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	SystemInfoStreamHandler(w, r)

	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	body := w.Body.String()
	assert.True(t, strings.HasPrefix(body, "data: ["), body)
	assert.True(t, strings.HasSuffix(body, "]\n\n"), body)
	assert.Contains(t, body, `"temperature":"70.8°C"`)
}

func TestSystemInfoStreamRepeats(t *testing.T) {
	freshDB(t)
	saved := systemInfoStreamInterval
	systemInfoStreamInterval = 10 * time.Millisecond
	t.Cleanup(func() { systemInfoStreamInterval = saved })

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	SystemInfoStreamHandler(w, httptest.NewRequest("GET", "/system-info/sse", nil).WithContext(ctx))
	assert.GreaterOrEqual(t, strings.Count(w.Body.String(), "data: []\n\n"), 3)
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

func TestZigzagScoresTopTenAscending(t *testing.T) {
	freshDB(t)
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
	for _, origin := range []string{"", "https://evil.example", "http://raspberrypi.local.evil.example", "https://alexisraspberry.duckdns.org.evil.example"} {
		assert.Equal(t, http.StatusForbidden, postZigzag(100, origin).Code, origin)
	}

	r := postJSON("/zigzag/score", map[string]any{"score": 100}, "")
	r.Header.Set("Referer", "https://alexisraspberry.duckdns.org/zigzag/")
	w := httptest.NewRecorder()
	PostZigzagScoreHandler(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	const origin = "https://alexisraspberry.duckdns.org"
	for _, score := range []any{nil, 0, "", "abc", true, 1e300} {
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
