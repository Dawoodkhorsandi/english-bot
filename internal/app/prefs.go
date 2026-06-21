package app

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// Send interval (Change L). Users choose how often scheduled drills/words arrive.
// Values are kept as multiples of the 30-minute base scheduler tick so the
// wall-clock alignment math stays exact.
// Default is 60 min (one broadcast per hour). The global hourly rate limiter
// further ensures no other scheduler adds a second message in the same hour.
const defaultInterval = 60

// allIntervals is the ordered set of selectable send intervals, in minutes.
var allIntervals = []int{30, 60, 120, 180, 240, 360, 480, 720}

// Quiz interval. Users choose how often scheduled quizzes arrive (in hours).
const defaultQuizIntervalHours = 6

// allQuizIntervalHours is the ordered set of selectable quiz intervals.
var allQuizIntervalHours = []int{3, 6, 12, 24}

// UserPrefs holds the per-user settings stored in the user_prefs table.
type UserPrefs struct {
	Level              string
	Paused             bool
	Interval           int  // minutes between scheduled broadcasts
	TTSEnabled         bool // pronunciation voice messages
	TipsEnabled        bool // scheduled daily grammar tips
	QuizEnabled        bool // scheduled quizzes
	IdiomEnabled       bool // daily idiom of the day
	CollocationEnabled bool // daily collocation of the day
	StoryEnabled       bool // daily mini story
	ReviewEnabled      bool // SRS spaced-repetition memory checks
	DigestEnabled      bool // weekly digest
	DailyReviewEnabled bool // midnight vocab recap
	QuizIntervalHours  int  // hours between scheduled quizzes
	FirstName          string
	StreakCelebrated   int     // highest streak milestone already celebrated
	DesiredRetention   float64 // FSRS target recall probability (0.70–0.97)
	StreakFreezes      int     // streak-saver tokens banked
	ExamTarget         string  // "", "ielts", or "toefl"
	Onboarded          int     // 1 once the known-word onboarding has been completed
}

// normalizeLevel lowercases/trims a level string and validates it, falling back
// to the default when the input is empty or unrecognized.
func normalizeLevel(level string) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	for _, l := range config.AllLevels {
		if l == level {
			return level, true
		}
	}
	return config.DefaultLevel, false
}

