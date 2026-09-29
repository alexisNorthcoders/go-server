package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-server/models"
)

const botSecret = "bot-secret"

func botResultBody(over map[string]any) map[string]any {
	body := map[string]any{"resultId": "r1", "botId": "rookie", "mode": "timed", "delay": 2, "outcome": "win"}
	for k, v := range over {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

func reportBotResult(body map[string]any, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	PostBotResultHandler(w, postJSON("/bot-results", body, token))
	return w
}

func botResultCount(t *testing.T) int {
	var n int
	assert.NoError(t, models.DB.QueryRow("SELECT COUNT(*) FROM bot_results").Scan(&n))
	return n
}

func TestBotResultIsStoredAndDuplicateStoredOnce(t *testing.T) {
	freshDB(t)
	t.Setenv("BOT_RESULTS_SECRET", botSecret)

	assert.Equal(t, http.StatusOK, reportBotResult(botResultBody(nil), botSecret).Code)
	assert.Equal(t, 1, botResultCount(t))
	assert.Equal(t, http.StatusOK, reportBotResult(botResultBody(nil), botSecret).Code)
	assert.Equal(t, 1, botResultCount(t))

	var recordedAt string
	assert.NoError(t, models.DB.QueryRow("SELECT recorded_at FROM bot_results").Scan(&recordedAt))
	assert.NotEmpty(t, recordedAt)
}

func TestBotResultRefusedWithoutValidSecret(t *testing.T) {
	freshDB(t)
	t.Setenv("BOT_RESULTS_SECRET", botSecret)
	assert.Equal(t, http.StatusUnauthorized, reportBotResult(botResultBody(nil), "wrong").Code)
	assert.Equal(t, http.StatusUnauthorized, reportBotResult(botResultBody(nil), "").Code)
	assert.Equal(t, 0, botResultCount(t))

	t.Setenv("BOT_RESULTS_SECRET", "")
	assert.Equal(t, http.StatusUnauthorized, reportBotResult(botResultBody(nil), "").Code)
	assert.Equal(t, http.StatusUnauthorized, reportBotResult(botResultBody(nil), "Bearer ").Code)
	assert.Equal(t, 0, botResultCount(t))
}

func TestBotResultBadBodyIsRefused(t *testing.T) {
	freshDB(t)
	t.Setenv("BOT_RESULTS_SECRET", botSecret)
	for name, over := range map[string]map[string]any{
		"bad mode":         {"mode": "vs-bot"},
		"missing mode":     {"mode": nil},
		"delay too high":   {"delay": 5},
		"negative delay":   {"delay": -1},
		"missing delay":    {"delay": nil},
		"bad outcome":      {"outcome": "won"},
		"missing outcome":  {"outcome": nil},
		"empty botId":      {"botId": ""},
		"uppercase botId":  {"botId": "Rookie"},
		"botId with slash": {"botId": "a/b"},
		"long botId":       {"botId": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"missing resultId": {"resultId": nil},
	} {
		assert.Equal(t, http.StatusBadRequest, reportBotResult(botResultBody(over), botSecret).Code, name)
	}
	assert.Equal(t, 0, botResultCount(t))
}

func TestBotRecordsTotalsPerBotAndMode(t *testing.T) {
	freshDB(t)
	t.Setenv("BOT_RESULTS_SECRET", botSecret)
	for i, r := range []map[string]any{
		{"botId": "rookie", "mode": "timed", "outcome": "win"},
		{"botId": "rookie", "mode": "timed", "outcome": "win"},
		{"botId": "rookie", "mode": "timed", "outcome": "loss"},
		{"botId": "rookie", "mode": "endless", "outcome": "draw"},
		{"botId": "dummy", "mode": "timed", "outcome": "loss"},
	} {
		r["resultId"] = string(rune('a' + i))
		assert.Equal(t, http.StatusOK, reportBotResult(botResultBody(r), botSecret).Code)
	}

	get := func(url string) map[string]map[string]models.BotRecord {
		w := httptest.NewRecorder()
		BotRecordsHandler(w, httptest.NewRequest("GET", url, nil))
		assert.Equal(t, http.StatusOK, w.Code)
		var out map[string]map[string]models.BotRecord
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out
	}

	assert.Equal(t, map[string]map[string]models.BotRecord{
		"rookie": {"timed": {Wins: 2, Losses: 1}, "endless": {Draws: 1}},
		"dummy":  {"timed": {Losses: 1}},
	}, get("/bot-records"))
	assert.Equal(t, map[string]map[string]models.BotRecord{
		"dummy": {"timed": {Losses: 1}},
	}, get("/bot-records?botId=dummy"))
	assert.Empty(t, get("/bot-records?botId=nobody"))
}
