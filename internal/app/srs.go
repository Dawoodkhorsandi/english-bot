package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Spaced Repetition Review (Change D)
//
// A lightweight SM-2-style scheduler per (chat_id, word). Every vocabulary word
// a user receives is seeded into review_schedule with interval=1 day. A
// background scheduler resurfaces words whose due_at has passed as a compact
// "memory check" card with "Knew it / Forgot" buttons; the answer promotes or
// resets the word's interval. Difficulty feedback can also come from Change E.
// ---------------------------------------------------------------------------

const (
	// srsDefaultEase is the legacy SM-2 ease column's default for newly seeded
	// words. The scheduler is now FSRS (stability/difficulty); ease is retained
	// only so existing rows and the seed INSERT stay schema-compatible.
	srsDefaultEase = 2.5
	// srsMasteredIntervalDays is the interval at/above which a word counts as
	// moved into long-term memory (used by /stats).
	srsMasteredIntervalDays = 21
)

// stored timestamp layout (UTC), matching the rest of the codebase.
const srsTimeLayout = "2006-01-02 15:04:05"

// dueReview is one word due for spaced-repetition review.
type dueReview struct {
	term         string
	meaning      string
	text         string // full pooled card text (for pronunciation/Persian/example)
	intervalDays int
	ease         float64
	reps         int
}

// ---------------------------------------------------------------------------
// FSRS-lite scheduler
//
// We schedule with the open FSRS algorithm (the modern successor to SM-2 used by
// Anki). Each card carries a stability S (days until recall probability decays
// to ~90%) and a difficulty D (1–10). The next interval is chosen so the
// predicted recall probability at review time equals the user's *desired
// retention* dial (a per-user pref). Higher retention ⇒ shorter intervals ⇒ more
// reviews but fewer lapses. We only have two buttons, so a "Knew it" maps to the
// FSRS "good" grade and a "Forgot" to "again".
//
// fsrsW are the FSRS-5 default weights. fsrsFactor / fsrsDecay define the power
// forgetting curve R(t) = (1 + FACTOR·t/S)^DECAY.
// ---------------------------------------------------------------------------

var fsrsW = [19]float64{
	0.40255, 1.18385, 3.173, 15.69105, 7.1949, 0.5345, 1.4604, 0.0046,
	1.54575, 0.1192, 1.01925, 1.9395, 0.11, 0.29605, 2.2698, 0.2315,
	2.9898, 0.51655, 0.6621,
}

const (
	fsrsDecay       = -0.5
	fsrsFactor      = 0.2345679 // 0.9^(1/fsrsDecay) − 1, the FSRS interval constant
	fsrsMaxInterval = 365 * 5   // cap a single interval at five years

	gradeAgain = 1 // "Forgot"
	gradeGood  = 3 // "Knew it"
	gradeEasy  = 4 // used only as the difficulty mean-reversion target

	// retention dial bounds; 0.9 is the FSRS default sweet spot.
	defaultRetention = 0.9
	minRetention     = 0.70
	maxRetention     = 0.97
)

func fsrsClampD(d float64) float64 {
	return math.Max(1, math.Min(10, d))
}

// fsrsInitStability is the stability of a brand-new card for a given grade.
func fsrsInitStability(grade int) float64 {
	return math.Max(fsrsW[grade-1], 0.1)
}

// fsrsInitDifficulty is the starting difficulty for a brand-new card.
func fsrsInitDifficulty(grade int) float64 {
	return fsrsClampD(fsrsW[4] - math.Exp(fsrsW[5]*float64(grade-1)) + 1)
}

// fsrsRetrievability is the predicted recall probability of a card with the
// given stability after elapsedDays have passed since the last review.
func fsrsRetrievability(elapsedDays, stability float64) float64 {
	if stability <= 0 {
		return 0
	}
	if elapsedDays < 0 {
		elapsedDays = 0
	}
	return math.Pow(1+fsrsFactor*elapsedDays/stability, fsrsDecay)
}

