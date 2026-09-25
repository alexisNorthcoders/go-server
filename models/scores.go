package models

import (
	"time"

	"github.com/google/uuid"
)

// Mode is the game mode a score was played in. Scores are only ever ranked
// against scores of the same mode.
type Mode string

const (
	ModeTimed   Mode = "timed"
	ModeEndless Mode = "endless"
)

// ParseMode returns the Mode named by s, and false if s is not a known mode.
func ParseMode(s string) (Mode, bool) {
	switch m := Mode(s); m {
	case ModeTimed, ModeEndless:
		return m, true
	}
	return "", false
}

type Score struct {
	ID        string    `json:"id"`
	UserID    *string   `json:"userId,omitempty"`
	ClientID  *string   `json:"clientId,omitempty"`
	Score     int       `json:"score"`
	Mode      Mode      `json:"mode"`
	Timestamp time.Time `json:"timestamp"`
}

func AddScore(userID string, mode Mode, value int) error {
	id := uuid.New().String()
	_, err := DB.Exec(
		"INSERT INTO scores (id, user_id, score, mode) VALUES (?, ?, ?, ?)",
		id, userID, value, mode,
	)
	return err
}

func AddScoreWithClientID(userID, clientID string, mode Mode, value int) error {
	id := uuid.New().String()
	_, err := DB.Exec(
		"INSERT INTO scores (id, user_id, client_id, score, mode) VALUES (?, ?, ?, ?, ?)",
		id, userID, clientID, value, mode,
	)
	return err
}

func AddAnonymousScore(clientID string, mode Mode, value int) error {
	id := uuid.New().String()
	_, err := DB.Exec(
		"INSERT INTO scores (id, client_id, score, mode) VALUES (?, ?, ?, ?)",
		id, clientID, value, mode,
	)
	return err
}

func GetScoresForUser(userID string) ([]Score, error) {
	rows, err := DB.Query("SELECT id, user_id, client_id, score, mode, timestamp FROM scores WHERE user_id = ? ORDER BY timestamp DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []Score
	for rows.Next() {
		var s Score
		err := rows.Scan(&s.ID, &s.UserID, &s.ClientID, &s.Score, &s.Mode, &s.Timestamp)
		if err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, nil
}

func GetAnonymousScoresForClient(clientID string) ([]Score, error) {
	rows, err := DB.Query("SELECT id, user_id, client_id, score, mode, timestamp FROM scores WHERE client_id = ? ORDER BY timestamp DESC", clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []Score
	for rows.Next() {
		var s Score
		err := rows.Scan(&s.ID, &s.UserID, &s.ClientID, &s.Score, &s.Mode, &s.Timestamp)
		if err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, nil
}

type HighScore struct {
	Username  string    `json:"username"`
	Score     int       `json:"score"`
	Timestamp time.Time `json:"timestamp"`
}

func GetTopScores(mode Mode, limit int) ([]HighScore, error) {
	rows, err := DB.Query(`
	SELECT COALESCE(u.username, 'Anonymous') AS username, s.score, s.timestamp
	FROM scores s
	LEFT JOIN users u ON s.user_id = u.id
	WHERE s.mode = ?
	ORDER BY s.score DESC, s.timestamp DESC
	LIMIT ?
`, mode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []HighScore
	for rows.Next() {
		var hs HighScore
		err := rows.Scan(&hs.Username, &hs.Score, &hs.Timestamp)
		if err != nil {
			return nil, err
		}
		scores = append(scores, hs)
	}
	return scores, nil
}

func GetLeaderboard(mode Mode, limit int) ([]HighScore, error) {
	rows, err := DB.Query(`
		SELECT u.username, s.score, s.timestamp
		FROM scores s
		JOIN users u ON s.user_id = u.id
		WHERE s.user_id IS NOT NULL AND u.username IS NOT NULL AND s.mode = ?
		ORDER BY s.score DESC, s.timestamp DESC
		LIMIT ?
	`, mode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []HighScore
	for rows.Next() {
		var hs HighScore
		err := rows.Scan(&hs.Username, &hs.Score, &hs.Timestamp)
		if err != nil {
			return nil, err
		}
		scores = append(scores, hs)
	}
	return scores, nil
}

func MigrateScores(clientID, userID string) (int64, error) {
	result, err := DB.Exec(
		"UPDATE scores SET user_id = ?, client_id = NULL WHERE client_id = ?",
		userID, clientID,
	)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return count, err
}
