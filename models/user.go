package models

import (
	"database/sql"
	"errors"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

type User struct {
	ID       string
	Username string
	Password string
}

func InitDB() error {
	var err error
	DB, err = sql.Open("sqlite3", "./users.db")
	if err != nil {
		return err
	}

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT UNIQUE,
		password TEXT,
		is_anonymous INTEGER DEFAULT 0
	);`
	if _, err = DB.Exec(createUsersTable); err != nil {
		return err
	}

	createScoresTable := `
	CREATE TABLE IF NOT EXISTS scores (
		id TEXT PRIMARY KEY,
		user_id TEXT,
		client_id TEXT,
		score INTEGER NOT NULL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);`
	if _, err = DB.Exec(createScoresTable); err != nil {
		return err
	}

	createAppearancesTable := `
	CREATE TABLE IF NOT EXISTS appearances (
		user_id TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);`
	if _, err = DB.Exec(createAppearancesTable); err != nil {
		return err
	}

	// Add client_id column if it doesn't exist (for existing databases)
	addClientIDColumn := `ALTER TABLE scores ADD COLUMN client_id TEXT;`
	DB.Exec(addClientIDColumn) // Ignore error if column already exists

	return relaxScoresUserIDNotNull()
}

// relaxScoresUserIDNotNull rebuilds the scores table when an older database
// declared user_id NOT NULL, which rejects anonymous scores (NULL user_id).
func relaxScoresUserIDNotNull() error {
	var notNull bool
	rows, err := DB.Query("PRAGMA table_info(scores)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, nn, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &nn, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "user_id" && nn == 1 {
			notNull = true
		}
	}
	rows.Close()
	if !notNull {
		return nil
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE scores_new (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			client_id TEXT,
			score INTEGER NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`INSERT INTO scores_new (id, user_id, client_id, score, timestamp)
			SELECT id, user_id, client_id, score, timestamp FROM scores`,
		`DROP TABLE scores`,
		`ALTER TABLE scores_new RENAME TO scores`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func CreateUser(username, hashedPassword string) error {
	id := uuid.New().String()
	_, err := DB.Exec("INSERT INTO users (id, username, password) VALUES (?, ?, ?)", id, username, hashedPassword)
	return err
}

func FindByUsername(username string) (User, error) {
	row := DB.QueryRow("SELECT id, username, password FROM users WHERE username = ?", username)
	var user User
	err := row.Scan(&user.ID, &user.Username, &user.Password)
	if err == sql.ErrNoRows {
		return User{}, errors.New("user not found")
	}
	return user, err
}

func AllUsers() ([]User, error) {
	rows, err := DB.Query("SELECT id, username, password FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		err := rows.Scan(&user.ID, &user.Username, &user.Password)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func CreateAnonymousUser(id string) error {
	_, err := DB.Exec("INSERT INTO users (id, is_anonymous) VALUES (?, 1)", id)
	return err
}
