package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Quiz / Active Recall (Change E)
//
// Turns passive reading into testing. A quiz picks a word the user has already
// learned (biased toward words due for spaced-repetition review) and asks a
// multiple-choice question built entirely from pooled words — no AI call. The
// answer is recorded in quiz_results and feeds the spaced-repetition schedule
// (correct = promote, wrong = reset) and /stats accuracy.
// ---------------------------------------------------------------------------

const (
	quizTypeWordToMeaning = "w2m" // "What does WORD mean?" → meaning options
	quizTypeMeaningToWord = "m2w" // "Which word means …?" → word options
	quizOptionCount       = 4
)

// quizQuestion is a ready-to-send multiple-choice question.
type quizQuestion struct {
	word       string   // subject word (lowercased) the result is recorded against
	prompt     string   // HTML prompt
	options    []string // display text per button
	correctIdx int      // index of the correct option
}

// newRand returns a freshly seeded PRNG (cheap; callers use their own instance
// so there is no shared-state contention between goroutines).
func newRand() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// ---------------------------------------------------------------------------
// Quiz building (pure-ish helpers)
// ---------------------------------------------------------------------------

// buildQuiz assembles a question of qType around subject, drawing 3 distractors
// from distractPool. Returns ok=false when there aren't enough distinct
// distractors to fill the options.
func buildQuiz(subject reviewItem, distractPool []reviewItem, qType string, rng *rand.Rand) (quizQuestion, bool) {
	optionValue := func(it reviewItem) string {
		if qType == quizTypeMeaningToWord {
			return it.term
		}
		return it.meaning
	}

	correctVal := strings.TrimSpace(optionValue(subject))
	if correctVal == "" {
		return quizQuestion{}, false
	}

	used := map[string]bool{strings.ToLower(correctVal): true}
	shuffled := append([]reviewItem(nil), distractPool...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	var options []string
	for _, it := range shuffled {
		if it.term == subject.term {
			continue
		}
		v := strings.TrimSpace(optionValue(it))
		key := strings.ToLower(v)
		if v == "" || used[key] {
			continue
		}
		used[key] = true
		options = append(options, v)
		if len(options) == quizOptionCount-1 {
			break
		}
	}
	if len(options) < quizOptionCount-1 {
		return quizQuestion{}, false
	}

	options = append(options, correctVal)
	rng.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	correctIdx := 0
	for i, o := range options {
		if o == correctVal {
			correctIdx = i
			break
		}
	}

	var prompt string
	if qType == quizTypeMeaningToWord {
		prompt = fmt.Sprintf("🧩 <b>Quiz</b>\n\nWhich word means:\n\n<i>%s</i>", subject.meaning)
	} else {
		prompt = fmt.Sprintf("🧩 <b>Quiz</b>\n\nWhat does <b>%s</b> mean?", subject.term)
	}
	return quizQuestion{word: subject.term, prompt: prompt, options: options, correctIdx: correctIdx}, true
}

// makeQuiz builds a quiz for a user from their learned words, biasing the
// subject toward words currently due for review. ok=false means there isn't
// enough material yet (too few learned words / distractors).
func makeQuiz(store *Store, chatID int64, now time.Time, rng *rand.Rand) (quizQuestion, bool, error) {
	seen, err := store.SeenWordsWithMeaning(chatID)
	if err != nil {
		return quizQuestion{}, false, err
	}
	if len(seen) == 0 {
		return quizQuestion{}, false, nil
	}

	// Prefer a subject that is due for spaced-repetition review.
	due, _ := store.DueReviews(chatID, now, 100)
	dueSet := make(map[string]bool, len(due))
	for _, d := range due {
		dueSet[d.term] = true
	}
	var preferred []reviewItem
	for _, it := range seen {
		if dueSet[it.term] {
			preferred = append(preferred, it)
		}
	}
	candidates := seen
	if len(preferred) > 0 {
		candidates = preferred
	}
	subject := candidates[rng.Intn(len(candidates))]

	pool, err := store.PoolWordMeanings(60)
	if err != nil {
		return quizQuestion{}, false, err
	}

	qType := quizTypeWordToMeaning
	if rng.Intn(2) == 0 {
		qType = quizTypeMeaningToWord
	}
	q, ok := buildQuiz(subject, pool, qType, rng)
	if !ok {
		// Fall back to the other question type before giving up.
		other := quizTypeMeaningToWord
		if qType == quizTypeMeaningToWord {
			other = quizTypeWordToMeaning
		}
		q, ok = buildQuiz(subject, pool, other, rng)
	}
	return q, ok, nil
}

// quizKeyboard renders one option per row; the correct option carries a "c" tag
// in its callback data, wrong ones an "x". All buttons record against the same
// subject word so a tap always grades that word.
func quizKeyboard(q quizQuestion) [][]inlineButton {
	rows := make([][]inlineButton, 0, len(q.options))
	for i, opt := range q.options {
		cx := "x"
		if i == q.correctIdx {
			cx = "c"
		}
		rows = append(rows, []inlineButton{{Text: opt, CallbackData: "quiz:" + cx + ":" + q.word}})
	}
	return rows
}

// ---------------------------------------------------------------------------
// quiz_results store methods
// ---------------------------------------------------------------------------

// SeenWordsWithMeaning returns the words a user has received that have a
// non-empty meaning in the content pool (eligible quiz subjects).
func (s *Store) SeenWordsWithMeaning(chatID int64) ([]reviewItem, error) {
	rows, err := s.db.Query(`
		SELECT sv.word, cp.meaning
		FROM sent_vocab sv
		JOIN content_pool cp ON cp.kind = 'word' AND cp.term = sv.word
		WHERE sv.chat_id = ? AND TRIM(cp.meaning) != ''`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []reviewItem
	for rows.Next() {
		var it reviewItem
		if err := rows.Scan(&it.term, &it.meaning); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// PoolWordMeanings returns up to limit random pooled words with meanings, used
// as the distractor source for quizzes.
func (s *Store) PoolWordMeanings(limit int) ([]reviewItem, error) {
	rows, err := s.db.Query(
		"SELECT term, meaning FROM content_pool WHERE kind = 'word' AND TRIM(meaning) != '' ORDER BY RANDOM() LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []reviewItem
	for rows.Next() {
		var it reviewItem
		if err := rows.Scan(&it.term, &it.meaning); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// MeaningForWord returns the pooled meaning for a word ("" if none).
func (s *Store) MeaningForWord(word string) string {
	var meaning string
	_ = s.db.QueryRow(
		"SELECT meaning FROM content_pool WHERE kind = 'word' AND term = ?",
		strings.ToLower(strings.TrimSpace(word)),
	).Scan(&meaning)
	return meaning
}

// RecordQuizResult appends a quiz answer to the history.
func (s *Store) RecordQuizResult(chatID int64, word string, correct bool) error {
	c := 0
	if correct {
		c = 1
	}
	_, err := s.db.Exec(
		"INSERT INTO quiz_results (chat_id, word, correct) VALUES (?, ?, ?)",
		chatID, strings.ToLower(strings.TrimSpace(word)), c,
	)
	return err
}

// QuizStats returns the total answered and correct counts for a user.
func (s *Store) QuizStats(chatID int64) (answered, correct int, err error) {
	err = s.db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(correct), 0) FROM quiz_results WHERE chat_id = ?",
		chatID,
	).Scan(&answered, &correct)
	return answered, correct, err
}

// ---------------------------------------------------------------------------
// Command + callback handlers
// ---------------------------------------------------------------------------

// handleQuiz sends one quiz question on demand (/quiz).
func handleQuiz(store *Store, chatID int64) {
	q, ok, err := makeQuiz(store, chatID, time.Now(), newRand())
	if err != nil {
		log.Printf("❌ [QUIZ] Could not build quiz for chat %d: %v", chatID, err)
		_ = sendToTelegram(chatID, "❌ Sorry, I couldn't make a quiz right now. Please try again.")
		return
	}
	if !ok {
		_ = sendToTelegram(chatID, "🧩 Not enough learned words for a quiz yet — keep practising and I'll test you soon!")
		return
	}
	if err := sendToTelegramWithKeyboard(chatID, q.prompt, quizKeyboard(q)); err != nil {
		log.Printf("❌ [QUIZ] Could not send quiz to chat %d: %v", chatID, err)
	}
}

// handleQuizCallback grades a quiz answer tap (Change E). Callback data is of the
// form "quiz:c:<word>" (correct) or "quiz:x:<word>" (wrong); the result is
// recorded and fed into the spaced-repetition schedule.
func handleQuizCallback(store *Store, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "quiz:")
	cx, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = answerCallbackQuery(cb.ID, "")
		return
	}
	correct := cx == "c"

	if err := store.RecordQuizResult(chatID, word, correct); err != nil {
		log.Printf("⚠️  [QUIZ] Could not record result for chat %d word %q: %v", chatID, word, err)
	}

	// Feed the spaced-repetition schedule: correct promotes, wrong resets.
	now := time.Now()
	if correct {
		_, _ = store.ApplyReviewKnown(chatID, word, now)
	} else {
		_, _ = store.ApplyReviewForgot(chatID, word, now)
	}

	log.Printf("🧩 [QUIZ] ChatID %d answered %q: correct=%v.", chatID, word, correct)

	meaning := store.MeaningForWord(word)
	var toast, reveal string
	if correct {
		toast = "Correct! ✅"
		reveal = fmt.Sprintf("✅ <b>Correct!</b>\n\n<b>%s</b>", word)
	} else {
		toast = "Not quite — review it once more"
		reveal = fmt.Sprintf("❌ <b>Not quite.</b>\n\nThe answer was <b>%s</b>", word)
	}
	if meaning != "" {
		reveal += fmt.Sprintf(" — %s", meaning)
	}

	_ = answerCallbackQuery(cb.ID, toast)
	if cb.Message != nil {
		_ = editMessageText(chatID, cb.Message.MessageID, reveal, [][]inlineButton{})
	}
}

// ---------------------------------------------------------------------------
// Quiz scheduler goroutine
// ---------------------------------------------------------------------------

// runQuizScheduler periodically sends one quiz to each eligible active subscriber
// (quiet-hour and paused aware). Disabled when QUIZ_INTERVAL <= 0.
func runQuizScheduler(ctx context.Context, store *Store) {
	if quizInterval <= 0 {
		log.Println("🧩 [QUIZ] Quiz scheduler disabled (QUIZ_INTERVAL <= 0).")
		return
	}
	log.Printf("🧩 [QUIZ] Quiz scheduler started (every %s).", quizInterval)
	ticker := time.NewTicker(quizInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🧩 [QUIZ] Quiz scheduler stopped.")
			return
		case <-ticker.C:
			runQuizSweep(store, time.Now())
		}
	}
}

// runQuizSweep sends a quiz to every eligible subscriber for the current moment.
func runQuizSweep(store *Store, now time.Time) {
	if isQuietHours(now) {
		return
	}
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [QUIZ] Could not read subscribers: %v", err)
		return
	}
	rng := newRand()
	sent := 0
	for _, chatID := range chats {
		if store.IsPaused(chatID) {
			continue
		}
		q, ok, err := makeQuiz(store, chatID, now, rng)
		if err != nil {
			log.Printf("⚠️  [QUIZ] Build failed for chat %d: %v", chatID, err)
			continue
		}
		if !ok {
			continue
		}
		if err := sendToTelegramWithKeyboard(chatID, q.prompt, quizKeyboard(q)); err != nil {
			log.Printf("❌ [QUIZ] Send to chat %d failed: %v", chatID, err)
			continue
		}
		sent++
	}
	if sent > 0 {
		log.Printf("🧩 [QUIZ] Sweep complete: %d quiz(zes) delivered.", sent)
	}
}
