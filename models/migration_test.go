package models

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func inTempDir(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	assert.NoError(t, err)
	assert.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() {
		if DB != nil {
			DB.Close()
		}
		os.Chdir(wd)
	})
}

func usersHasColumn(t *testing.T, col string) bool {
	rows, err := DB.Query("PRAGMA table_info(users)")
	assert.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		assert.NoError(t, rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk))
		if name == col {
			return true
		}
	}
	return false
}

func TestInitDBFreshDatabaseHasNoIsAnonymousColumn(t *testing.T) {
	inTempDir(t)
	assert.NoError(t, InitDB())
	assert.False(t, usersHasColumn(t, "is_anonymous"))
}

func TestInitDBPurgesAnonymousUsersAndDropsColumn(t *testing.T) {
	inTempDir(t)
	legacy, err := sql.Open("sqlite3", "./users.db")
	assert.NoError(t, err)
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT UNIQUE, password TEXT, is_anonymous INTEGER DEFAULT 0)`,
		`INSERT INTO users (id, username, password) VALUES ('real', 'alice', 'pw')`,
		`INSERT INTO users (id, is_anonymous) VALUES ('anon', 1)`,
		`CREATE TABLE scores (id TEXT PRIMARY KEY, user_id TEXT, client_id TEXT, score INTEGER NOT NULL, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO scores (id, user_id, score) VALUES ('s1', 'anon', 500)`,
	} {
		_, err := legacy.Exec(stmt)
		assert.NoError(t, err)
	}
	legacy.Close()

	assert.NoError(t, InitDB())
	assert.False(t, usersHasColumn(t, "is_anonymous"))

	var n int
	assert.NoError(t, DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n))
	assert.Equal(t, 1, n)
	u, err := FindByUsername("alice")
	assert.NoError(t, err)
	assert.Equal(t, "real", u.ID)

	// Second boot is a no-op.
	assert.NoError(t, InitDB())
	assert.NoError(t, DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n))
	assert.Equal(t, 1, n)

	// Scores of purged users are left intact and render as Anonymous.
	top, err := GetTopScores(ModeEndless, 10)
	assert.NoError(t, err)
	assert.Len(t, top, 1)
	assert.Equal(t, "Anonymous", top[0].Username)
	assert.Equal(t, 500, top[0].Score)
	var uid sql.NullString
	assert.NoError(t, DB.QueryRow("SELECT user_id FROM scores WHERE id='s1'").Scan(&uid))
	assert.Equal(t, "anon", uid.String)
}

func TestInitDBAddsModeColumnDefaultingToEndless(t *testing.T) {
	inTempDir(t)
	legacy, err := sql.Open("sqlite3", "./users.db")
	assert.NoError(t, err)
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT UNIQUE, password TEXT)`,
		`CREATE TABLE scores (id TEXT PRIMARY KEY, user_id TEXT, client_id TEXT, score INTEGER NOT NULL, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO scores (id, user_id, score) VALUES ('s1', 'u1', 100)`,
		`INSERT INTO scores (id, client_id, score) VALUES ('s2', 'c1', 200)`,
	} {
		_, err := legacy.Exec(stmt)
		assert.NoError(t, err)
	}
	legacy.Close()

	assert.NoError(t, InitDB())
	modes := func() []string {
		rows, err := DB.Query("SELECT mode FROM scores ORDER BY id")
		assert.NoError(t, err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var m string
			assert.NoError(t, rows.Scan(&m))
			out = append(out, m)
		}
		return out
	}
	assert.Equal(t, []string{"endless", "endless"}, modes())

	// Second boot is harmless and keeps the data.
	DB.Close()
	assert.NoError(t, InitDB())
	assert.Equal(t, []string{"endless", "endless"}, modes())
}

func TestInitDBLegacyUserIDNotNullStillGetsMode(t *testing.T) {
	inTempDir(t)
	legacy, err := sql.Open("sqlite3", "./users.db")
	assert.NoError(t, err)
	for _, stmt := range []string{
		`CREATE TABLE scores (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, score INTEGER NOT NULL, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO scores (id, user_id, score) VALUES ('s1', 'u1', 100)`,
	} {
		_, err := legacy.Exec(stmt)
		assert.NoError(t, err)
	}
	legacy.Close()

	assert.NoError(t, InitDB())
	var m string
	assert.NoError(t, DB.QueryRow("SELECT mode FROM scores WHERE id='s1'").Scan(&m))
	assert.Equal(t, "endless", m)
}

func TestModeIsStoredAndFiltersRankings(t *testing.T) {
	inTempDir(t)
	assert.NoError(t, InitDB())
	assert.NoError(t, CreateUser("alice", "pw"))
	alice, err := FindByUsername("alice")
	assert.NoError(t, err)

	assert.NoError(t, AddScore(alice.ID, ModeTimed, 300))
	assert.NoError(t, AddScoreWithClientID(alice.ID, "c1", ModeEndless, 500))
	assert.NoError(t, AddAnonymousScore("c2", ModeTimed, 900))

	timed, err := GetTopScores(ModeTimed, 10)
	assert.NoError(t, err)
	assert.Len(t, timed, 2)
	assert.Equal(t, 900, timed[0].Score)
	assert.Equal(t, 300, timed[1].Score)

	board, err := GetLeaderboard(ModeEndless, 10)
	assert.NoError(t, err)
	assert.Len(t, board, 1)
	assert.Equal(t, 500, board[0].Score)

	mine, err := GetScoresForUser(alice.ID)
	assert.NoError(t, err)
	got := map[int]Mode{}
	for _, s := range mine {
		got[s.Score] = s.Mode
	}
	assert.Equal(t, map[int]Mode{300: ModeTimed, 500: ModeEndless}, got)
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]bool{"timed": true, "endless": true, "": false, "Timed": false, "vs-bot": false} {
		m, ok := ParseMode(in)
		assert.Equal(t, want, ok, in)
		if ok {
			assert.Equal(t, Mode(in), m)
		}
	}
}
