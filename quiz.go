package main

import (
	"context"
	"database/sql"
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
	quizTypeSynonym       = "syn" // "Pick the synonym of WORD" → word options
	quizTypeFillBlank     = "fib" // Fill-in-the-blank sentence → word options
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

// ---------------------------------------------------------------------------
// Synonym quiz helpers (Change E extension)
// ---------------------------------------------------------------------------

// parseSynonyms extracts the comma-separated synonyms from a vocabulary card's
// HTML text. Returns nil if the synonyms section is not found.
func parseSynonyms(cardText string) []string {
	lines := strings.Split(cardText, "\n")
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), "synonym") {
			continue
		}
		// The synonyms are on the next non-empty line after the header.
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(stripHTMLTags(lines[j]))
			if trimmed == "" {
				continue
			}
			parts := strings.Split(trimmed, ",")
			var syns []string
			for _, p := range parts {
				s := strings.TrimSpace(p)
				if s != "" {
					syns = append(syns, s)
				}
			}
			if len(syns) > 0 {
				return syns
			}
			return nil
		}
	}
	return nil
}

// buildSynonymQuiz assembles a "Pick the synonym of WORD" question. The correct
// answer is one of the synonyms parsed from the card text; distractors are
// random pool terms that are NOT synonyms of the subject.
func buildSynonymQuiz(subject reviewItem, distractPool []reviewItem, cardText string, rng *rand.Rand) (quizQuestion, bool) {
	syns := parseSynonyms(cardText)
	if len(syns) == 0 {
		return quizQuestion{}, false
	}
	correctSyn := syns[rng.Intn(len(syns))]

	// Exclude the subject and all its synonyms from the distractor pool.
	synSet := make(map[string]bool)
	for _, s := range syns {
		synSet[strings.ToLower(s)] = true
	}
	synSet[strings.ToLower(subject.term)] = true

	shuffled := append([]reviewItem(nil), distractPool...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	used := map[string]bool{strings.ToLower(correctSyn): true}
	var options []string
	for _, it := range shuffled {
		key := strings.ToLower(it.term)
		if synSet[key] || used[key] {
			continue
		}
		used[key] = true
		options = append(options, it.term)
		if len(options) == quizOptionCount-1 {
			break
		}
	}
	if len(options) < quizOptionCount-1 {
		return quizQuestion{}, false
	}

	options = append(options, correctSyn)
	rng.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	correctIdx := 0
	for i, o := range options {
		if o == correctSyn {
			correctIdx = i
			break
		}
	}

	prompt := fmt.Sprintf("🧩 <b>Quiz</b>\n\nPick the synonym of <b>%s</b>:", subject.term)
	return quizQuestion{word: subject.term, prompt: prompt, options: options, correctIdx: correctIdx}, true
}

// ---------------------------------------------------------------------------
// Fill-in-the-blank quiz helpers (Change E extension)
// ---------------------------------------------------------------------------

// parseExampleForBlank extracts an example sentence from a vocabulary card's
// HTML text and replaces the bolded target word with "____". Returns ok=false
// if no suitable example with a bolded word is found.
func parseExampleForBlank(cardText string) (blanked string, ok bool) {
	lines := strings.Split(cardText, "\n")
	inExamples := false
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "example") && strings.Contains(line, "<b>") {
			inExamples = true
			continue
		}
		if inExamples {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "•") {
				if trimmed == "" {
					continue
				}
				break // hit a non-example, non-empty line
			}
			sentence := strings.TrimSpace(strings.TrimPrefix(trimmed, "•"))
			replaced := blankBoldedWords(sentence)
			if replaced == sentence {
				continue // no bold word found in this example
			}
			return stripHTMLTags(replaced), true
		}
	}
	return "", false
}

// blankBoldedWords replaces all <b>…</b> spans in s with "____".
func blankBoldedWords(s string) string {
	for {
		start := strings.Index(s, "<b>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</b>")
		if end == -1 {
			break
		}
		s = s[:start] + "____" + s[start+end+4:]
	}
	return s
}

