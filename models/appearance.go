package models

import (
	"database/sql"
	"errors"
)

var ErrAppearanceNotFound = errors.New("appearance not found")

func GetAppearance(userID string) ([]byte, error) {
	var data string
	err := DB.QueryRow("SELECT data FROM appearances WHERE user_id = ?", userID).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, ErrAppearanceNotFound
	}
	if err != nil {
		return nil, err
	}
	return []byte(data), nil
}

func SaveAppearance(userID string, data []byte) error {
	_, err := DB.Exec(`
		INSERT INTO appearances (user_id, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET data = excluded.data, updated_at = CURRENT_TIMESTAMP`,
		userID, string(data),
	)
	return err
}
