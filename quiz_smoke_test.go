package main

import (
	"math/rand"
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