// levelLabel returns a human-friendly, capitalized label for a level.
func levelLabel(level string) string {
	switch level {
	case config.LevelBeginner:
		return "Beginner"
	case config.LevelUpperInt:
		return "Upper-Intermediate"
	case config.LevelAdvanced:
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

// normalizeQuizIntervalHours validates a requested quiz interval (in hours).
func normalizeQuizIntervalHours(hours int) (int, bool) {
	for _, h := range allQuizIntervalHours {
		if h == hours {
			return hours, true
		}
	}
	return defaultQuizIntervalHours, false
}

// quizIntervalLabel returns a human-friendly label for a quiz interval in hours.
func quizIntervalLabel(hours int) string {
	if hours == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", hours)
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
// user_prefs store methods (Changes F + H + I + L)
// ---------------------------------------------------------------------------

// GetPrefs returns the user's preferences, applying defaults when no row exists.
func (s *Store) GetPrefs(chatID int64) (UserPrefs, error) {
	prefs := UserPrefs{
		Level: config.DefaultLevel, Paused: false, Interval: defaultInterval,
		TTSEnabled: true, TipsEnabled: true,
		QuizEnabled: true, IdiomEnabled: true, ReviewEnabled: true,
		CollocationEnabled: true, StoryEnabled: true,
		DigestEnabled: true, DailyReviewEnabled: true,
		QuizIntervalHours: defaultQuizIntervalHours,
		DesiredRetention:  defaultRetention,
	}
	var level, firstName string
	var paused, interval, ttsEnabled, tipsEnabled int
	var quizEnabled, idiomEnabled, reviewEnabled, digestEnabled, dailyReviewEnabled int
	var collocationEnabled, storyEnabled int
	var quizIntervalHours, streakCelebrated int
	var desiredRetention float64
	var streakFreezes, onboarded int
	var examTarget string
	err := s.db.QueryRow(
		`SELECT level, paused, interval_minutes, tts_enabled, tips_enabled,
		        quiz_enabled, idiom_enabled, review_enabled, digest_enabled,
		        daily_review_enabled, quiz_interval_hours,
		        COALESCE(collocation_enabled,1), COALESCE(story_enabled,1),
		        COALESCE(first_name,''), COALESCE(streak_celebrated,0),
		        COALESCE(desired_retention,0.9), COALESCE(streak_freezes,0),
		        COALESCE(exam_target,''), COALESCE(onboarded,0)
		 FROM user_prefs WHERE chat_id = ?`, chatID,
	).Scan(&level, &paused, &interval, &ttsEnabled, &tipsEnabled,
		&quizEnabled, &idiomEnabled, &reviewEnabled, &digestEnabled,
		&dailyReviewEnabled, &quizIntervalHours,
		&collocationEnabled, &storyEnabled,
		&firstName, &streakCelebrated, &desiredRetention,
		&streakFreezes, &examTarget, &onboarded)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	prefs.TTSEnabled = ttsEnabled != 0
	prefs.TipsEnabled = tipsEnabled != 0
	prefs.QuizEnabled = quizEnabled != 0
	prefs.IdiomEnabled = idiomEnabled != 0
	prefs.ReviewEnabled = reviewEnabled != 0
	prefs.CollocationEnabled = collocationEnabled != 0
	prefs.StoryEnabled = storyEnabled != 0
	prefs.DigestEnabled = digestEnabled != 0
	prefs.DailyReviewEnabled = dailyReviewEnabled != 0
	if qh, ok := normalizeQuizIntervalHours(quizIntervalHours); ok {
		prefs.QuizIntervalHours = qh
	}
	prefs.FirstName = firstName
	prefs.StreakCelebrated = streakCelebrated
	prefs.DesiredRetention = normalizeRetention(desiredRetention)
	prefs.StreakFreezes = streakFreezes
	prefs.ExamTarget = normalizeExamTarget(examTarget)
	prefs.Onboarded = onboarded
	return prefs, nil
}

// ---------------------------------------------------------------------------
// Streak-saver freezes (#7) — banked tokens that auto-rescue a missed day.
// ---------------------------------------------------------------------------

// GetStreakFreezes returns how many streak-saver tokens the user has banked.
func (s *Store) GetStreakFreezes(chatID int64) int {
	var n int
	_ = s.db.QueryRow("SELECT COALESCE(streak_freezes,0) FROM user_prefs WHERE chat_id = ?", chatID).Scan(&n)
	return n
}

// AddStreakFreezes grants (or, with a negative delta, spends) streak-saver
// tokens, never letting the balance drop below zero. Returns the new balance.
func (s *Store) AddStreakFreezes(chatID int64, delta int) (int, error) {
	if _, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, streak_freezes) VALUES (?, MAX(0, ?))
		ON CONFLICT(chat_id) DO UPDATE SET
			streak_freezes = MAX(0, COALESCE(streak_freezes,0) + ?),
			updated_at = CURRENT_TIMESTAMP`,
		chatID, delta, delta,
	); err != nil {
		return 0, err
	}
	return s.GetStreakFreezes(chatID), nil
}

// ---------------------------------------------------------------------------
// Exam target (#1) — IELTS / TOEFL focus.
// ---------------------------------------------------------------------------

// allExamTargets enumerates the supported exam tracks ("" = none).
var allExamTargets = []string{"", "ielts", "toefl"}

// normalizeExamTarget validates an exam-target string, defaulting to "" (none).
func normalizeExamTarget(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	for _, v := range allExamTargets {
		if v == t {
			return t
		}
	}
	return ""
}

// examTargetLabel returns a human label for an exam target.
func examTargetLabel(t string) string {
	switch normalizeExamTarget(t) {
	case "ielts":
		return "IELTS"
	case "toefl":
		return "TOEFL"
	default:
		return "None"
	}
}

// GetExamTarget returns the user's exam track ("" when none).
func (s *Store) GetExamTarget(chatID int64) string {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		return ""
	}
	return prefs.ExamTarget
}

// SetExamTarget upserts the user's exam track.
func (s *Store) SetExamTarget(chatID int64, target string) error {
	target = normalizeExamTarget(target)
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, exam_target) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET exam_target = excluded.exam_target, updated_at = CURRENT_TIMESTAMP`,
		chatID, target,
	)
	return err
}

// IsOnboarded reports whether the user has completed known-word onboarding (#8).
func (s *Store) IsOnboarded(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		return false
	}
	return prefs.Onboarded != 0
}

// SetOnboarded marks the known-word onboarding as completed.
func (s *Store) SetOnboarded(chatID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, onboarded) VALUES (?, 1)
		ON CONFLICT(chat_id) DO UPDATE SET onboarded = 1, updated_at = CURRENT_TIMESTAMP`,
		chatID,
	)
	return err
}

// GetDesiredRetention returns the user's FSRS target recall probability
// (default 0.9 when unset).
func (s *Store) GetDesiredRetention(chatID int64) float64 {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load desired_retention for chat %d: %v (using default)", chatID, err)
		return defaultRetention
	}
	return prefs.DesiredRetention
}

// SetDesiredRetention upserts the user's FSRS target recall probability,
// clamped to the supported range.
func (s *Store) SetDesiredRetention(chatID int64, r float64) error {
	r = normalizeRetention(r)
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, desired_retention) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET desired_retention = excluded.desired_retention, updated_at = CURRENT_TIMESTAMP`,
		chatID, r,
	)
	return err
}

// SetFirstName stores the user's Telegram first name (best-effort, updated on
// every inbound message so it stays current without a dedicated DB lookup).
func (s *Store) SetFirstName(chatID int64, name string) {
	if name == "" {
		return
	}
	_, _ = s.db.Exec(`
		INSERT INTO user_prefs (chat_id, first_name) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET first_name = excluded.first_name, updated_at = CURRENT_TIMESTAMP`,
		chatID, name,
	)
}