// fsrsNextInterval picks the whole-day interval whose predicted retrievability
// equals the desired retention.
func fsrsNextInterval(stability, retention float64) int {
	if retention <= 0 || retention >= 1 {
		retention = defaultRetention
	}
	iv := stability / fsrsFactor * (math.Pow(retention, 1/fsrsDecay) - 1)
	n := int(math.Round(iv))
	if n < 1 {
		n = 1
	}
	if n > fsrsMaxInterval {
		n = fsrsMaxInterval
	}
	return n
}

// fsrsNextDifficulty updates difficulty after a grade (FSRS-5 linear damping +
// mean reversion toward the "easy" baseline).
func fsrsNextDifficulty(d float64, grade int) float64 {
	delta := -fsrsW[6] * float64(grade-3)
	dp := d + delta*(10-d)/9
	return fsrsClampD(fsrsW[7]*fsrsInitDifficulty(gradeEasy) + (1-fsrsW[7])*dp)
}

// fsrsNextStabilityRecall grows stability after a successful recall.
func fsrsNextStabilityRecall(d, s, r float64) float64 {
	return s * (1 + math.Exp(fsrsW[8])*(11-d)*math.Pow(s, -fsrsW[9])*(math.Exp((1-r)*fsrsW[10])-1))
}

// fsrsNextStabilityForget computes the (lower) post-lapse stability. A lapse can
// never increase stability.
func fsrsNextStabilityForget(d, s, r float64) float64 {
	sf := fsrsW[11] * math.Pow(d, -fsrsW[12]) * (math.Pow(s+1, fsrsW[13]) - 1) * math.Exp((1-r)*fsrsW[14])
	return math.Max(0.1, math.Min(sf, s))
}

// normalizeRetention clamps a desired-retention value to the supported range,
// falling back to the default for zero/out-of-range input.
func normalizeRetention(r float64) float64 {
	if r < minRetention || r > maxRetention {
		return defaultRetention
	}
	return r
}

// ---------------------------------------------------------------------------
// review_schedule store methods
// ---------------------------------------------------------------------------

