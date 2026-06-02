package main

import (
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

// UserPrefs holds the per-user settings stored in the user_prefs table.
type UserPrefs struct {
	Level  string
	Paused bool
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

// ---------------------------------------------------------------------------
// user_prefs store methods (Changes F + H)
// ---------------------------------------------------------------------------

// GetPrefs returns the user's preferences, applying defaults when no row exists.
func (s *Store) GetPrefs(chatID int64) (UserPrefs, error) {
	prefs := UserPrefs{Level: defaultLevel, Paused: false}
	var level string
	var paused int
	err := s.db.QueryRow(
		"SELECT level, paused FROM user_prefs WHERE chat_id = ?", chatID,
	).Scan(&level, &paused)
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
