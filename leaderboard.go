package main

import (
	"database/sql"
	"strings"
)

// ---------------------------------------------------------------------------
// Leaderboard — cross-user ranking for the Mini App.
//
// Users are ranked by a metric (words learned or words mastered). Each user
// chooses a display name on first open; if they skip, a stable, friendly random
// name is shown instead — never a raw Telegram name, and never "Anonymous".
// ---------------------------------------------------------------------------

const (
	// maxDisplayNameLen bounds a user-chosen leaderboard name.
	maxDisplayNameLen = 24
	// leaderboardSize is how many ranked rows the API returns for display.
	leaderboardSize = 50
)

// LeaderRow is one ranked entry.
type LeaderRow struct {
	Rank  int    `json:"rank"`
	Name  string `json:"name"`
	Value int    `json:"value"`
	IsMe  bool   `json:"isMe"`
}

// funnyAdjectives / funnyNouns build a stable nickname for users who haven't
// chosen a display name. Indexed deterministically by chat ID.
var funnyAdjectives = []string{
	"Brave", "Witty", "Mighty", "Sneaky", "Jolly", "Clever", "Swift", "Cosmic",
	"Sleepy", "Dapper", "Fuzzy", "Bold", "Curious", "Lucky", "Zen", "Turbo",
}

var funnyNouns = []string{
	"Otter", "Falcon", "Panda", "Wombat", "Penguin", "Fox", "Llama", "Narwhal",
	"Badger", "Koala", "Gecko", "Walrus", "Hedgehog", "Raccoon", "Moose", "Dolphin",
}

// funnyName returns a deterministic friendly nickname for a chat ID.
func funnyName(chatID int64) string {
	n := chatID
	if n < 0 {
		n = -n
	}
	adj := funnyAdjectives[n%int64(len(funnyAdjectives))]
	noun := funnyNouns[(n/int64(len(funnyAdjectives)))%int64(len(funnyNouns))]
	return adj + " " + noun
}

// sanitizeDisplayName trims, strips control characters and HTML angle brackets,
// and caps the length of a user-supplied leaderboard name.
func sanitizeDisplayName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.NewReplacer("<", "", ">", "", "\n", " ", "\r", " ", "\t", " ").Replace(name)
	name = strings.Join(strings.Fields(name), " ") // collapse runs of spaces
	if len(name) > maxDisplayNameLen {
		name = strings.TrimSpace(name[:maxDisplayNameLen])
	}
	return name
}

// GetDisplayName returns the user's chosen leaderboard name, or "" if unset.
func (s *Store) GetDisplayName(chatID int64) string {
	var name string
	_ = s.db.QueryRow(
		"SELECT COALESCE(display_name, '') FROM user_prefs WHERE chat_id = ?", chatID,
	).Scan(&name)
	return name
}

// SetDisplayName stores the user's chosen leaderboard name.
func (s *Store) SetDisplayName(chatID int64, name string) error {
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, display_name) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET display_name = excluded.display_name, updated_at = CURRENT_TIMESTAMP`,
		chatID, name,
	)
	return err
}

// Leaderboard returns the ranked rows for a metric plus the requesting user's own
// rank and value. metric is "mastered" (words in long-term memory) or "words"
// (default — total vocabulary learned). Users with a zero value are excluded.
func (s *Store) Leaderboard(metric string, me int64) (rows []LeaderRow, myRank, myValue int, err error) {
	var inner string
	switch metric {
	case "mastered":
		inner = "SELECT chat_id, COUNT(*) AS v FROM review_schedule WHERE interval_days >= ? GROUP BY chat_id"
	default:
		inner = "SELECT chat_id, COUNT(*) AS v FROM sent_vocab GROUP BY chat_id"
	}

	query := `
		SELECT t.chat_id, t.v, COALESCE(up.display_name, '')
		FROM (` + inner + `) t
		LEFT JOIN user_prefs up ON up.chat_id = t.chat_id
		WHERE t.v > 0
		ORDER BY t.v DESC, t.chat_id ASC`

	var qrows *sql.Rows
	if metric == "mastered" {
		qrows, err = s.db.Query(query, srsMasteredIntervalDays)
	} else {
		qrows, err = s.db.Query(query)
	}
	if err != nil {
		return nil, 0, 0, err
	}
	defer qrows.Close()

	rank := 0
	for qrows.Next() {
		var (
			chatID  int64
			value   int
			display string
		)
		if err := qrows.Scan(&chatID, &value, &display); err != nil {
			return nil, 0, 0, err
		}
		rank++
		isMe := chatID == me
		if isMe {
			myRank = rank
			myValue = value
		}
		name := display
		if name == "" {
			name = funnyName(chatID)
		}
		if rank <= leaderboardSize {
			rows = append(rows, LeaderRow{Rank: rank, Name: name, Value: value, IsMe: isMe})
		}
	}
	return rows, myRank, myValue, qrows.Err()
}