// SeedReview enrols a word into the spaced-repetition schedule the first time it
// is sent (idempotent — an existing schedule is never reset). The first review
// is due one day later.
func (s *Store) SeedReview(chatID int64, word string, now time.Time) error {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return nil
	}
	due := now.UTC().AddDate(0, 0, 1).Format(srsTimeLayout)
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO review_schedule (chat_id, word, interval_days, ease, reps, due_at)
		VALUES (?, ?, 1, ?, 0, ?)`,
		chatID, word, srsDefaultEase, due,
	)
	return err
}

// DueReviews returns up to limit words whose review is due (due_at <= now) for a
// chat, oldest-due first, joined with their meaning from the content pool.
func (s *Store) DueReviews(chatID int64, now time.Time, limit int) ([]dueReview, error) {
	// GROUP BY rs.word so a word that exists in content_pool at more than one
	// level (the pool is UNIQUE per (kind, level, term)) yields a single review
	// card, not one per level. MAX() picks one non-empty meaning/text.
	rows, err := s.db.Query(`
		SELECT rs.word, COALESCE(MAX(cp.meaning), ''), COALESCE(MAX(cp.text), ''),
		       rs.interval_days, rs.ease, rs.reps
		FROM review_schedule rs
		LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = rs.word
		WHERE rs.chat_id = ? AND rs.due_at <= ?
		GROUP BY rs.word, rs.interval_days, rs.ease, rs.reps, rs.due_at
		ORDER BY rs.due_at ASC
		LIMIT ?`,
		chatID, now.UTC().Format(srsTimeLayout), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dueReview
	for rows.Next() {
		var d dueReview
		if err := rows.Scan(&d.term, &d.meaning, &d.text, &d.intervalDays, &d.ease, &d.reps); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// getReview loads a single schedule row. ok=false when the word isn't scheduled.
func (s *Store) getReview(chatID int64, word string) (intervalDays int, ease float64, reps int, ok bool, err error) {
	word = strings.ToLower(strings.TrimSpace(word))
	err = s.db.QueryRow(
		"SELECT interval_days, ease, reps FROM review_schedule WHERE chat_id = ? AND word = ?",
		chatID, word,
	).Scan(&intervalDays, &ease, &reps)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, false, nil
		}
		return 0, 0, 0, false, err
	}
	return intervalDays, ease, reps, true, nil
}

// fsrsCard is the FSRS memory state persisted for one (chat, word).
type fsrsCard struct {
	stability    float64
	difficulty   float64
	intervalDays int
	reps         int
	lastReview   time.Time
	hasLast      bool
}

// getReviewState loads a word's FSRS state. ok=false when it isn't scheduled.
func (s *Store) getReviewState(chatID int64, word string) (fsrsCard, bool, error) {
	word = strings.ToLower(strings.TrimSpace(word))
	var c fsrsCard
	var lastRaw any
	err := s.db.QueryRow(
		`SELECT COALESCE(stability,0), COALESCE(difficulty,0), interval_days, reps, last_review
		 FROM review_schedule WHERE chat_id = ? AND word = ?`,
		chatID, word,
	).Scan(&c.stability, &c.difficulty, &c.intervalDays, &c.reps, &lastRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if ts, ok := parseStoredUTC(lastRaw); ok {
		c.lastReview, c.hasLast = ts, true
	}
	return c, true, nil
}

// updateReviewState persists a word's new FSRS state and reschedules due_at to
// now + intervalDays days, stamping last_review = now.
func (s *Store) updateReviewState(chatID int64, word string, c fsrsCard, intervalDays int, now time.Time) error {
	word = strings.ToLower(strings.TrimSpace(word))
	due := now.UTC().AddDate(0, 0, intervalDays).Format(srsTimeLayout)
	_, err := s.db.Exec(
		`UPDATE review_schedule
		 SET interval_days = ?, stability = ?, difficulty = ?, reps = ?, last_review = ?, due_at = ?
		 WHERE chat_id = ? AND word = ?`,
		intervalDays, c.stability, c.difficulty, c.reps, now.UTC().Format(srsTimeLayout), due, chatID, word,
	)
	return err
}

// SnoozeReview pushes a word's next review out by intervalDays days from now,
// without changing its interval/ease/reps. Used after a reminder is shown so the
// same word isn't re-sent on every scheduler tick before the user answers.
func (s *Store) SnoozeReview(chatID int64, word string, intervalDays int, now time.Time) error {
	word = strings.ToLower(strings.TrimSpace(word))
	if intervalDays < 1 {
		intervalDays = 1
	}
	due := now.UTC().AddDate(0, 0, intervalDays).Format(srsTimeLayout)
	_, err := s.db.Exec(
		"UPDATE review_schedule SET due_at = ? WHERE chat_id = ? AND word = ?",
		due, chatID, word,
	)
	return err
}

// ApplyReviewKnown promotes a word after a correct/"Knew it" recall. ok=false
// when the word isn't in the schedule (e.g. a stale button).
func (s *Store) ApplyReviewKnown(chatID int64, word string, now time.Time) (ok bool, err error) {
	return s.applyReview(chatID, word, gradeGood, now)
}

// ApplyReviewForgot resets a word after a "Forgot" recall. ok=false when the
// word isn't in the schedule.
func (s *Store) ApplyReviewForgot(chatID int64, word string, now time.Time) (ok bool, err error) {
	return s.applyReview(chatID, word, gradeAgain, now)
}

// applyReview folds one grade into a word's FSRS state and reschedules it at the
// user's desired retention. Brand-new cards take the FSRS init values; legacy
// SM-2 cards (which predate stability tracking) are bootstrapped from their
// existing interval so a user's progress carries over.
func (s *Store) applyReview(chatID int64, word string, grade int, now time.Time) (bool, error) {
	c, found, err := s.getReviewState(chatID, word)
	if err != nil || !found {
		return false, err
	}
	retention := s.GetDesiredRetention(chatID)

	if c.stability <= 0 {
		if c.reps > 0 && c.intervalDays > 0 {
			// Legacy card: seed FSRS state from the prior interval, neutral difficulty.
			c.stability = float64(c.intervalDays)
			c.difficulty = 5
		} else {
			// Brand-new card: initialise straight from the grade.
			c.stability = fsrsInitStability(grade)
			c.difficulty = fsrsInitDifficulty(grade)
			c.reps = 0
			if grade != gradeAgain {
				c.reps = 1
			}
			return true, s.updateReviewState(chatID, word, c, fsrsNextInterval(c.stability, retention), now)
		}
	}

	elapsed := 0.0
	if c.hasLast {
		elapsed = now.Sub(c.lastReview).Hours() / 24
	}
	r := fsrsRetrievability(elapsed, c.stability)
	c.difficulty = fsrsNextDifficulty(c.difficulty, grade)
	if grade == gradeAgain {
		c.stability = fsrsNextStabilityForget(c.difficulty, c.stability, r)
		c.reps = 0
	} else {
		c.stability = fsrsNextStabilityRecall(c.difficulty, c.stability, r)
		c.reps++
	}
	return true, s.updateReviewState(chatID, word, c, fsrsNextInterval(c.stability, retention), now)
}

// ExportWord is one row of a user's exportable learning data.
type ExportWord struct {
	Term         string  `json:"term"`
	Meaning      string  `json:"meaning"`
	IntervalDays int     `json:"interval_days"`
	Stability    float64 `json:"stability"`
	Difficulty   float64 `json:"difficulty"`
	Reps         int     `json:"reps"`
	DueAt        string  `json:"due_at"`
	Mastered     bool    `json:"mastered"`
}

// ExportReviewData returns a user's full spaced-repetition schedule (with the
// pooled meaning for each word) for the data-export feature — every word they've
// learned plus its FSRS memory state, so progress is portable, not locked in.
func (s *Store) ExportReviewData(chatID int64) ([]ExportWord, error) {
	rows, err := s.db.Query(`
		SELECT rs.word, COALESCE(MAX(cp.meaning), ''),
		       rs.interval_days, COALESCE(rs.stability,0), COALESCE(rs.difficulty,0),
		       rs.reps, rs.due_at
		FROM review_schedule rs
		LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = rs.word
		WHERE rs.chat_id = ?
		GROUP BY rs.word, rs.interval_days, rs.stability, rs.difficulty, rs.reps, rs.due_at
		ORDER BY rs.due_at ASC`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExportWord
	for rows.Next() {
		var e ExportWord
		if err := rows.Scan(&e.Term, &e.Meaning, &e.IntervalDays, &e.Stability, &e.Difficulty, &e.Reps, &e.DueAt); err != nil {
			return nil, err
		}
		e.Mastered = e.IntervalDays >= srsMasteredIntervalDays
		out = append(out, e)
	}
	return out, rows.Err()
}

// MasteredCount returns how many of a user's words have reached the mastered
// interval threshold (moved into long-term memory).
func (s *Store) MasteredCount(chatID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM review_schedule WHERE chat_id = ? AND interval_days >= ?",
		chatID, srsMasteredIntervalDays,
	).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Review scheduler goroutine
// ---------------------------------------------------------------------------

// runReviewScheduler periodically resurfaces due words to each active subscriber
// as a "memory check" card with Knew-it/Forgot buttons. It honours quiet hours
// and the per-user paused flag, and snoozes each reminded word so it isn't
// re-sent before the user responds.
func runReviewScheduler(ctx context.Context, store *Store, notifier telegram.Notifier) {
	log.Printf("🧠 [SRS] Spaced-repetition scheduler started (every %s, up to %d/user).", config.ReviewCheckInterval, config.ReviewBatchMax)
	ticker := time.NewTicker(config.ReviewCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🧠 [SRS] Spaced-repetition scheduler stopped.")
			return
		case <-ticker.C:
			runReviewSweep(store, notifier, time.Now())
		}
	}
}

// runReviewSweep delivers due review reminders for the current moment.
func runReviewSweep(store *Store, notifier telegram.Notifier, now time.Time) {
	if isQuietHours(now) {
		return
	}
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [SRS] Could not read subscribers: %v", err)
		return
	}

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [SRS] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.ReviewEnabled {
			continue
		}
		// SRS reviews are supplementary — they send independently of the global
		// rate limiter so they never block or get blocked by broadcasts/quiz/idiom.
		// config.ReviewBatchMax (default 1) caps how many cards fire per sweep.
		due, err := store.DueReviews(chatID, now, config.ReviewBatchMax)
		if err != nil {
			log.Printf("⚠️  [SRS] Due lookup failed for chat %d: %v", chatID, err)
			continue
		}
		for _, d := range due {
			if err := notifier.SendKeyboard(chatID, formatReviewReminder(d), reviewKeyboard(d.term)); err != nil {
				log.Printf("❌ [SRS] Reminder send to chat %d failed: %v", chatID, err)
				continue
			}
			// Snooze so we don't re-send before the user answers; a tapped
			// button reschedules precisely from the user's response.
			if err := store.SnoozeReview(chatID, d.term, d.intervalDays, now); err != nil {
				log.Printf("⚠️  [SRS] Could not snooze %q for chat %d: %v", d.term, chatID, err)
			}
			sent++
		}
	}
	if sent > 0 {
		log.Printf("🧠 [SRS] Sweep complete: %d reminder(s) delivered.", sent)
	}
}

// formatReviewReminder renders a compact spaced-repetition memory-check card.
// The meaning is deliberately hidden so the user has to recall it first — it is
// revealed only after they tap "Knew it" / "Forgot" (see handleReviewCallback).
func formatReviewReminder(d dueReview) string {
	var b strings.Builder
	b.WriteString("🧠 <b>Memory check</b>\n\n")
	b.WriteString(fmt.Sprintf("Do you still remember what <b>%s</b> means?\n", d.term))
	b.WriteString("\nRecall it first, then tap below — be honest, your answer tunes when you'll see it next.")
	return b.String()
}

// reviewKeyboard builds the Knew-it / Forgot inline keyboard for a word.
func reviewKeyboard(term string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "✅ Knew it", CallbackData: "srs:known:" + term},
		{Text: "❌ Forgot", CallbackData: "srs:forgot:" + term},
	}}
}

const (
	// minBatchForLevelSuggestion is the smallest sample we'll draw a level-change
	// conclusion from — fewer cards is too noisy to trust.
	minBatchForLevelSuggestion = 5
	// levelUpAccuracy / levelDownAccuracy are the rolling-accuracy thresholds for
	// nudging the user to a harder / easier level.
	levelUpAccuracy   = 0.85
	levelDownAccuracy = 0.50
	// reviewPerfMinSample is how many recent review answers must accumulate before
	// we'll even consider a level suggestion. The suggestion is based on this
	// rolling window, NOT a single session, so it only fires once we're confident
	// the user's level is genuinely mismatched.
	reviewPerfMinSample = 20
	// reviewPerfWindowCap keeps the rolling window recency-weighted: once it grows
	// past this, both counters halve so old performance fades out.
	reviewPerfWindowCap = 60
	// reviewSuggestCooldown is the minimum gap between two level suggestions, so a
	// user is never nagged session after session.
	reviewSuggestCooldown = 7 * 24 * time.Hour
)

// suggestLevelChange inspects how an in-app review batch went and, when the
// signal is strong enough, proposes the adjacent difficulty level. It returns
// the target level, a "harder"/"easier" direction, and ok=true only when a
// suggestion is warranted: a high-accuracy batch nudges up (unless already at
// the hardest level), a low-accuracy batch nudges down (unless already easiest).
// Batches smaller than minBatchForLevelSuggestion never suggest.
func suggestLevelChange(currentLevel string, correct, total int) (target, direction string, ok bool) {
	if total < minBatchForLevelSuggestion || correct < 0 || correct > total {
		return "", "", false
	}
	idx := -1
	for i, l := range config.AllLevels {
		if l == currentLevel {
			idx = i
			break
		}
	}
	if idx == -1 {
		return "", "", false
	}
	accuracy := float64(correct) / float64(total)
	switch {
	case accuracy >= levelUpAccuracy && idx < len(config.AllLevels)-1:
		return config.AllLevels[idx+1], "harder", true
	case accuracy <= levelDownAccuracy && idx > 0:
		return config.AllLevels[idx-1], "easier", true
	default:
		return "", "", false
	}
}

// RecordReviewOutcome folds one review answer into the user's rolling
// performance window (used to decide whether to suggest a level change). The
// window is recency-weighted: once it grows past reviewPerfWindowCap both
// counters halve, so a level the user has since outgrown doesn't haunt them.
func (s *Store) RecordReviewOutcome(chatID int64, known bool) error {
	c := 0
	if known {
		c = 1
	}
	if _, err := s.db.Exec(`
		INSERT INTO review_perf (chat_id, window_correct, window_total) VALUES (?, ?, 1)
		ON CONFLICT(chat_id) DO UPDATE SET
			window_correct = window_correct + ?,
			window_total   = window_total + 1`,
		chatID, c, c,
	); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		UPDATE review_perf SET window_correct = window_correct / 2, window_total = window_total / 2
		WHERE chat_id = ? AND window_total > ?`,
		chatID, reviewPerfWindowCap,
	)
	return err
}

