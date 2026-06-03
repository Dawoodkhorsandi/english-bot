package main

import (
	"fmt"
	"log"
	"strings"
)

// Difficulty levels (Change F). The level is injected into generation prompts
// and used to partition the content pool.
const (
	levelBeginner     = "beginner"
	levelIntermediate = "intermediate"
	levelAdvanced     = "advanced"

	defaultLevel = levelIntermediate
)

// allLevels is the ordered set of selectable levels.
var allLevels = []string{levelBeginner, levelIntermediate, levelAdvanced}

// Send interval (Change L). Users choose how often scheduled drills/words arrive.
// Values are kept as multiples of the 30-minute base scheduler tick so the
// wall-clock alignment math stays exact.
const defaultInterval = 30

// allIntervals is the ordered set of selectable send intervals, in minutes.
var allIntervals = []int{30, 60, 120, 180, 240, 360, 480, 720}

// UserPrefs holds the per-user settings stored in the user_prefs table.
type UserPrefs struct {
	Level    string
	Paused   bool
	Interval int // minutes between scheduled sends
}

// normalizeLevel lowercases/trims a level string and validates it, falling back
// to the default when the input is empty or unrecognized.
func normalizeLevel(level string) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	for _, l := range allLevels {
		if l == level {
			return level, true
		}
	}
	return defaultLevel, false
}

// levelLabel returns a human-friendly, capitalized label for a level.
func levelLabel(level string) string {
	switch level {
	case levelBeginner:
		return "Beginner"
	case levelAdvanced:
		return "Advanced"
	default:
		return "Intermediate"
	}
}

// normalizeInterval validates a requested interval (in minutes), falling back to
// the default when the value is not one of the allowed options.
func normalizeInterval(minutes int) (int, bool) {
	for _, m := range allIntervals {
		if m == minutes {
			return minutes, true
		}
	}
	return defaultInterval, false
}

// intervalLabel returns a human-friendly label for an interval in minutes.
func intervalLabel(minutes int) string {
	switch {
	case minutes%60 == 0 && minutes >= 60:
		h := minutes / 60
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		return fmt.Sprintf("%d minutes", minutes)
	}
}

// ---------------------------------------------------------------------------
// user_prefs store methods (Changes F + H + L)
// ---------------------------------------------------------------------------

// GetPrefs returns the user's preferences, applying defaults when no row exists.
func (s *Store) GetPrefs(chatID int64) (UserPrefs, error) {
	prefs := UserPrefs{Level: defaultLevel, Paused: false, Interval: defaultInterval}
	var level string
	var paused int
	var interval int
	err := s.db.QueryRow(
		"SELECT level, paused, interval_minutes FROM user_prefs WHERE chat_id = ?", chatID,
	).Scan(&level, &paused, &interval)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return prefs, nil
		}
		return prefs, err
	}
	if l, ok := normalizeLevel(level); ok {
		prefs.Level = l
	}
	prefs.Paused = paused != 0
	if iv, ok := normalizeInterval(interval); ok {
		prefs.Interval = iv
	}
	return prefs, nil
}

// GetLevel returns just the user's difficulty level (default when unset).
func (s *Store) GetLevel(chatID int64) string {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load level for chat %d: %v (using default)", chatID, err)
		return defaultLevel
	}
	return prefs.Level
}

// SetLevel upserts the user's difficulty level.
func (s *Store) SetLevel(chatID int64, level string) error {
	level, _ = normalizeLevel(level)
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, level) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET level = excluded.level, updated_at = CURRENT_TIMESTAMP`,
		chatID, level,
	)
	return err
}

// SetPaused upserts the user's paused flag.
func (s *Store) SetPaused(chatID int64, paused bool) error {
	p := 0
	if paused {
		p = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, paused) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET paused = excluded.paused, updated_at = CURRENT_TIMESTAMP`,
		chatID, p,
	)
	return err
}

// IsPaused reports whether the user has paused scheduled sends.
func (s *Store) IsPaused(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load paused flag for chat %d: %v (assuming active)", chatID, err)
		return false
	}
	return prefs.Paused
}

// GetInterval returns the user's send interval in minutes (default when unset).
func (s *Store) GetInterval(chatID int64) int {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load interval for chat %d: %v (using default)", chatID, err)
		return defaultInterval
	}
	return prefs.Interval
}

// SetInterval upserts the user's send interval (in minutes).
func (s *Store) SetInterval(chatID int64, minutes int) error {
	minutes, _ = normalizeInterval(minutes)
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, interval_minutes) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET interval_minutes = excluded.interval_minutes, updated_at = CURRENT_TIMESTAMP`,
		chatID, minutes,
	)
	return err
}

// ActiveLevels returns the distinct set of levels the pool should keep stocked:
// always the default level, plus any level a user has explicitly selected.
func (s *Store) ActiveLevels() ([]string, error) {
	seen := map[string]bool{defaultLevel: true}
	levels := []string{defaultLevel}

	rows, err := s.db.Query("SELECT DISTINCT level FROM user_prefs")
	if err != nil {
		return levels, err
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return levels, err
		}
		if l, ok := normalizeLevel(l); ok && !seen[l] {
			seen[l] = true
			levels = append(levels, l)
		}
	}
	return levels, rows.Err()
}
