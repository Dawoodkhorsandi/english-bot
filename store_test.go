package main

import (
	"context"
	"database/sql"
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

func TestStoreDrillText(t *testing.T) {
	s := testStore(t)

	// Miss when the verb is not pooled.
	if _, ok, err := s.DrillText("walk"); err != nil || ok {
		t.Fatalf("expected miss, got ok=%v err=%v", ok, err)
	}

	s.AddToPool(kindDrill, defaultLevel, "Walk", "", "full drill for walk")
	s.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "word card")

	// Hit is case-insensitive (terms are normalized to lowercase).
	text, ok, err := s.DrillText("WALK")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || text != "full drill for walk" {
		t.Fatalf("got ok=%v text=%q, want hit with drill text", ok, text)
	}

	// A word with the same spelling must not be returned as a drill.
	if _, ok, _ := s.DrillText("apple"); ok {
		t.Error("DrillText should only match kind=drill rows")
	}
}

func TestStoreSentIdioms(t *testing.T) {
	s := testStore(t)

	// Recording is case-insensitive (normalized to lowercase).
	if err := s.RecordSentIdiom(100, "Break The Ice"); err != nil {
		t.Fatal(err)
	}

	s.AddToPool(kindIdiom, defaultLevel, "break the ice", "m1", "card1")
	s.AddToPool(kindIdiom, defaultLevel, "piece of cake", "m2", "card2")

	// PooledUnseen must skip the already-sent idiom via the sent_idioms table.
	term, _, _, ok, err := s.PooledUnseen(kindIdiom, defaultLevel, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || term != "piece of cake" {
		t.Fatalf("expected unseen 'piece of cake', got ok=%v term=%q", ok, term)
	}

	n, err := s.ResetSentIdiom(100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ResetSentIdiom removed %d, want 1", n)
	}
}

func TestStorePoolTerms(t *testing.T) {
	s := testStore(t)

	s.AddToPool(kindDrill, defaultLevel, "run", "m1", "t1")
	s.AddToPool(kindDrill, "beginner", "walk", "m2", "t2")
	s.AddToPool(kindWord, defaultLevel, "apple", "m3", "t3")

	// PoolTerms is scoped per (kind, level).
	terms, err := s.PoolTerms(kindDrill, defaultLevel)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 || terms[0] != "run" {
		t.Fatalf("expected [run] at default level, got %v", terms)
	}

	terms, err = s.PoolTerms(kindDrill, "beginner")
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 || terms[0] != "walk" {
		t.Fatalf("expected [walk] at beginner level, got %v", terms)
	}

	terms, err = s.PoolTerms(kindWord, defaultLevel)
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
	// A duplicate (kind, level, term) is ignored.
	if err := s.AddToPool(kindDrill, defaultLevel, "run", "m1b", "t1b"); err != nil {
		t.Fatal(err)
	}
	terms, _ := s.PoolTerms(kindDrill, defaultLevel)
	if len(terms) != 1 {
		t.Fatalf("expected 1 (idempotent within level), got %d", len(terms))
	}

	// The same term at a different level is now allowed (per-level dedup).
	if err := s.AddToPool(kindDrill, "beginner", "run", "m2", "t2"); err != nil {
		t.Fatal(err)
	}
	terms, _ = s.PoolTerms(kindDrill, "beginner")
	if len(terms) != 1 {
		t.Fatalf("expected run also pooled at beginner level, got %d", len(terms))
	}
}

func TestStorePooledUnseen(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 111

	s.AddToPool(kindDrill, defaultLevel, "run", "m1", "text-run")
	s.AddToPool(kindDrill, defaultLevel, "jump", "m2", "text-jump")

	// Selection is randomized, so the first unseen item is either run or jump.
	term, _, _, ok, err := s.PooledUnseen(kindDrill, defaultLevel, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || (term != "run" && term != "jump") {
		t.Fatalf("expected run or jump, got %q ok=%v", term, ok)
	}

	// Mark the returned term seen; the other unseen term must come next.
	first := term
	s.RecordSentWord(chatID, first)

	term, _, _, ok, err = s.PooledUnseen(kindDrill, defaultLevel, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || term == first {
		t.Fatalf("expected the other unseen term after seeing %q, got %q ok=%v", first, term, ok)
	}

	// Mark it seen too — now nothing is unseen.
	s.RecordSentWord(chatID, term)

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

// TestMigrateUpgradesPreV13Schema simulates a pre-v1.13 database — content_pool
// with the old global UNIQUE(kind, term) and sent tables without last_sent_at — and
// verifies openStore's migration rebuilds it to per-level dedup and backfills
// last_sent_at without losing data.
func TestMigrateUpgradesPreV13Schema(t *testing.T) {
	path := t.TempDir() + "/legacy.db"

	// Build the old schema by hand and seed a row.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	_, err = raw.Exec(`
		CREATE TABLE content_pool (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			kind       TEXT    NOT NULL,
			term       TEXT    NOT NULL,
			meaning    TEXT    DEFAULT '',
			text       TEXT    NOT NULL,
			level      TEXT    NOT NULL DEFAULT 'intermediate',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (kind, term)
		);
		CREATE TABLE sent_words (
			chat_id INTEGER NOT NULL,
			word    TEXT    NOT NULL,
			sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (chat_id, word)
		);
		INSERT INTO content_pool (kind, term, meaning, text, level) VALUES ('drill', 'run', '', 'text-run', 'intermediate');
		INSERT INTO sent_words (chat_id, word, sent_at) VALUES (42, 'run', '2026-01-01 08:00:00');
	`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	raw.Close()

	// openStore runs migrate() on the existing file.
	store, err := openStore(path)
	if err != nil {
		t.Fatalf("openStore (migrate): %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Per-level dedup is now in force: the same term may be pooled at another level.
	if !store.poolDedupIsLevelAware() {
		t.Fatal("expected content_pool to be level-aware after migration")
	}
	if err := store.AddToPool(kindDrill, "beginner", "run", "", "text-run-beginner"); err != nil {
		t.Fatalf("AddToPool same term, other level: %v", err)
	}
	if terms, _ := store.PoolTerms(kindDrill, "beginner"); len(terms) != 1 {
		t.Fatalf("expected run pooled at beginner level post-migration, got %v", terms)
	}
	// The pre-existing row survived.
	if terms, _ := store.PoolTerms(kindDrill, defaultLevel); len(terms) != 1 || terms[0] != "run" {
		t.Fatalf("expected original run preserved at default level, got %v", terms)
	}

	// last_sent_at was added and backfilled from sent_at for the existing row.
	if !store.columnExists("sent_words", "last_sent_at") {
		t.Fatal("expected sent_words.last_sent_at after migration")
	}
	// Backfilled value must equal sent_at. The driver may reformat the datetime on
	// read (e.g. RFC3339), so compare the two columns directly rather than a literal.
	var matches int
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM sent_words WHERE chat_id = 42 AND word = 'run' AND last_sent_at = sent_at",
	).Scan(&matches); err != nil {
		t.Fatalf("read last_sent_at: %v", err)
	}
	if matches != 1 {
		t.Fatal("expected last_sent_at to be backfilled equal to sent_at")
	}
}

// TestServeContentRotatesWhenExhausted is the regression test for the "same drill
// every hour" bug: once a user has seen every pooled item at their level, the
// broadcast fallback (allowGenerate=false) must rotate through the pool instead of
// pinning the single oldest item forever. The guarantee we assert is that no two
// consecutive broadcast serves return the same item.
func TestServeContentRotatesWhenExhausted(t *testing.T) {
	s := testStore(t)
	var chatID int64 = 777
	s.AddSubscriber(chatID)

	// Distinct texts so we can detect repeats by content.
	s.AddToPool(kindDrill, defaultLevel, "run", "", "text-run")
	s.AddToPool(kindDrill, defaultLevel, "jump", "", "text-jump")
	s.AddToPool(kindDrill, defaultLevel, "swim", "", "text-swim")

	ctx := context.Background()
	chain := emptyProviderChain() // never used: allowGenerate=false stays pool-only

	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 30; i++ {
		text, err := serveContent(ctx, chain, s, chatID, kindDrill, defaultLevel, false)
		if err != nil {
			t.Fatalf("serveContent iteration %d: %v", i, err)
		}
		if text == "" {
			t.Fatalf("iteration %d: empty text", i)
		}
		if text == prev {
			t.Fatalf("iteration %d: same item served twice in a row (%q) — rotation broken", i, text)
		}
		seen[text] = true
		prev = text
	}

	// Over 30 serves of a 3-item pool, rotation must surface every item.
	if len(seen) != 3 {
		t.Fatalf("expected all 3 pooled drills to be served over time, got %d distinct: %v", len(seen), seen)
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
