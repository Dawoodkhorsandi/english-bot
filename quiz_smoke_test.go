package main

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

func fixedRand() *rand.Rand { return rand.New(rand.NewSource(1)) }

// TestBuildQuizWordToMeaning verifies a well-formed question with the correct
// answer present and indexed.
func TestBuildQuizWordToMeaning(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "too long, slow or dull"}
	pool := []reviewItem{
		{term: "reluctant", meaning: "unwilling and hesitant"},
		{term: "vigorous", meaning: "done with great energy"},
		{term: "candid", meaning: "honest and direct"},
		{term: "scarce", meaning: "in short supply"},
	}

	q, ok := buildQuiz(subject, pool, quizTypeWordToMeaning, fixedRand())
	if !ok {
		t.Fatal("buildQuiz returned ok=false with sufficient distractors")
	}
	if len(q.options) != quizOptionCount {
		t.Fatalf("got %d options, want %d", len(q.options), quizOptionCount)
	}
	if q.word != "tedious" {
		t.Errorf("subject word = %q, want tedious", q.word)
	}
	if q.options[q.correctIdx] != subject.meaning {
		t.Errorf("correct option = %q, want %q", q.options[q.correctIdx], subject.meaning)
	}
	// All options must be distinct.
	seen := map[string]bool{}
	for _, o := range q.options {
		if seen[o] {
			t.Errorf("duplicate option %q", o)
		}
		seen[o] = true
	}
}

// TestBuildQuizMeaningToWord checks the word-answer variant.
func TestBuildQuizMeaningToWord(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "too long, slow or dull"}
	pool := []reviewItem{
		{term: "reluctant", meaning: "unwilling"},
		{term: "vigorous", meaning: "energetic"},
		{term: "candid", meaning: "honest"},
	}
	q, ok := buildQuiz(subject, pool, quizTypeMeaningToWord, fixedRand())
	if !ok {
		t.Fatal("ok=false")
	}
	if q.options[q.correctIdx] != "tedious" {
		t.Errorf("correct option = %q, want tedious", q.options[q.correctIdx])
	}
}

// TestBuildQuizInsufficientDistractors verifies graceful failure.
func TestBuildQuizInsufficientDistractors(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "dull"}
	pool := []reviewItem{{term: "reluctant", meaning: "unwilling"}}
	if _, ok := buildQuiz(subject, pool, quizTypeWordToMeaning, fixedRand()); ok {
		t.Error("expected ok=false with too few distractors")
	}
}

