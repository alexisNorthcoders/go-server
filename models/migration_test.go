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
	top, err := GetTopScores(10)
	assert.NoError(t, err)
	assert.Len(t, top, 1)
	assert.Equal(t, "Anonymous", top[0].Username)
	assert.Equal(t, 500, top[0].Score)
	var uid sql.NullString
	assert.NoError(t, DB.QueryRow("SELECT user_id FROM scores WHERE id='s1'").Scan(&uid))
	assert.Equal(t, "anon", uid.String)
}
