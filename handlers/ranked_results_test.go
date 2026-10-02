package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"go-server/utils"
)

func addUser(t *testing.T, id string) {
	_, err := models.DB.Exec("INSERT INTO users (id, username, password) VALUES (?, ?, 'pw')", id, "name-"+id)
	assert.NoError(t, err)
}

func rankedBody(over map[string]any) map[string]any {
	body := map[string]any{
		"resultId": "m1", "a": map[string]any{"accountId": "alice"}, "b": map[string]any{"accountId": "bob"},
		"outcome": "a", "forfeit": false,
	}
	for k, v := range over {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

func reportRanked(t *testing.T, body map[string]any, token string) (*httptest.ResponseRecorder, models.RankedResponse) {
	w := httptest.NewRecorder()
	PostRankedResultHandler(w, postJSON("/ranked-results", body, token))
	var resp models.RankedResponse
	if w.Code == http.StatusOK {
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

func ratingOf(t *testing.T, id string) models.Rating {
	w := httptest.NewRecorder()
	RatingHandler(w, httptest.NewRequest("GET", "/rating?userId="+id, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	var r models.Rating
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
	return r
}

func rankedSetup(t *testing.T) {
	freshDB(t)
	t.Setenv("BOT_RESULTS_SECRET", botSecret)
	addUser(t, "alice")
	addUser(t, "bob")
}

func TestRankedResultRefusedWithoutValidSecret(t *testing.T) {
	rankedSetup(t)
	for _, token := range []string{"wrong", ""} {
		w, _ := reportRanked(t, rankedBody(nil), token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	}
	t.Setenv("BOT_RESULTS_SECRET", "")
	w, _ := reportRanked(t, rankedBody(nil), "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var n int
	assert.NoError(t, models.DB.QueryRow("SELECT (SELECT COUNT(*) FROM ratings) + (SELECT COUNT(*) FROM ranked_results)").Scan(&n))
	assert.Equal(t, 0, n)
}

func TestRankedWinMovesRatingsApart(t *testing.T) {
	rankedSetup(t)
	w, resp := reportRanked(t, rankedBody(nil), botSecret)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, resp.Players, 2)
	alice, bob := resp.Players[0], resp.Players[1]
	assert.Equal(t, "alice", alice.AccountID)
	assert.Equal(t, 1500.0, alice.Before)
	assert.Greater(t, alice.After, alice.Before)
	assert.Less(t, bob.After, bob.Before)
	assert.Equal(t, alice.After, ratingOf(t, "alice").Rating)
	assert.Equal(t, bob.After, ratingOf(t, "bob").Rating)
}

func TestRankedDrawIsRecorded(t *testing.T) {
	rankedSetup(t)
	w, resp := reportRanked(t, rankedBody(map[string]any{"outcome": "draw"}), botSecret)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, resp.Players[0].RankedMatches)
	assert.Equal(t, 1, ratingOf(t, "bob").RankedMatches)
	// Equal ratings and a draw: nobody moves.
	assert.InDelta(t, 1500, resp.Players[0].After, 0.001)
}

func TestRankedAgainstStandInChangesOnlyTheAccount(t *testing.T) {
	rankedSetup(t)
	body := rankedBody(map[string]any{"b": map[string]any{"standInId": "rookie", "rating": 1700}})
	w, resp := reportRanked(t, body, botSecret)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, resp.Players, 1)
	assert.Greater(t, resp.Players[0].After, resp.Players[0].Before)

	var n int
	assert.NoError(t, models.DB.QueryRow("SELECT COUNT(*) FROM ratings").Scan(&n))
	assert.Equal(t, 1, n)

	// A stand-in with no rating, or on both sides, is refused.
	w, _ = reportRanked(t, rankedBody(map[string]any{"resultId": "m2", "b": map[string]any{"standInId": "rookie"}}), botSecret)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	two := rankedBody(map[string]any{"resultId": "m3",
		"a": map[string]any{"standInId": "x", "rating": 1500}, "b": map[string]any{"standInId": "y", "rating": 1500}})
	w, _ = reportRanked(t, two, botSecret)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRankedForfeitIsALossForTheForfeiter(t *testing.T) {
	rankedSetup(t)
	// Bob wins because alice forfeited.
	w, resp := reportRanked(t, rankedBody(map[string]any{"outcome": "b", "forfeit": true}), botSecret)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, resp.Forfeit)
	assert.Less(t, resp.Players[0].After, resp.Players[0].Before)
	assert.Greater(t, resp.Players[1].After, resp.Players[1].Before)

	w, _ = reportRanked(t, rankedBody(map[string]any{"resultId": "m2", "outcome": "draw", "forfeit": true}), botSecret)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRankedDuplicateResultIDAppliedOnce(t *testing.T) {
	rankedSetup(t)
	_, first := reportRanked(t, rankedBody(nil), botSecret)
	afterFirst := ratingOf(t, "alice")
	w, second := reportRanked(t, rankedBody(nil), botSecret)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, first, second)
	assert.Equal(t, afterFirst, ratingOf(t, "alice"))
	assert.Equal(t, 1, afterFirst.RankedMatches)
}

func TestRankedProvisionalUntilFifthMatch(t *testing.T) {
	rankedSetup(t)
	for i := 1; i <= 5; i++ {
		_, resp := reportRanked(t, rankedBody(map[string]any{"resultId": fmt.Sprintf("match-%d", i)}), botSecret)
		assert.Len(t, resp.Players, 2)
		for _, p := range resp.Players {
			assert.Equal(t, i, p.RankedMatches, "match %d", i)
			assert.Equal(t, i < 5, p.Provisional, "match %d", i)
		}
		if i == 5 {
			for _, p := range resp.Players {
				assert.False(t, p.Provisional, "%s after 5th match", p.AccountID)
			}
		}
	}
	assert.False(t, ratingOf(t, "alice").Provisional)
}

func TestRankedBadBodyIsRefused(t *testing.T) {
	rankedSetup(t)
	for name, over := range map[string]map[string]any{
		"missing resultId": {"resultId": nil},
		"bad outcome":      {"outcome": "alice"},
		"same account":     {"b": map[string]any{"accountId": "alice"}},
		"empty side":       {"a": map[string]any{}},
		"both ids":         {"a": map[string]any{"accountId": "alice", "standInId": "x", "rating": 1500}},
	} {
		w, _ := reportRanked(t, rankedBody(over), botSecret)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
	}
	w, _ := reportRanked(t, rankedBody(map[string]any{"b": map[string]any{"accountId": "ghost"}}), botSecret)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, 1500.0, ratingOf(t, "alice").Rating)
}

func TestRatingDefaultsForAccountThatNeverPlayed(t *testing.T) {
	rankedSetup(t)
	r := ratingOf(t, "alice")
	assert.Equal(t, 1500.0, r.Rating)
	assert.Equal(t, 0, r.RankedMatches)
	assert.True(t, r.Provisional)

	w := httptest.NewRecorder()
	RatingHandler(w, httptest.NewRequest("GET", "/rating?userId=ghost", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestVerifyTokenSaysAccountOrGuest(t *testing.T) {
	rankedSetup(t)
	kind := func(token string) string {
		r := httptest.NewRequest("POST", "/verify-token", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		ValidateHandler(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		var out map[string]any
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out["kind"].(string)
	}
	account, _ := utils.GenerateToken("name-alice", "alice")
	guest, _ := utils.GenerateToken("anonymous", "some-guest-id")
	assert.Equal(t, "account", kind(account))
	assert.Equal(t, "guest", kind(guest))
}

func getRatingLeaderboard(t *testing.T) []models.RatingRow {
	w := httptest.NewRecorder()
	RatingLeaderboardHandler(w, httptest.NewRequest("GET", "/rating-leaderboard", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	var rows []models.RatingRow
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	return rows
}

func TestRatingLeaderboardOrdersByRatingAndHidesProvisional(t *testing.T) {
	rankedSetup(t)
	addUser(t, "carol")
	// alice beats bob 5 times; both reach 5 matches. carol has played none.
	for i := 0; i < models.ProvisionalMatches; i++ {
		if i > 0 {
			rows := getRatingLeaderboard(t)
			assert.Empty(t, rows, "provisional accounts are left out")
		}
		w, _ := reportRanked(t, rankedBody(map[string]any{"resultId": fmt.Sprintf("m%d", i)}), botSecret)
		assert.Equal(t, http.StatusOK, w.Code)
	}
	rows := getRatingLeaderboard(t)
	assert.Len(t, rows, 2)
	assert.Equal(t, "name-alice", rows[0].Username)
	assert.Equal(t, "name-bob", rows[1].Username)
	assert.Greater(t, rows[0].Rating, rows[1].Rating)
	assert.Equal(t, models.ProvisionalMatches, rows[0].RankedMatches)
}

func TestRatingLeaderboardOmitsStandIns(t *testing.T) {
	rankedSetup(t)
	for i := 0; i < models.ProvisionalMatches; i++ {
		body := rankedBody(map[string]any{
			"resultId": fmt.Sprintf("s%d", i),
			"b":        map[string]any{"standInId": "rookie", "rating": 1500},
		})
		w, _ := reportRanked(t, body, botSecret)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	rows := getRatingLeaderboard(t)
	assert.Len(t, rows, 1)
	assert.Equal(t, "name-alice", rows[0].Username)
}

func TestRatingLeaderboardEmptyIsList(t *testing.T) {
	freshDB(t)
	w := httptest.NewRecorder()
	RatingLeaderboardHandler(w, httptest.NewRequest("GET", "/rating-leaderboard", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, "[]", w.Body.String())
}
