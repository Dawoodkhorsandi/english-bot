package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
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
	// srsMinEase is the floor for the ease factor (classic SM-2 uses 1.3).
	srsMinEase = 1.3
	// srsDefaultEase is the starting ease for a freshly seeded word.
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
	intervalDays int
	ease         float64
	reps         int
}

// srsKnown advances a word's schedule after a successful recall. The interval
// grows (1 → 3 → round(interval×ease)) and the ease nudges up slightly.
func srsKnown(intervalDays int, ease float64, reps int) (nextInterval int, nextEase float64, nextReps int) {
	nextReps = reps + 1
	nextEase = ease + 0.1
	switch nextReps {
	case 1:
		nextInterval = 1
	case 2:
		nextInterval = 3
	default:
		nextInterval = int(math.Round(float64(intervalDays) * ease))
		if nextInterval <= intervalDays {
			nextInterval = intervalDays + 1
		}
	}
	return nextInterval, nextEase, nextReps
}

// srsForgot resets a word's schedule after a failed recall: back to a 1-day
// interval, reps zeroed, and the ease reduced (floored at srsMinEase).
func srsForgot(ease float64) (nextInterval int, nextEase float64, nextReps int) {
	nextEase = ease - 0.2
	if nextEase < srsMinEase {
		nextEase = srsMinEase
	}
	return 1, nextEase, 0
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
	rows, err := s.db.Query(`
		SELECT rs.word, COALESCE(cp.meaning, ''), rs.interval_days, rs.ease, rs.reps
		FROM review_schedule rs
		LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = rs.word
		WHERE rs.chat_id = ? AND rs.due_at <= ?
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
		if err := rows.Scan(&d.term, &d.meaning, &d.intervalDays, &d.ease, &d.reps); err != nil {
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

// updateReview persists a word's new schedule state, setting the next due_at to
// now + intervalDays days.
func (s *Store) updateReview(chatID int64, word string, intervalDays int, ease float64, reps int, now time.Time) error {
	word = strings.ToLower(strings.TrimSpace(word))
	due := now.UTC().AddDate(0, 0, intervalDays).Format(srsTimeLayout)
	_, err := s.db.Exec(
		"UPDATE review_schedule SET interval_days = ?, ease = ?, reps = ?, due_at = ? WHERE chat_id = ? AND word = ?",
		intervalDays, ease, reps, due, chatID, word,
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
	interval, ease, reps, found, err := s.getReview(chatID, word)
	if err != nil || !found {
		return false, err
	}
	ni, ne, nr := srsKnown(interval, ease, reps)
	return true, s.updateReview(chatID, word, ni, ne, nr, now)
}

// ApplyReviewForgot resets a word after a "Forgot" recall. ok=false when the
// word isn't in the schedule.
func (s *Store) ApplyReviewForgot(chatID int64, word string, now time.Time) (ok bool, err error) {
	_, ease, _, found, err := s.getReview(chatID, word)
	if err != nil || !found {
		return false, err
	}
	ni, ne, nr := srsForgot(ease)
	return true, s.updateReview(chatID, word, ni, ne, nr, now)
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
func runReviewScheduler(ctx context.Context, store *Store, notifier Notifier) {
	log.Printf("🧠 [SRS] Spaced-repetition scheduler started (every %s, up to %d/user).", reviewCheckInterval, reviewBatchMax)
	ticker := time.NewTicker(reviewCheckInterval)
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
func runReviewSweep(store *Store, notifier Notifier, now time.Time) {
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
		// reviewBatchMax (default 1) caps how many cards fire per sweep.
		due, err := store.DueReviews(chatID, now, reviewBatchMax)
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
func reviewKeyboard(term string) [][]inlineButton {
	return [][]inlineButton{{
		{Text: "✅ Knew it", CallbackData: "srs:known:" + term},
		{Text: "❌ Forgot", CallbackData: "srs:forgot:" + term},
	}}
}