// reviewPerf reads a user's rolling review window and the time of their last
// level suggestion (lastOK=false when none yet).
func (s *Store) reviewPerf(chatID int64) (correct, total int, last time.Time, lastOK bool, err error) {
	var lastRaw any
	err = s.db.QueryRow(
		"SELECT window_correct, window_total, last_suggest_at FROM review_perf WHERE chat_id = ?",
		chatID,
	).Scan(&correct, &total, &lastRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, 0, time.Time{}, false, err
	}
	if ts, ok := parseStoredUTC(lastRaw); ok {
		last, lastOK = ts, true
	}
	return correct, total, last, lastOK, nil
}

// markLevelSuggested stamps the cooldown and resets the rolling window so the
// next suggestion is judged on fresh performance after the user acts.
func (s *Store) markLevelSuggested(chatID int64, now time.Time) error {
	_, err := s.db.Exec(
		"UPDATE review_perf SET window_correct = 0, window_total = 0, last_suggest_at = ? WHERE chat_id = ?",
		now.UTC().Format(srsTimeLayout), chatID,
	)
	return err
}

// LevelSuggestion decides, from a user's accumulated review performance, whether
// to nudge them to a different difficulty level. It only fires once the rolling
// window has at least reviewPerfMinSample answers and the cooldown has elapsed,
// so it surfaces after a sustained run of reviews — never after a single batch.
// When it returns ok=true it has already stamped the cooldown and reset the
// window (the caller need only render the suggestion).
func (s *Store) LevelSuggestion(chatID int64, now time.Time) (target, direction string, accuracy int, ok bool, err error) {
	correct, total, last, lastOK, err := s.reviewPerf(chatID)
	if err != nil || total < reviewPerfMinSample {
		return "", "", 0, false, err
	}
	if lastOK && now.Sub(last) < reviewSuggestCooldown {
		return "", "", 0, false, nil
	}
	target, direction, ok = suggestLevelChange(s.GetLevel(chatID), correct, total)
	if !ok {
		return "", "", 0, false, nil
	}
	accuracy = correct * 100 / total
	if err := s.markLevelSuggested(chatID, now); err != nil {
		return "", "", 0, false, err
	}
	return target, direction, accuracy, true, nil
}
