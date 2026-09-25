package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go-server/models"
	"go-server/utils"
)

// freshDB points models.DB at an empty database for the duration of the test,
// so ranking assertions are not skewed by scores left by other tests.
func freshDB(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	assert.NoError(t, err)
	models.DB.Close()
	assert.NoError(t, os.Chdir(t.TempDir()))
	assert.NoError(t, models.InitDB())
	t.Cleanup(func() {
		models.DB.Close()
		os.Chdir(wd)
		models.InitDB()
	})
}

func scoreCount(t *testing.T) int {
	var n int
	assert.NoError(t, models.DB.QueryRow("SELECT COUNT(*) FROM scores").Scan(&n))
	return n
}

func postJSON(path string, body any, token string) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestSavingStoresRequestedMode(t *testing.T) {
	freshDB(t)
	token, _ := utils.GenerateToken("mode-user", "mode-user-id")

	for _, mode := range []models.Mode{models.ModeTimed, models.ModeEndless} {
		w := httptest.NewRecorder()
		PostAuthenticatedScoreHandler(w, postJSON("/scores", AuthenticatedScoreRequest{ClientID: "c", Score: 10, Mode: string(mode)}, token))
		assert.Equal(t, http.StatusOK, w.Code, mode)

		w = httptest.NewRecorder()
		AddScoreHandler(w, postJSON("/add-score", map[string]any{"score": 20, "mode": mode}, token))
		assert.Equal(t, http.StatusOK, w.Code, mode)

		w = httptest.NewRecorder()
		PostAnonymousScoreHandler(w, postJSON("/scores/anonymous", AnonymousScoreRequest{ClientID: "anon-" + string(mode), Score: 30, Mode: string(mode)}, ""))
		assert.Equal(t, http.StatusOK, w.Code, mode)

		anon, err := models.GetAnonymousScoresForClient("anon-" + string(mode))
		assert.NoError(t, err)
		assert.Len(t, anon, 1)
		assert.Equal(t, mode, anon[0].Mode)
	}

	mine, err := models.GetScoresForUser("mode-user-id")
	assert.NoError(t, err)
	counts := map[models.Mode]int{}
	for _, s := range mine {
		counts[s.Mode]++
	}
	assert.Equal(t, map[models.Mode]int{models.ModeTimed: 2, models.ModeEndless: 2}, counts)
}

func TestSavingWithoutValidModeIsRejected(t *testing.T) {
	freshDB(t)
	token, _ := utils.GenerateToken("mode-user", "mode-user-id")

	for _, mode := range []string{"", "Timed", "vs-bot"} {
		w := httptest.NewRecorder()
		PostAuthenticatedScoreHandler(w, postJSON("/scores", AuthenticatedScoreRequest{ClientID: "c", Score: 10, Mode: mode}, token))
		assert.Equal(t, http.StatusBadRequest, w.Code, mode)

		w = httptest.NewRecorder()
		AddScoreHandler(w, postJSON("/add-score", map[string]any{"score": 20, "mode": mode}, token))
		assert.Equal(t, http.StatusBadRequest, w.Code, mode)

		w = httptest.NewRecorder()
		PostAnonymousScoreHandler(w, postJSON("/scores/anonymous", AnonymousScoreRequest{ClientID: "anon", Score: 30, Mode: mode}, ""))
		assert.Equal(t, http.StatusBadRequest, w.Code, mode)
	}
	assert.Equal(t, 0, scoreCount(t))
}

func TestRankingsReturnOnlyRequestedMode(t *testing.T) {
	freshDB(t)
	assert.NoError(t, models.CreateUser("alice", "pw"))
	alice, _ := models.FindByUsername("alice")
	assert.NoError(t, models.AddScore(alice.ID, models.ModeTimed, 100))
	assert.NoError(t, models.AddScore(alice.ID, models.ModeEndless, 200))
	assert.NoError(t, models.AddAnonymousScore("anon", models.ModeTimed, 300))

	get := func(handler http.HandlerFunc, url string) (int, []models.HighScore) {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", url, nil))
		var out []models.HighScore
		if w.Code == http.StatusOK {
			assert.NoError(t, json.NewDecoder(w.Body).Decode(&out))
		}
		return w.Code, out
	}
	scoresOf := func(hs []models.HighScore) []int {
		var out []int
		for _, h := range hs {
			out = append(out, h.Score)
		}
		return out
	}

	code, hs := get(HighScoresHandler, "/high-scores?mode=timed")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []int{300, 100}, scoresOf(hs))
	code, hs = get(HighScoresHandler, "/high-scores?mode=endless")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []int{200}, scoresOf(hs))

	code, hs = get(LeaderboardHandler, "/leaderboard?mode=timed")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []int{100}, scoresOf(hs))
	code, hs = get(LeaderboardHandler, "/leaderboard?mode=endless")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []int{200}, scoresOf(hs))

	for _, q := range []string{"", "?mode=", "?mode=bogus"} {
		code, _ = get(HighScoresHandler, "/high-scores"+q)
		assert.Equal(t, http.StatusBadRequest, code, q)
		code, _ = get(LeaderboardHandler, "/leaderboard"+q)
		assert.Equal(t, http.StatusBadRequest, code, q)
	}
}

func TestScoreListsIncludeMode(t *testing.T) {
	freshDB(t)
	assert.NoError(t, models.AddScore("list-user", models.ModeTimed, 1))
	assert.NoError(t, models.AddAnonymousScore("list-client", models.ModeEndless, 2))

	w := httptest.NewRecorder()
	GetUserScoresWithPathHandler(w, httptest.NewRequest("GET", "/scores/list-user", nil), "list-user")
	assert.Contains(t, w.Body.String(), `"mode":"timed"`)

	w = httptest.NewRecorder()
	GetAnonymousScoresHandler(w, httptest.NewRequest("GET", "/scores/anonymous/list-client", nil), "list-client")
	assert.Contains(t, w.Body.String(), `"mode":"endless"`)
}

func TestMigratingAnonymousScoresKeepsMode(t *testing.T) {
	freshDB(t)
	assert.NoError(t, models.AddAnonymousScore("mig-client", models.ModeTimed, 10))
	assert.NoError(t, models.AddAnonymousScore("mig-client", models.ModeEndless, 20))
	token, _ := utils.GenerateToken("mig", "mig-user")

	w := httptest.NewRecorder()
	MigrateScoresHandler(w, postJSON("/scores/migrate/mig-client", MigrateScoresRequest{UserID: "mig-user"}, token), "mig-client")
	assert.Equal(t, http.StatusOK, w.Code)

	mine, err := models.GetScoresForUser("mig-user")
	assert.NoError(t, err)
	got := map[int]models.Mode{}
	for _, s := range mine {
		got[s.Score] = s.Mode
	}
	assert.Equal(t, map[int]models.Mode{10: models.ModeTimed, 20: models.ModeEndless}, got)
}
