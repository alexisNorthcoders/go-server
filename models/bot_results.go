package models

import "regexp"

// Outcome is how a vs-bot round ended, from the bot's side.
type Outcome string

const (
	OutcomeWin  Outcome = "win"
	OutcomeLoss Outcome = "loss"
	OutcomeDraw Outcome = "draw"
)

// ParseOutcome returns the Outcome named by s, and false if s is not known.
func ParseOutcome(s string) (Outcome, bool) {
	switch o := Outcome(s); o {
	case OutcomeWin, OutcomeLoss, OutcomeDraw:
		return o, true
	}
	return "", false
}

var botIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// ValidBotID reports whether s is a short slug. go-server does not know the
// roster, so this is the only check made on a bot id.
func ValidBotID(s string) bool {
	return botIDPattern.MatchString(s)
}

type BotResult struct {
	ResultID string
	BotID    string
	Mode     Mode
	Delay    int
	Outcome  Outcome
}

// AddBotResult stores a result. A result whose ResultID is already stored is
// ignored, and that is not an error.
func AddBotResult(r BotResult) error {
	_, err := DB.Exec(
		"INSERT OR IGNORE INTO bot_results (result_id, bot_id, mode, delay, outcome) VALUES (?, ?, ?, ?, ?)",
		r.ResultID, r.BotID, r.Mode, r.Delay, r.Outcome,
	)
	return err
}

type BotRecord struct {
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Draws  int `json:"draws"`
}

// GetBotRecords totals results per bot and mode. A non-empty botID narrows it
// to that bot. Bots and modes without results are absent.
func GetBotRecords(botID string) (map[string]map[Mode]BotRecord, error) {
	query := `SELECT bot_id, mode,
		COALESCE(SUM(outcome = 'win'), 0),
		COALESCE(SUM(outcome = 'loss'), 0),
		COALESCE(SUM(outcome = 'draw'), 0)
		FROM bot_results`
	var args []any
	if botID != "" {
		query += " WHERE bot_id = ?"
		args = append(args, botID)
	}
	query += " GROUP BY bot_id, mode"

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := map[string]map[Mode]BotRecord{}
	for rows.Next() {
		var id string
		var mode Mode
		var rec BotRecord
		if err := rows.Scan(&id, &mode, &rec.Wins, &rec.Losses, &rec.Draws); err != nil {
			return nil, err
		}
		if records[id] == nil {
			records[id] = map[Mode]BotRecord{}
		}
		records[id][mode] = rec
	}
	return records, rows.Err()
}