// TestQuizResultsAndStats exercises recording and aggregation against a real DB.
func TestQuizResultsAndStats(t *testing.T) {
	store, err := openStore(t.TempDir() + "/quiz.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(99)
	for _, c := range []bool{true, true, false} {
		if err := store.RecordQuizResult(chatID, "tedious", c); err != nil {
			t.Fatalf("RecordQuizResult: %v", err)
		}
	}
	answered, correct, err := store.QuizStats(chatID)
	if err != nil {
		t.Fatalf("QuizStats: %v", err)
	}
	if answered != 3 || correct != 2 {
		t.Fatalf("QuizStats = %d/%d, want 2/3", correct, answered)
	}
}

// TestMakeQuizFromSeenWords builds a quiz end-to-end from pooled/seen data and
// confirms a correct answer promotes the word's review schedule.
func TestMakeQuizFromSeenWords(t *testing.T) {
	store, err := openStore(t.TempDir() + "/mkquiz.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(5)
	words := []struct{ term, meaning string }{
		{"tedious", "too long or dull"},
		{"reluctant", "unwilling"},
		{"vigorous", "energetic"},
		{"candid", "honest"},
	}
	for _, w := range words {
		if err := store.AddToPool(kindWord, defaultLevel, w.term, w.meaning, "card for "+w.term); err != nil {
			t.Fatalf("AddToPool: %v", err)
		}
	}
	// The user has "seen" tedious (also seeds its review schedule).
	if err := store.recordSentFor(kindWord, chatID, "tedious"); err != nil {
		t.Fatalf("recordSentFor: %v", err)
	}

	q, ok, err := makeQuiz(store, chatID, time.Now(), fixedRand())
	if err != nil {
		t.Fatalf("makeQuiz: %v", err)
	}
	if !ok {
		t.Fatal("makeQuiz ok=false despite sufficient data")
	}
	if q.word != "tedious" {
		t.Errorf("quiz subject = %q, want tedious (only seen word)", q.word)
	}

	// A correct answer should promote the word (reps go from 0 to 1).
	if _, err := store.ApplyReviewKnown(chatID, "tedious", time.Now()); err != nil {
		t.Fatalf("ApplyReviewKnown: %v", err)
	}
	_, _, reps, found, err := store.getReview(chatID, "tedious")
	if err != nil || !found {
		t.Fatalf("getReview: found=%v err=%v", found, err)
	}
	if reps != 1 {
		t.Errorf("reps = %d, want 1 after one correct answer", reps)
	}
}

// ---------------------------------------------------------------------------
// Synonym quiz tests
// ---------------------------------------------------------------------------

const sampleCardWithSynonyms = `📘 <b>Word of the Session: tedious</b>
————————————————————

💬 <b>Meaning</b>
Too long, slow, or dull; tiringly monotonous.

🔊 <b>Pronunciation</b>
TEE-dee-uhs  ·  /ˈtiː.di.əs/

✅ <b>Synonyms</b>
boring, monotonous, dull, tiresome, wearisome

⛔ <b>Opposites</b>
exciting, interesting, stimulating

📝 <b>Examples</b>
• The lecture was so <b>tedious</b> that half the audience fell asleep.
• Filing taxes is a <b>tedious</b> but necessary task.

💡 <i>Read it aloud and try using it in your own sentence today!</i>`

func TestParseSynonyms(t *testing.T) {
	syns := parseSynonyms(sampleCardWithSynonyms)
	if len(syns) == 0 {
		t.Fatal("parseSynonyms returned no synonyms")
	}
	if syns[0] != "boring" {
		t.Errorf("first synonym = %q, want boring", syns[0])
	}
	if len(syns) != 5 {
		t.Errorf("got %d synonyms, want 5", len(syns))
	}
}

func TestParseSynonymsEmpty(t *testing.T) {
	if syns := parseSynonyms("no synonyms section here"); syns != nil {
		t.Errorf("expected nil, got %v", syns)
	}
}

func TestBuildSynonymQuiz(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "too long, slow or dull"}
	pool := []reviewItem{
		{term: "reluctant", meaning: "unwilling"},
		{term: "vigorous", meaning: "energetic"},
		{term: "candid", meaning: "honest"},
		{term: "scarce", meaning: "in short supply"},
	}

	q, ok := buildSynonymQuiz(subject, pool, sampleCardWithSynonyms, fixedRand())
	if !ok {
		t.Fatal("buildSynonymQuiz returned ok=false")
	}
	if q.word != "tedious" {
		t.Errorf("subject word = %q, want tedious", q.word)
	}
	if len(q.options) != quizOptionCount {
		t.Fatalf("got %d options, want %d", len(q.options), quizOptionCount)
	}
	// The correct option should be one of the synonyms.
	correct := q.options[q.correctIdx]
	validSyns := map[string]bool{"boring": true, "monotonous": true, "dull": true, "tiresome": true, "wearisome": true}
	if !validSyns[correct] {
		t.Errorf("correct option %q is not a known synonym", correct)
	}
}

func TestBuildSynonymQuizNoSynonyms(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "dull"}
	pool := []reviewItem{{term: "reluctant", meaning: "unwilling"}}
	if _, ok := buildSynonymQuiz(subject, pool, "no synonyms", fixedRand()); ok {
		t.Error("expected ok=false with no synonyms")
	}
}

// ---------------------------------------------------------------------------
// Fill-in-the-blank quiz tests
// ---------------------------------------------------------------------------

func TestParseExampleForBlank(t *testing.T) {
	blanked, ok := parseExampleForBlank(sampleCardWithSynonyms)
	if !ok {
		t.Fatal("parseExampleForBlank returned ok=false")
	}
	if blanked == "" {
		t.Fatal("blanked sentence is empty")
	}
	if !strings.Contains(blanked, "____") {
		t.Errorf("blanked sentence does not contain ____: %q", blanked)
	}
	if strings.Contains(blanked, "<b>") || strings.Contains(blanked, "</b>") {
		t.Errorf("blanked sentence still contains HTML tags: %q", blanked)
	}
}

func TestParseExampleForBlankNoExamples(t *testing.T) {
	if _, ok := parseExampleForBlank("no examples here"); ok {
		t.Error("expected ok=false with no examples")
	}
}

func TestBuildFillBlankQuiz(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "too long, slow or dull"}
	pool := []reviewItem{
		{term: "reluctant", meaning: "unwilling"},
		{term: "vigorous", meaning: "energetic"},
		{term: "candid", meaning: "honest"},
	}

	q, ok := buildFillBlankQuiz(subject, pool, sampleCardWithSynonyms, fixedRand())
	if !ok {
		t.Fatal("buildFillBlankQuiz returned ok=false")
	}
	if q.word != "tedious" {
		t.Errorf("subject word = %q, want tedious", q.word)
	}
	if len(q.options) != quizOptionCount {
		t.Fatalf("got %d options, want %d", len(q.options), quizOptionCount)
	}
	if q.options[q.correctIdx] != "tedious" {
		t.Errorf("correct option = %q, want tedious", q.options[q.correctIdx])
	}
}

// ---------------------------------------------------------------------------
// Admin stats tests
// ---------------------------------------------------------------------------

