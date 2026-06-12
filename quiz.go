package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/content"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
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
			trimmed := strings.TrimSpace(content.StripHTMLTags(lines[j]))
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
			return content.StripHTMLTags(replaced), true
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
func quizKeyboard(q quizQuestion) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, len(q.options))
	for i, opt := range q.options {
		cx := "x"
		if i == q.correctIdx {
			cx = "c"
		}
		rows = append(rows, []telegram.InlineButton{{Text: opt, CallbackData: "quiz:" + cx + ":" + q.word}})
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
	if err != nil {
		return err
	}
	// Answering a quiz counts as a learning day for the streak (quiz answers
	// leave no sent_* footprint of their own). Best-effort.
	return s.RecordActivity(chatID, time.Now())
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

// ---------------------------------------------------------------------------
// Native Telegram poll quiz
// ---------------------------------------------------------------------------

// pendingQuizPoll maps a Telegram poll_id to the context needed to grade it.
type pendingQuizPoll struct {
	chatID     int64
	word       string
	correctIdx int
}

var (
	quizPollMu       sync.Mutex
	pendingQuizPolls = make(map[string]pendingQuizPoll)
)

func storePendingPoll(pollID string, p pendingQuizPoll) {
	quizPollMu.Lock()
	pendingQuizPolls[pollID] = p
	quizPollMu.Unlock()
}

func popPendingPoll(pollID string) (pendingQuizPoll, bool) {
	quizPollMu.Lock()
	defer quizPollMu.Unlock()
	p, ok := pendingQuizPolls[pollID]
	if ok {
		delete(pendingQuizPolls, pollID)
	}
	return p, ok
}

// sendQuizAsPoll delivers q using Telegram's native quiz poll UI.
// Falls back to inline keyboard if SendPoll fails.
func sendQuizAsPoll(store *Store, notifier telegram.Notifier, chatID int64, q quizQuestion) {
	meaning := store.MeaningForWord(q.word)
	explanation := q.word
	if meaning != "" {
		explanation = fmt.Sprintf("%s — %s", q.word, meaning)
	}

	pollID, err := notifier.SendPoll(chatID, q.prompt, q.options, q.correctIdx, explanation)
	if err != nil {
		log.Printf("⚠️  [QUIZ] Poll send failed for chat %d, falling back to keyboard: %v", chatID, err)
		_ = notifier.SendKeyboard(chatID, q.prompt, quizKeyboard(q))
		return
	}
	storePendingPoll(pollID, pendingQuizPoll{chatID: chatID, word: q.word, correctIdx: q.correctIdx})
}

// handleQuizPollAnswer grades a poll_answer update from Telegram.
func handleQuizPollAnswer(store *Store, pa *telegram.PollAnswer) {
	p, ok := popPendingPoll(pa.PollID)
	if !ok {
		return
	}
	correct := len(pa.OptionIDs) > 0 && pa.OptionIDs[0] == p.correctIdx
	if err := store.RecordQuizResult(p.chatID, p.word, correct); err != nil {
		log.Printf("⚠️  [QUIZ] Could not record poll result for chat %d word %q: %v", p.chatID, p.word, err)
	}
	now := time.Now()
	if correct {
		_, _ = store.ApplyReviewKnown(p.chatID, p.word, now)
	} else {
		_, _ = store.ApplyReviewForgot(p.chatID, p.word, now)
	}
	log.Printf("🧩 [QUIZ] ChatID %d answered poll for %q: correct=%v.", p.chatID, p.word, correct)
}

// handleQuiz sends one quiz question on demand (/quiz).
func handleQuiz(store *Store, notifier telegram.Notifier, chatID int64) {
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
	sendQuizAsPoll(store, notifier, chatID, q)
}

// handleQuizCallback grades a quiz answer tap (Change E). Callback data is of the
// form "quiz:c:<word>" (correct) or "quiz:x:<word>" (wrong); the result is
// recorded and fed into the spaced-repetition schedule.
func handleQuizCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
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
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, reveal, [][]telegram.InlineButton{})
	}
}

// ---------------------------------------------------------------------------
// Quiz scheduler goroutine
// ---------------------------------------------------------------------------

// nextTopOfHour returns the next top-of-hour boundary after t (in config.AppLocation).
func nextTopOfHour(t time.Time) time.Time {
	local := t.In(config.AppLocation)
	truncated := local.Truncate(time.Hour)
	next := truncated.Add(time.Hour)
	if !next.After(local) {
		next = next.Add(time.Hour)
	}
	return next
}

// quizDueForUser reports whether a user with the given quiz interval (hours) is
// due for a quiz at time t. Uses wall-clock hour alignment so restarts and
// quiet-hour skips never desync delivery.
func quizDueForUser(t time.Time, intervalHours int) bool {
	if intervalHours <= 0 {
		return false
	}
	h := t.In(config.AppLocation).Hour()
	return h%intervalHours == 0
}

// runQuizScheduler ticks once per hour (wall-clock aligned) and delivers a quiz
// to each eligible user whose per-user quiz interval is due this hour.
// Disabled globally when QUIZ_INTERVAL <= 0 (admin kill-switch).
func runQuizScheduler(ctx context.Context, store *Store, notifier telegram.Notifier) {
	if config.QuizInterval <= 0 {
		log.Println("🧩 [QUIZ] Quiz scheduler disabled (QUIZ_INTERVAL <= 0).")
		return
	}
	log.Println("🧩 [QUIZ] Quiz scheduler started (hourly wall-clock sweep, per-user interval).")
	for {
		next := nextTopOfHour(time.Now())
		wait := time.Until(next)
		log.Printf("🧩 [QUIZ] Next quiz sweep at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("🧩 [QUIZ] Quiz scheduler stopped.")
			return
		case <-time.After(wait):
		}

		runQuizSweep(store, notifier, time.Now())
	}
}

// runQuizSweep sends a quiz to every eligible subscriber whose per-user quiz
// interval aligns with the current hour.
func runQuizSweep(store *Store, notifier telegram.Notifier, now time.Time) {
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
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [QUIZ] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.QuizEnabled {
			continue
		}
		if !quizDueForUser(now, prefs.QuizIntervalHours) {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
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
		sendQuizAsPoll(store, notifier, chatID, q)
		sent++
	}
	if sent > 0 {
		log.Printf("🧩 [QUIZ] Sweep complete: %d quiz(zes) delivered.", sent)
	}
}
