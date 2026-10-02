package models

import (
	"database/sql"
	"encoding/json"
	"errors"

	glicko "github.com/zelenin/go-glicko2"
)

// ProvisionalMatches is how many Ranked matches an Account needs before its
// Rating stops being Provisional.
const ProvisionalMatches = 5

// standInRD is the fixed rating deviation a Stand-in is given as an opponent.
const standInRD = 50

var (
	ErrUnknownAccount = errors.New("unknown account")
	ErrBadSides       = errors.New("invalid sides")
)

// Rating is an Account's Glicko-2 rating.
type Rating struct {
	UserID        string  `json:"userId,omitempty"`
	Rating        float64 `json:"rating"`
	RD            float64 `json:"rd"`
	Sigma         float64 `json:"-"`
	RankedMatches int     `json:"rankedMatches"`
	Provisional   bool    `json:"provisional"`
}

func defaultRating() Rating {
	d := glicko.NewDefaultRating()
	return Rating{Rating: d.R(), RD: d.Rd(), Sigma: d.Sigma(), Provisional: true}
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func userExists(q queryer, id string) (bool, error) {
	var n int
	if err := q.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsAccount reports whether id belongs to a registered user, as opposed to a Guest.
func IsAccount(id string) (bool, error) { return userExists(DB, id) }

func loadRating(q queryer, id string) (Rating, error) {
	r := Rating{UserID: id}
	err := q.QueryRow("SELECT rating, rd, sigma, ranked_matches FROM ratings WHERE user_id = ?", id).
		Scan(&r.Rating, &r.RD, &r.Sigma, &r.RankedMatches)
	if errors.Is(err, sql.ErrNoRows) {
		r = defaultRating()
		r.UserID = id
		return r, nil
	}
	r.Provisional = r.RankedMatches < ProvisionalMatches
	return r, err
}

// GetRating returns an Account's Rating, the defaults if it never played Ranked.
func GetRating(id string) (Rating, error) {
	ok, err := userExists(DB, id)
	if err != nil {
		return Rating{}, err
	}
	if !ok {
		return Rating{}, ErrUnknownAccount
	}
	return loadRating(DB, id)
}

// Side is one side of a Ranked match: an Account, or a Stand-in with a fixed Rating.
type Side struct {
	AccountID string
	StandInID string
	Rating    float64
}

// RankedOutcome says who won: "a", "b" or "draw".
type RankedOutcome string

type RankedResult struct {
	ResultID string
	A, B     Side
	Outcome  RankedOutcome
	Forfeit  bool
}

type RatingChange struct {
	AccountID     string  `json:"accountId"`
	Before        float64 `json:"ratingBefore"`
	After         float64 `json:"ratingAfter"`
	RankedMatches int     `json:"rankedMatches"`
	Provisional   bool    `json:"provisional"`
}

type RankedResponse struct {
	ResultID string         `json:"resultId"`
	Outcome  RankedOutcome  `json:"outcome"`
	Forfeit  bool           `json:"forfeit"`
	Players  []RatingChange `json:"players"`
}

// RecordRankedResult applies a Ranked match to the Accounts' Ratings. Each match
// is its own rating period. A ResultID already recorded returns the original
// response and changes nothing.
func RecordRankedResult(res RankedResult) (RankedResponse, error) {
	if (res.A.AccountID == "") == (res.A.StandInID == "") ||
		(res.B.AccountID == "") == (res.B.StandInID == "") ||
		(res.A.AccountID != "" && res.A.AccountID == res.B.AccountID) ||
		(res.A.StandInID != "" && res.B.StandInID != "") {
		return RankedResponse{}, ErrBadSides
	}

	tx, err := DB.Begin()
	if err != nil {
		return RankedResponse{}, err
	}
	defer tx.Rollback()

	var stored string
	err = tx.QueryRow("SELECT response FROM ranked_results WHERE result_id = ?", res.ResultID).Scan(&stored)
	if err == nil {
		var prev RankedResponse
		return prev, json.Unmarshal([]byte(stored), &prev)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RankedResponse{}, err
	}

	type entrant struct {
		side   Side
		before Rating
		player *glicko.Player
	}
	entrants := []*entrant{{side: res.A}, {side: res.B}}
	for _, e := range entrants {
		if e.side.AccountID != "" {
			ok, err := userExists(tx, e.side.AccountID)
			if err != nil {
				return RankedResponse{}, err
			}
			if !ok {
				return RankedResponse{}, ErrUnknownAccount
			}
			if e.before, err = loadRating(tx, e.side.AccountID); err != nil {
				return RankedResponse{}, err
			}
			e.player = glicko.NewPlayer(glicko.NewRating(e.before.Rating, e.before.RD, e.before.Sigma))
		} else {
			e.player = glicko.NewPlayer(glicko.NewRating(e.side.Rating, standInRD, glicko.RATING_BASE_SIGMA))
		}
	}

	score := glicko.MATCH_RESULT_DRAW
	switch res.Outcome {
	case "a":
		score = glicko.MATCH_RESULT_WIN
	case "b":
		score = glicko.MATCH_RESULT_LOSS
	}
	period := glicko.NewRatingPeriod()
	period.AddMatch(entrants[0].player, entrants[1].player, score)
	period.Calculate()

	resp := RankedResponse{ResultID: res.ResultID, Outcome: res.Outcome, Forfeit: res.Forfeit, Players: []RatingChange{}}
	for _, e := range entrants {
		if e.side.AccountID == "" {
			continue
		}
		after := e.player.Rating()
		matches := e.before.RankedMatches + 1
		_, err := tx.Exec(`INSERT INTO ratings (user_id, rating, rd, sigma, ranked_matches) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET rating = excluded.rating, rd = excluded.rd,
			sigma = excluded.sigma, ranked_matches = excluded.ranked_matches`,
			e.side.AccountID, after.R(), after.Rd(), after.Sigma(), matches)
		if err != nil {
			return RankedResponse{}, err
		}
		resp.Players = append(resp.Players, RatingChange{
			AccountID: e.side.AccountID, Before: e.before.Rating, After: after.R(),
			RankedMatches: matches, Provisional: matches < ProvisionalMatches,
		})
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return RankedResponse{}, err
	}
	if _, err := tx.Exec("INSERT INTO ranked_results (result_id, response) VALUES (?, ?)", res.ResultID, string(b)); err != nil {
		return RankedResponse{}, err
	}
	return resp, tx.Commit()
}