func TestSubscriberStats(t *testing.T) {
	store, err := openStore(t.TempDir() + "/adminstats.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	store.AddSubscriber(1)
	store.AddSubscriber(2)
	store.AddSubscriber(3)
	store.SetPaused(2, true)

	total, active, paused := store.SubscriberStats()
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if active != 2 {
		t.Errorf("active = %d, want 2", active)
	}
	if paused != 1 {
		t.Errorf("paused = %d, want 1", paused)
	}
}

// ---------------------------------------------------------------------------
// Weekly digest delivery tests
// ---------------------------------------------------------------------------

func TestWeeklyDigestDelivery(t *testing.T) {
	store, err := openStore(t.TempDir() + "/digest.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(42)
	const weekStart = "2025-05-26"

	delivered, err := store.WeeklyDigestDelivered(chatID, weekStart)
	if err != nil {
		t.Fatalf("WeeklyDigestDelivered: %v", err)
	}
	if delivered {
		t.Error("expected not delivered initially")
	}

	if err := store.MarkWeeklyDigestDelivered(chatID, weekStart); err != nil {
		t.Fatalf("MarkWeeklyDigestDelivered: %v", err)
	}

	delivered, err = store.WeeklyDigestDelivered(chatID, weekStart)
	if err != nil {
		t.Fatalf("WeeklyDigestDelivered after mark: %v", err)
	}
	if !delivered {
		t.Error("expected delivered after marking")
	}
}

func TestFormatWeeklyDigestEmpty(t *testing.T) {
	msg := formatWeeklyDigest(nil, UserStats{}, 0, 0)
	if msg != "" {
		t.Errorf("expected empty message for no activity, got %q", msg)
	}
}

func TestFormatWeeklyDigestWithContent(t *testing.T) {
	words := []reviewItem{
		{term: "tedious", meaning: "too long or dull"},
		{term: "candid", meaning: "honest and direct"},
	}
	stats := UserStats{CurrentStreak: 5, Mastered: 3}
	msg := formatWeeklyDigest(words, stats, 10, 7)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(msg, "Weekly Recap") {
		t.Error("missing Weekly Recap header")
	}
	if !strings.Contains(msg, "tedious") {
		t.Error("missing word tedious")
	}
	if !strings.Contains(msg, "70%") {
		t.Error("missing quiz accuracy")
	}
	if !strings.Contains(msg, "Word of the week") {
		t.Error("missing word of the week")
	}
}

// ---------------------------------------------------------------------------
// blankBoldedWords
// ---------------------------------------------------------------------------

func TestBlankBoldedWords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"two bold words", "The <b>cat</b> sat on the <b>mat</b>.", "The ____ sat on the ____."},
		{"no bold", "No bold here.", "No bold here."},
		{"empty", "", ""},
		{"only bold", "<b>only</b>", "____"},
		{"unclosed", "<b>broken", "<b>broken"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blankBoldedWords(tt.input); got != tt.want {
				t.Errorf("blankBoldedWords(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// quizKeyboard
// ---------------------------------------------------------------------------

func TestQuizKeyboard(t *testing.T) {
	q := quizQuestion{
		word:       "tedious",
		prompt:     "What does 'tedious' mean?",
		options:    []string{"happy", "sad", "too long or dull", "fast"},
		correctIdx: 2,
	}
	rows := quizKeyboard(q)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	if !strings.HasPrefix(rows[2][0].CallbackData, "quiz:c:") {
		t.Errorf("correct row callback = %q, want prefix quiz:c:", rows[2][0].CallbackData)
	}
	for i, row := range rows {
		if i == 2 {
			continue
		}
		if !strings.HasPrefix(row[0].CallbackData, "quiz:x:") {
			t.Errorf("row %d callback = %q, want prefix quiz:x:", i, row[0].CallbackData)
		}
	}
}

// ---------------------------------------------------------------------------
// buildQuiz edge cases
// ---------------------------------------------------------------------------

func TestBuildQuizEmptyMeaning(t *testing.T) {
	subject := reviewItem{term: "test", meaning: ""}
	pool := []reviewItem{
		{term: "a", meaning: "m1"},
		{term: "b", meaning: "m2"},
		{term: "c", meaning: "m3"},
	}
	_, ok := buildQuiz(subject, pool, quizTypeWordToMeaning, fixedRand())
	if ok {
		t.Error("expected ok=false when subject meaning is empty")
	}
}

func TestBuildQuizDuplicateDistractors(t *testing.T) {
	subject := reviewItem{term: "tedious", meaning: "too long or dull"}
	pool := []reviewItem{
		{term: "a", meaning: "same meaning"},
		{term: "b", meaning: "same meaning"},
		{term: "c", meaning: "same meaning"},
		{term: "d", meaning: "unique meaning"},
		{term: "e", meaning: "another meaning"},
	}
	q, ok := buildQuiz(subject, pool, quizTypeWordToMeaning, fixedRand())
	if !ok {
		t.Fatal("buildQuiz returned ok=false")
	}
	seen := map[string]bool{}
	for _, o := range q.options {
		if seen[o] {
			t.Errorf("duplicate option %q", o)
		}
		seen[o] = true
	}
}
