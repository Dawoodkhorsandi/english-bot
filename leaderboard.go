package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
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

// LeaderRow is one ranked entry. ID is an opaque, stable public id (never the
// raw chat_id) so the Mini App can open the user's profile without exposing
// Telegram identities.
type LeaderRow struct {
	Rank  int    `json:"rank"`
	ID    string `json:"id"`
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

// weekStartUTC returns the start of the current ISO week (Monday 00:00 in
// appLocation) formatted like the stored sent_at values (UTC DATETIME).
func weekStartUTC(now time.Time) string {
	local := now.In(appLocation)
	daysBack := (int(local.Weekday()) + 6) % 7 // Monday = 0
	monday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, appLocation).
		AddDate(0, 0, -daysBack)
	return monday.UTC().Format("2006-01-02 15:04:05")
}

// dayStartUTC returns the start of today (00:00 in appLocation) formatted like
// the stored sent_at values (UTC DATETIME).
func dayStartUTC(now time.Time) string {
	local := now.In(appLocation)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, appLocation)
	return midnight.UTC().Format("2006-01-02 15:04:05")
}

// Leaderboard returns the ranked rows for a metric plus the requesting user's own
// rank and value. metric is "mastered" (words in long-term memory), "weekly"
// (words learned since Monday), "today" (words learned since local midnight) or
// "words" (default — total vocabulary learned). Users with a zero value are
// excluded. Profile photos are deliberately not exposed (privacy).
func (s *Store) Leaderboard(metric string, me int64) (rows []LeaderRow, myRank, myValue int, err error) {
	var inner string
	var args []interface{}
	switch metric {
	case "mastered":
		inner = "SELECT chat_id, COUNT(*) AS v FROM review_schedule WHERE interval_days >= ? GROUP BY chat_id"
		args = append(args, srsMasteredIntervalDays)
	case "weekly":
		inner = "SELECT chat_id, COUNT(*) AS v FROM sent_vocab WHERE sent_at >= ? GROUP BY chat_id"
		args = append(args, weekStartUTC(time.Now()))
	case "today":
		inner = "SELECT chat_id, COUNT(*) AS v FROM sent_vocab WHERE sent_at >= ? GROUP BY chat_id"
		args = append(args, dayStartUTC(time.Now()))
	default:
		inner = "SELECT chat_id, COUNT(*) AS v FROM sent_vocab GROUP BY chat_id"
	}

	query := `
		SELECT t.chat_id, t.v, COALESCE(up.display_name, '')
		FROM (` + inner + `) t
		LEFT JOIN user_prefs up ON up.chat_id = t.chat_id
		WHERE t.v > 0
		ORDER BY t.v DESC, t.chat_id ASC`

	qrows, err := s.db.Query(query, args...)
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
			rows = append(rows, LeaderRow{Rank: rank, ID: s.PublicID(chatID), Name: name, Value: value, IsMe: isMe})
		}
	}
	return rows, myRank, myValue, qrows.Err()
}

// ---------------------------------------------------------------------------
// Opaque public ids — addressing a user's profile without exposing chat_id.
// ---------------------------------------------------------------------------

// publicIDFor derives a stable, opaque public id for a chat from the bot token
// (same keyed-HMAC pattern as quizTokenMAC). 16 hex chars (64 bits) — enough to
// avoid collisions, and never reversible to the chat_id.
func publicIDFor(chatID int64) string {
	h1 := hmac.New(sha256.New, []byte("UserPublicId"))
	h1.Write([]byte(TelegramBotToken))
	key := h1.Sum(nil)
	h2 := hmac.New(sha256.New, key)
	fmt.Fprintf(h2, "%d", chatID)
	return hex.EncodeToString(h2.Sum(nil))[:16]
}

// PublicID returns the chat's opaque public id, persisting it (idempotently) so
// reverse lookups hit the indexed user_prefs.public_id column.
func (s *Store) PublicID(chatID int64) string {
	pid := publicIDFor(chatID)
	_, _ = s.db.Exec(`
		INSERT INTO user_prefs (chat_id, public_id) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET public_id = excluded.public_id`,
		chatID, pid,
	)
	return pid
}

// ChatIDByPublicID resolves an opaque public id back to its chat_id (ok=false
// when unknown).
func (s *Store) ChatIDByPublicID(pid string) (int64, bool) {
	if pid == "" {
		return 0, false
	}
	var chatID int64
	if err := s.db.QueryRow("SELECT chat_id FROM user_prefs WHERE public_id = ?", pid).Scan(&chatID); err != nil {
		return 0, false
	}
	return chatID, true
}

// ---------------------------------------------------------------------------
// Kudos — one toggle per (giver, recipient) pair; profile shows total received.
// ---------------------------------------------------------------------------

// kudosCount returns how many distinct users have given kudos to a user.
func (s *Store) kudosCount(to int64) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM kudos WHERE to_chat_id = ?", to).Scan(&n)
	return n, err
}

// ToggleKudos flips the viewer's kudos for a target: gives one if absent, takes
// it back if present. Returns whether kudos is now given and the new total.
func (s *Store) ToggleKudos(from, to int64) (gave bool, count int, err error) {
	if from == to {
		return false, 0, nil
	}
	var x int
	err = s.db.QueryRow("SELECT 1 FROM kudos WHERE from_chat_id = ? AND to_chat_id = ?", from, to).Scan(&x)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.Exec("INSERT INTO kudos (from_chat_id, to_chat_id) VALUES (?, ?)", from, to)
		gave = true
	case err == nil:
		_, err = s.db.Exec("DELETE FROM kudos WHERE from_chat_id = ? AND to_chat_id = ?", from, to)
		gave = false
	}
	if err != nil {
		return gave, 0, err
	}
	count, err = s.kudosCount(to)
	return gave, count, err
}

// KudosFor returns the target's total kudos and whether the viewer has given one.
func (s *Store) KudosFor(viewer, target int64) (count int, gaveByMe bool, err error) {
	count, err = s.kudosCount(target)
	if err != nil {
		return 0, false, err
	}
	var x int
	gaveByMe = s.db.QueryRow("SELECT 1 FROM kudos WHERE from_chat_id = ? AND to_chat_id = ?", viewer, target).Scan(&x) == nil
	return count, gaveByMe, nil
}