// buildFillBlankQuiz assembles a fill-in-the-blank question using an example
// sentence from the card text with the target word blanked out.
func buildFillBlankQuiz(subject reviewItem, distractPool []reviewItem, cardText string, rng *rand.Rand) (quizQuestion, bool) {
	blanked, ok := parseExampleForBlank(cardText)
	if !ok {
		return quizQuestion{}, false
	}

	used := map[string]bool{strings.ToLower(subject.term): true}
	shuffled := append([]reviewItem(nil), distractPool...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	var options []string
	for _, it := range shuffled {
		key := strings.ToLower(it.term)
		if used[key] {
			continue
		}
		used[key] = true
		options = append(options, it.term)
		if len(options) == quizOptionCount-1 {
			break
		}
	}
	if len(options) < quizOptionCount-1 {
		return quizQuestion{}, false
	}

	options = append(options, subject.term)
	rng.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	correctIdx := 0
	for i, o := range options {
		if o == subject.term {
			correctIdx = i
			break
		}
	}

	prompt := fmt.Sprintf("🧩 <b>Quiz</b>\n\nFill in the blank:\n\n<i>%s</i>", blanked)
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

	// Try all quiz types in random order, falling back until one works.
	allTypes := []string{quizTypeWordToMeaning, quizTypeMeaningToWord, quizTypeSynonym, quizTypeFillBlank}
	rng.Shuffle(len(allTypes), func(i, j int) { allTypes[i], allTypes[j] = allTypes[j], allTypes[i] })

	for _, qType := range allTypes {
		var q quizQuestion
		var ok bool
		switch qType {
		case quizTypeSynonym, quizTypeFillBlank:
			cardText := store.PooledCardText(subject.term)
			if cardText == "" {
				continue
			}
			if qType == quizTypeSynonym {
				q, ok = buildSynonymQuiz(subject, pool, cardText, rng)
			} else {
				q, ok = buildFillBlankQuiz(subject, pool, cardText, rng)
			}
		default:
			q, ok = buildQuiz(subject, pool, qType, rng)
		}
		if ok {
			return q, true, nil
		}
	}
	return quizQuestion{}, false, nil
}

// quizKeyboard renders one option per row; the correct option carries a "c" tag
// in its callback data, wrong ones an "x". All buttons record against the same
// subject word so a tap always grades that word.
func quizKeyboard(q quizQuestion) [][]inlineButton {
	return quizKeyboardWithPrefix(q, "quiz:")
}

func challengeQuizKeyboard(q quizQuestion) [][]inlineButton {
	return quizKeyboardWithPrefix(q, "chal:")
}

func quizKeyboardWithPrefix(q quizQuestion, prefix string) [][]inlineButton {
	rows := make([][]inlineButton, 0, len(q.options))
	for i, opt := range q.options {
		cx := "x"
		if i == q.correctIdx {
			cx = "c"
		}
		rows = append(rows, []inlineButton{{Text: opt, CallbackData: prefix + cx + ":" + q.word}})
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

// PooledCardText returns the full HTML card text for a word ("" if none).
func (s *Store) PooledCardText(word string) string {
	var text string
	_ = s.db.QueryRow(
		"SELECT text FROM content_pool WHERE kind = 'word' AND term = ?",
		strings.ToLower(strings.TrimSpace(word)),
	).Scan(&text)
	return text
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

// StartChallenge opens (or resets) a 5-question challenge session.
func (s *Store) StartChallenge(chatID int64, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO challenge_sessions (chat_id, question_index, correct_count, started_at)
		 VALUES (?, 0, 0, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		   question_index = 0,
		   correct_count = 0,
		   started_at = excluded.started_at`,
		chatID, now.UTC(),
	)
	return err
}

// GetChallenge returns the active challenge session state for a user.
func (s *Store) GetChallenge(chatID int64) (index, correct int, active bool, err error) {
	err = s.db.QueryRow(
		"SELECT question_index, correct_count FROM challenge_sessions WHERE chat_id = ?",
		chatID,
	).Scan(&index, &correct)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return index, correct, true, nil
}

// AdvanceChallenge records one answer and advances the question index.
func (s *Store) AdvanceChallenge(chatID int64, correct bool) (newIndex, totalCorrect int, done bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()

	var index, count int
	if err := tx.QueryRow(
		"SELECT question_index, correct_count FROM challenge_sessions WHERE chat_id = ?",
		chatID,
	).Scan(&index, &count); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	if correct {
		count++
	}
	index++
	if _, err := tx.Exec(
		"UPDATE challenge_sessions SET question_index = ?, correct_count = ? WHERE chat_id = ?",
		index, count, chatID,
	); err != nil {
		return 0, 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	return index, count, index >= challengeQuestionCount, nil
}

// ClearChallenge clears an active challenge session.
func (s *Store) ClearChallenge(chatID int64) error {
	_, err := s.db.Exec("DELETE FROM challenge_sessions WHERE chat_id = ?", chatID)
	return err
}

// RecordChallengeCompletion increments completion count and best score.
func (s *Store) RecordChallengeCompletion(chatID int64, score int) error {
	_, err := s.db.Exec(
		`INSERT INTO user_prefs (chat_id, challenge_completed, best_challenge_score)
		 VALUES (?, 1, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		   challenge_completed = challenge_completed + 1,
		   best_challenge_score = MAX(best_challenge_score, excluded.best_challenge_score),
		   updated_at = CURRENT_TIMESTAMP`,
		chatID, score,
	)
	return err
}

// ChallengeStats returns completed challenge count and best score (out of 5).
func (s *Store) ChallengeStats(chatID int64) (completed, best int, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(challenge_completed, 0), COALESCE(best_challenge_score, 0)
		 FROM user_prefs WHERE chat_id = ?`,
		chatID,
	).Scan(&completed, &best)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return completed, best, err
}

// ---------------------------------------------------------------------------
// Command + callback handlers
// ---------------------------------------------------------------------------

// handleQuiz sends one quiz question on demand (/quiz).
func handleQuiz(store *Store, notifier Notifier, chatID int64) {
	q, ok, err := makeQuiz(store, chatID, time.Now(), newRand())
	if err != nil {
		log.Printf("❌ [QUIZ] Could not build quiz for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't make a quiz right now. Please try again.")
		return
	}
	if !ok {
		_ = notifier.Send(chatID, "🧩 Not enough learned words for a quiz yet — keep practising and I'll test you soon!")
		return
	}
	if err := notifier.SendKeyboard(chatID, q.prompt, quizKeyboard(q)); err != nil {
		log.Printf("❌ [QUIZ] Could not send quiz to chat %d: %v", chatID, err)
	}
}

// handleChallenge starts a rapid 5-question challenge session.
func handleChallenge(store *Store, notifier Notifier, chatID int64) {
	_, _, active, err := store.GetChallenge(chatID)
	if err != nil {
		log.Printf("❌ [CHALLENGE] Could not read session for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't start a challenge right now. Please try again.")
		return
	}
	if active {
		_ = notifier.Send(chatID, "🏁 You already have an active challenge. Finish it first!")
		return
	}
	if err := store.StartChallenge(chatID, time.Now()); err != nil {
		log.Printf("❌ [CHALLENGE] Could not start challenge for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't start a challenge right now. Please try again.")
		return
	}
	q, ok, err := makeQuiz(store, chatID, time.Now(), newRand())
	if err != nil {
		log.Printf("❌ [CHALLENGE] Could not build first question for chat %d: %v", chatID, err)
		_ = store.ClearChallenge(chatID)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't make challenge questions right now. Please try again.")
		return
	}
	if !ok {
		_ = store.ClearChallenge(chatID)
		_ = notifier.Send(chatID, "🧩 Not enough learned words for a challenge yet — keep practising and try again soon!")
		return
	}
	msg := fmt.Sprintf("🏁 <b>Grammar Challenge</b>\n\nQuestion 1/%d\n\n%s", challengeQuestionCount, q.prompt)
	if err := notifier.SendKeyboard(chatID, msg, challengeQuizKeyboard(q)); err != nil {
		log.Printf("❌ [CHALLENGE] Could not send first question to chat %d: %v", chatID, err)
	}
}

// handleQuizCallback grades a quiz answer tap (Change E). Callback data is of the
// form "quiz:c:<word>" (correct) or "quiz:x:<word>" (wrong); the result is
// recorded and fed into the spaced-repetition schedule.
func handleQuizCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "quiz:")
	cx, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = notifier.AnswerCallback(cb.ID, "")
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

	_ = notifier.AnswerCallback(cb.ID, toast)
	if cb.Message != nil {
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, reveal, [][]inlineButton{})
	}
}

func challengeSummary(score int) string {
	switch score {
	case challengeQuestionCount:
		return "🏆 Perfect score! You're on fire!"
	case 4:
		return "⭐ Excellent! Almost perfect!"
	case 3:
		return "👍 Good effort! Keep practicing!"
	case 2:
		return "💪 Keep going — you're improving!"
	case 1, 0:
		return "🌱 Every expert was once a beginner. Try again!"
	default:
		return "🌱 Every expert was once a beginner. Try again!"
	}
}

// handleChallengeCallback grades one challenge answer, advances the session, and
// immediately sends the next question (or final summary at 5/5).
func handleChallengeCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "chal:")
	cx, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}
	correct := cx == "c"
	if err := store.RecordQuizResult(chatID, word, correct); err != nil {
		log.Printf("⚠️  [CHALLENGE] Could not record result for chat %d word %q: %v", chatID, word, err)
	}

	now := time.Now()
	if correct {
		_, _ = store.ApplyReviewKnown(chatID, word, now)
	} else {
		_, _ = store.ApplyReviewForgot(chatID, word, now)
	}
	newIndex, totalCorrect, done, err := store.AdvanceChallenge(chatID, correct)
	if err != nil {
		log.Printf("❌ [CHALLENGE] Could not advance challenge for chat %d: %v", chatID, err)
		_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
		return
	}
	if newIndex == 0 && !done {
		_ = notifier.AnswerCallback(cb.ID, "Challenge expired")
		return
	}

	toast := "Not quite — keep going!"
	if correct {
		toast = "Correct! ✅"
	}
	_ = notifier.AnswerCallback(cb.ID, toast)
	if cb.Message != nil {
		reveal := "❌ <b>Not quite.</b>"
		if correct {
			reveal = "✅ <b>Correct!</b>"
		}
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, reveal, [][]inlineButton{})
	}

	if done {
		if err := store.RecordChallengeCompletion(chatID, totalCorrect); err != nil {
			log.Printf("⚠️  [CHALLENGE] Could not persist completion for chat %d: %v", chatID, err)
		}
		if err := store.ClearChallenge(chatID); err != nil {
			log.Printf("⚠️  [CHALLENGE] Could not clear session for chat %d: %v", chatID, err)
		}
		_ = notifier.Send(chatID, fmt.Sprintf("🏁 <b>Challenge complete!</b>\n\nScore: <b>%d/%d</b>\n%s", totalCorrect, challengeQuestionCount, challengeSummary(totalCorrect)))
		return
	}

	q, ok, err := makeQuiz(store, chatID, time.Now(), newRand())
	if err != nil {
		log.Printf("❌ [CHALLENGE] Could not build question %d for chat %d: %v", newIndex+1, chatID, err)
		_ = store.ClearChallenge(chatID)
		_ = notifier.Send(chatID, "❌ Challenge ended early due to a temporary issue. Please try /challenge again.")
		return
	}
	if !ok {
		log.Printf("⚠️  [CHALLENGE] Could not build enough material for chat %d at question %d.", chatID, newIndex+1)
		_ = store.ClearChallenge(chatID)
		_ = notifier.Send(chatID, "❌ Challenge ended early because there weren't enough quiz options. Try again after learning more words.")
		return
	}
	msg := fmt.Sprintf("🏁 <b>Grammar Challenge</b>\n\nQuestion %d/%d\n\n%s", newIndex+1, challengeQuestionCount, q.prompt)
	if err := notifier.SendKeyboard(chatID, msg, challengeQuizKeyboard(q)); err != nil {
		log.Printf("❌ [CHALLENGE] Could not send question %d to chat %d: %v", newIndex+1, chatID, err)
	}
}

// ---------------------------------------------------------------------------
// Quiz scheduler goroutine
// ---------------------------------------------------------------------------

// runQuizScheduler periodically sends one quiz to each eligible active subscriber
// (quiet-hour and paused aware). Disabled when QUIZ_INTERVAL <= 0.
func runQuizScheduler(ctx context.Context, store *Store, notifier Notifier) {
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
			runQuizSweep(store, notifier, time.Now())
		}
	}
}

// runQuizSweep sends a quiz to every eligible subscriber for the current moment.
func runQuizSweep(store *Store, notifier Notifier, now time.Time) {
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
		if err := notifier.SendKeyboard(chatID, q.prompt, quizKeyboard(q)); err != nil {
			log.Printf("❌ [QUIZ] Send to chat %d failed: %v", chatID, err)
			continue
		}
		sent++
	}
	if sent > 0 {
		log.Printf("🧩 [QUIZ] Sweep complete: %d quiz(zes) delivered.", sent)
	}
}
