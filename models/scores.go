package models

import (
	"time"

	"github.com/google/uuid"
)

type Score struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Score     int       `json:"score"`
	Timestamp time.Time `json:"timestamp"`
}

func AddScore(userID string, value int) error {
	id := uuid.New().String()
	_, err := DB.Exec(
		"INSERT INTO scores (id, user_id, score) VALUES (?, ?, ?)",
		id, userID, value,
	)
	return err
}

func GetScoresForUser(userID string) ([]Score, error) {
	rows, err := DB.Query("SELECT id, user_id, score, timestamp FROM scores WHERE user_id = ? ORDER BY timestamp DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []Score
	for rows.Next() {
		var s Score
		err := rows.Scan(&s.ID, &s.UserID, &s.Score, &s.Timestamp)
		if err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, nil
}
