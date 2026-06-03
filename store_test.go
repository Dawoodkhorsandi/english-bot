package main

import (
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := openStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// pool.go Store methods
// ---------------------------------------------------------------------------

func TestStorePoolCount(t *testing.T) {
	s := testStore(t)

	n, err := s.PoolCount(kindDrill, defaultLevel)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	s.AddToPool(kindDrill, defaultLevel, "run", "to move quickly", "drill text")
	s.AddToPool(kindDrill, defaultLevel, "jump", "to leap", "drill text 2")

	n, err = s.PoolCount(kindDrill, defaultLevel)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestStorePoolTerms(t *testing.T) {
	s := testStore(t)

	s.AddToPool(kindDrill, defaultLevel, "run", "m1", "t1")
	s.AddToPool(kindDrill, "beginner", "walk", "m2", "t2")
	s.AddToPool(kindWord, defaultLevel, "apple", "m3", "t3")

	terms, err := s.PoolTerms(kindDrill)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 2 {
		t.Fatalf("expected 2 drill terms, got %d", len(terms))
	}

	terms, err = s.PoolTerms(kindWord)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 {
		t.Fatalf("expected 1 word term, got %d", len(terms))
	}
}

func TestStoreAddToPoolIdempotent(t *testing.T) {
	s := testStore(t)

	if err := s.AddToPool(kindDrill, defaultLevel, "run", "m1", "t1"); err != nil {
		t.Fatal(err)
	}
	// duplicate (kind, term) should be ignored
	if err := s.AddToPool(kindDrill, "beginner", "run", "m2", "t2"); err != nil {
		t.Fatal(err)
	}

	terms, _ := s.PoolTerms(kindDrill)
	if len(terms) != 1 {
		t.Fatalf("expected 1 (idempotent), got %d", len(terms))
	}
}

func TestStorePooledUnseen(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 111

	s.AddToPool(kindDrill, defaultLevel, "run", "m1", "text-run")
	s.AddToPool(kindDrill, defaultLevel, "jump", "m2", "text-jump")

	// Should return oldest unseen
	term, _, text, ok, err := s.PooledUnseen(kindDrill, defaultLevel, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || term != "run" || text != "text-run" {
		t.Fatalf("expected run, got %q ok=%v", term, ok)
	}

	// Mark "run" as seen
	s.RecordSentWord(chatID, "run")

	term, _, _, ok, err = s.PooledUnseen(kindDrill, defaultLevel, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || term != "jump" {
		t.Fatalf("expected jump after seeing run, got %q ok=%v", term, ok)
	}

	// Mark "jump" as seen too
	s.RecordSentWord(chatID, "jump")

	_, _, _, ok, err = s.PooledUnseen(kindDrill, defaultLevel, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false when all seen")
	}
}

func TestStorePooledOldest(t *testing.T) {
	s := testStore(t)

	_, _, _, ok, _ := s.PooledOldest(kindDrill, defaultLevel)
	if ok {
		t.Fatal("expected ok=false on empty pool")
	}

	s.AddToPool(kindDrill, defaultLevel, "run", "m1", "text-run")
	s.AddToPool(kindDrill, defaultLevel, "jump", "m2", "text-jump")

	term, _, _, ok, err := s.PooledOldest(kindDrill, defaultLevel)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || term != "run" {
		t.Fatalf("expected oldest=run, got %q", term)
	}
}

func TestStoreWordsSentBetween(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 222

	s.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "card")
	s.AddToPool(kindWord, defaultLevel, "banana", "yellow fruit", "card2")

	// Insert with controlled timestamps via raw SQL
	s.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", chatID, "apple", "2025-06-01 10:00:00")
	s.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", chatID, "banana", "2025-06-02 10:00:00")

	items, err := s.WordsSentBetween(chatID, "2025-06-01 00:00:00", "2025-06-02 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].term != "apple" {
		t.Fatalf("expected [apple], got %v", items)
	}

	items, err = s.WordsSentBetween(chatID, "2025-06-01 00:00:00", "2025-06-03 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestStoreReviewDelivered(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 333
	date := "2025-06-01"

	delivered, err := s.ReviewDelivered(chatID, date)
	if err != nil {
		t.Fatal(err)
	}
	if delivered {
		t.Fatal("expected not delivered initially")
	}

	if err := s.MarkReviewDelivered(chatID, date); err != nil {
		t.Fatal(err)
	}

	delivered, err = s.ReviewDelivered(chatID, date)
	if err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("expected delivered after marking")
	}
}

func TestStoreWeeklyQuizStats(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 444

	answered, correct, err := s.WeeklyQuizStats(chatID, "2025-06-01 00:00:00", "2025-06-08 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if answered != 0 || correct != 0 {
		t.Fatalf("expected 0/0, got %d/%d", answered, correct)
	}

	// Insert quiz results with controlled timestamps
	s.db.Exec("INSERT INTO quiz_results (chat_id, word, correct, answered_at) VALUES (?, ?, ?, ?)", chatID, "apple", 1, "2025-06-02 12:00:00")
	s.db.Exec("INSERT INTO quiz_results (chat_id, word, correct, answered_at) VALUES (?, ?, ?, ?)", chatID, "banana", 0, "2025-06-03 12:00:00")
	s.db.Exec("INSERT INTO quiz_results (chat_id, word, correct, answered_at) VALUES (?, ?, ?, ?)", chatID, "cherry", 1, "2025-06-10 12:00:00") // outside range

	answered, correct, err = s.WeeklyQuizStats(chatID, "2025-06-01 00:00:00", "2025-06-08 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if answered != 2 || correct != 1 {
		t.Fatalf("expected 2/1, got %d/%d", answered, correct)
	}
}

// ---------------------------------------------------------------------------
// main.go Store methods
// ---------------------------------------------------------------------------

func TestStoreAddSubscriber(t *testing.T) {
	s := testStore(t)

	isNew, err := s.AddSubscriber(100)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Fatal("expected new subscriber")
	}

	isNew, err = s.AddSubscriber(100)
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Fatal("expected duplicate to return false")
	}
}

func TestStoreSubscribers(t *testing.T) {
	s := testStore(t)

	s.AddSubscriber(10)
	s.AddSubscriber(20)
	s.AddSubscriber(30)

	subs, err := s.Subscribers()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 3 {
		t.Fatalf("expected 3 subscribers, got %d", len(subs))
	}
}

func TestStoreSentWords(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 500

	words, _ := s.SentWords(chatID)
	if len(words) != 0 {
		t.Fatalf("expected empty, got %d", len(words))
	}

	s.RecordSentWord(chatID, "run")
	s.RecordSentWord(chatID, "jump")
	// idempotent
	s.RecordSentWord(chatID, "run")

	words, err := s.SentWords(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 {
		t.Fatalf("expected 2, got %d", len(words))
	}

	n, err := s.ResetSentWords(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}

	words, _ = s.SentWords(chatID)
	if len(words) != 0 {
		t.Fatal("expected empty after reset")
	}
}

func TestStoreSentVocab(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 600

	words, _ := s.SentVocab(chatID)
	if len(words) != 0 {
		t.Fatalf("expected empty, got %d", len(words))
	}

	s.RecordSentVocab(chatID, "apple")
	s.RecordSentVocab(chatID, "banana")
	s.RecordSentVocab(chatID, "apple") // idempotent

	words, err := s.SentVocab(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 {
		t.Fatalf("expected 2, got %d", len(words))
	}

	n, err := s.ResetSentVocab(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}

	words, _ = s.SentVocab(chatID)
	if len(words) != 0 {
		t.Fatal("expected empty after reset")
	}
}

func TestStoreChangelogs(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 700

	if len(Changelogs) == 0 {
		t.Skip("no changelogs defined")
	}

	unseen, err := s.UnseenChangelogs(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unseen) != len(Changelogs) {
		t.Fatalf("expected all %d unseen, got %d", len(Changelogs), len(unseen))
	}

	// Mark first two as seen
	s.MarkChangelogSeen(chatID, Changelogs[0].Version)
	s.MarkChangelogSeen(chatID, Changelogs[1].Version)

	unseen, err = s.UnseenChangelogs(chatID)
	if err != nil {
		t.Fatal(err)
	}
	expected := len(Changelogs) - 2
	if len(unseen) != expected {
		t.Fatalf("expected %d unseen, got %d", expected, len(unseen))
	}

	// Mark all as seen
	for _, c := range Changelogs {
		s.MarkChangelogSeen(chatID, c.Version)
	}

	unseen, err = s.UnseenChangelogs(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unseen) != 0 {
		t.Fatalf("expected 0 unseen, got %d", len(unseen))
	}
}

// ---------------------------------------------------------------------------
// MeaningForWord / PooledCardText
// ---------------------------------------------------------------------------

func TestStoreMeaningForWord(t *testing.T) {
	s := testStore(t)
	s.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "card text")

	if got := s.MeaningForWord("apple"); got != "a fruit" {
		t.Errorf("MeaningForWord(apple) = %q, want %q", got, "a fruit")
	}
	if got := s.MeaningForWord("nonexistent"); got != "" {
		t.Errorf("MeaningForWord(nonexistent) = %q, want empty", got)
	}
}

func TestStorePooledCardText(t *testing.T) {
	s := testStore(t)
	s.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "full card text")

	if got := s.PooledCardText("apple"); got != "full card text" {
		t.Errorf("PooledCardText(apple) = %q, want %q", got, "full card text")
	}
	if got := s.PooledCardText("missing"); got != "" {
		t.Errorf("PooledCardText(missing) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// SnoozeReview
// ---------------------------------------------------------------------------

func TestStoreSnoozeReview(t *testing.T) {
	s := testStore(t)
	const chatID int64 = 800
	now := time.Now().UTC()

	s.AddSubscriber(chatID)
	s.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "card")
	if err := s.recordSentFor(kindWord, chatID, "apple"); err != nil {
		t.Fatalf("recordSentFor: %v", err)
	}

	// Snooze by 5 days
	if err := s.SnoozeReview(chatID, "apple", 5, now); err != nil {
		t.Fatalf("SnoozeReview: %v", err)
	}

	// Should not be due before the snooze period
	due, err := s.DueReviews(chatID, now.AddDate(0, 0, 4), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	for _, d := range due {
		if d.term == "apple" {
			t.Error("apple should not be due before snooze period ends")
		}
	}

	// Test intervalDays < 1 floors to 1
	if err := s.SnoozeReview(chatID, "apple", 0, now); err != nil {
		t.Fatalf("SnoozeReview with 0: %v", err)
	}
	due, err = s.DueReviews(chatID, now.AddDate(0, 0, 2), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	found := false
	for _, d := range due {
		if d.term == "apple" {
			found = true
		}
	}
	if !found {
		t.Error("apple should be due after floor(1) snooze + 2 days")
	}
}

// ---------------------------------------------------------------------------
// TotalQuizStats / TotalMasteredCount
// ---------------------------------------------------------------------------

func TestStoreTotalQuizStats(t *testing.T) {
	s := testStore(t)

	s.RecordQuizResult(1, "a", true)
	s.RecordQuizResult(1, "b", false)
	s.RecordQuizResult(2, "c", true)

	answered, correct, err := s.TotalQuizStats()
	if err != nil {
		t.Fatalf("TotalQuizStats: %v", err)
	}
	if answered != 3 || correct != 2 {
		t.Errorf("TotalQuizStats = %d/%d, want 3/2", answered, correct)
	}
}

func TestStoreTotalMasteredCount(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()

	// Seed reviews for 2 users
	for _, chatID := range []int64{1, 2} {
		s.AddSubscriber(chatID)
		s.AddToPool(kindWord, defaultLevel, "word1", "m1", "t1")
		s.AddToPool(kindWord, defaultLevel, "word2", "m2", "t2")
		s.recordSentFor(kindWord, chatID, "word1")
		s.recordSentFor(kindWord, chatID, "word2")
	}

	// Promote word1 for user 1 past mastery threshold (interval >= 21)
	s.db.Exec("UPDATE review_schedule SET interval_days = 21 WHERE chat_id = 1 AND word = 'word1'")
	// Promote word2 for user 2 past mastery
	s.db.Exec("UPDATE review_schedule SET interval_days = 25 WHERE chat_id = 2 AND word = 'word2'")
	_ = now

	n, err := s.TotalMasteredCount()
	if err != nil {
		t.Fatalf("TotalMasteredCount: %v", err)
	}
	if n != 2 {
		t.Errorf("TotalMasteredCount = %d, want 2", n)
	}
}