// GetStreakCelebrated returns the highest streak milestone already celebrated (0 if none).
func (s *Store) GetStreakCelebrated(chatID int64) int {
	var n int
	_ = s.db.QueryRow(
		"SELECT COALESCE(streak_celebrated,0) FROM user_prefs WHERE chat_id = ?",
		chatID,
	).Scan(&n)
	return n
}

// SetStreakCelebrated records the highest milestone that has been celebrated.
func (s *Store) SetStreakCelebrated(chatID int64, milestone int) {
	_, _ = s.db.Exec(`
		INSERT INTO user_prefs (chat_id, streak_celebrated) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET streak_celebrated = excluded.streak_celebrated, updated_at = CURRENT_TIMESTAMP`,
		chatID, milestone,
	)
}

// GetLevel returns just the user's difficulty level (default when unset).
func (s *Store) GetLevel(chatID int64) string {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load level for chat %d: %v (using default)", chatID, err)
		return config.DefaultLevel
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

// GetTTSEnabled reports whether pronunciation voice messages are enabled for the
// user (default true when unset).
func (s *Store) GetTTSEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load TTS flag for chat %d: %v (using default true)", chatID, err)
		return true
	}
	return prefs.TTSEnabled
}

// SetTTSEnabled upserts the user's TTS preference.
func (s *Store) SetTTSEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, tts_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET tts_enabled = excluded.tts_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetTipsEnabled reports whether scheduled daily tips are enabled for a user.
func (s *Store) GetTipsEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load tips_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.TipsEnabled
}

// SetTipsEnabled upserts the user's scheduled-tip flag.
func (s *Store) SetTipsEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, tips_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET tips_enabled = excluded.tips_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetQuizEnabled reports whether scheduled quizzes are enabled for the user.
func (s *Store) GetQuizEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load quiz_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.QuizEnabled
}

// SetQuizEnabled upserts the user's scheduled-quiz flag.
func (s *Store) SetQuizEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, quiz_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET quiz_enabled = excluded.quiz_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetIdiomEnabled reports whether the daily idiom is enabled for the user.
func (s *Store) GetIdiomEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load idiom_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.IdiomEnabled
}

// SetIdiomEnabled upserts the user's daily-idiom flag.
func (s *Store) SetIdiomEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, idiom_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET idiom_enabled = excluded.idiom_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetCollocationEnabled reports whether the daily collocation is enabled for the user.
func (s *Store) GetCollocationEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load collocation_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.CollocationEnabled
}

// SetCollocationEnabled upserts the user's daily-collocation flag.
func (s *Store) SetCollocationEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, collocation_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET collocation_enabled = excluded.collocation_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetStoryEnabled reports whether the daily mini story is enabled for the user.
func (s *Store) GetStoryEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load story_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.StoryEnabled
}

// SetStoryEnabled upserts the user's daily-mini-story flag.
func (s *Store) SetStoryEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, story_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET story_enabled = excluded.story_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetReviewEnabled reports whether SRS word reviews are enabled for the user.
func (s *Store) GetReviewEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load review_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.ReviewEnabled
}

// SetReviewEnabled upserts the user's SRS-review flag.
func (s *Store) SetReviewEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, review_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET review_enabled = excluded.review_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetDigestEnabled reports whether the weekly digest is enabled for the user.
func (s *Store) GetDigestEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load digest_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.DigestEnabled
}

// SetDigestEnabled upserts the user's weekly-digest flag.
func (s *Store) SetDigestEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, digest_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET digest_enabled = excluded.digest_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetDailyReviewEnabled reports whether the midnight vocab recap is enabled for the user.
func (s *Store) GetDailyReviewEnabled(chatID int64) bool {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load daily_review_enabled for chat %d: %v (using default=true)", chatID, err)
		return true
	}
	return prefs.DailyReviewEnabled
}

// SetDailyReviewEnabled upserts the user's daily-review flag.
func (s *Store) SetDailyReviewEnabled(chatID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, daily_review_enabled) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET daily_review_enabled = excluded.daily_review_enabled, updated_at = CURRENT_TIMESTAMP`,
		chatID, v,
	)
	return err
}

// GetQuizIntervalHours returns the user's quiz interval in hours.
func (s *Store) GetQuizIntervalHours(chatID int64) int {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		log.Printf("⚠️  [PREFS] Could not load quiz_interval_hours for chat %d: %v (using default)", chatID, err)
		return defaultQuizIntervalHours
	}
	return prefs.QuizIntervalHours
}

// SetQuizIntervalHours upserts the user's quiz interval (in hours).
func (s *Store) SetQuizIntervalHours(chatID int64, hours int) error {
	hours, _ = normalizeQuizIntervalHours(hours)
	_, err := s.db.Exec(`
		INSERT INTO user_prefs (chat_id, quiz_interval_hours) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET quiz_interval_hours = excluded.quiz_interval_hours, updated_at = CURRENT_TIMESTAMP`,
		chatID, hours,
	)
	return err
}

// ActiveLevels returns the distinct set of levels the pool should keep stocked:
// always the default level, plus any level a user has explicitly selected.
func (s *Store) ActiveLevels() ([]string, error) {
	seen := map[string]bool{config.DefaultLevel: true}
	levels := []string{config.DefaultLevel}

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
