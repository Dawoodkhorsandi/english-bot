package app

import (
	"testing"
	"time"
)

func TestComputeAnalytics_EmptyUser(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(4001)

	analytics, err := store.ComputeAnalytics(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if analytics == nil {
		t.Fatal("expected non-nil analytics")
	}
	// Empty user should have empty slices (or nil).
	if len(analytics.WordBreakdown) != 0 {
		t.Errorf("expected empty word_breakdown, got %d", len(analytics.WordBreakdown))
	}
}

func TestComputeAnalytics_WordBreakdown(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(4002)

	// Insert words at different levels via content_pool.
	_, _ = store.db.Exec("INSERT OR IGNORE INTO content_pool (kind, term, meaning, text, level) VALUES ('word', 'alpha', 'a', 'a', 'beginner')")
	_, _ = store.db.Exec("INSERT OR IGNORE INTO content_pool (kind, term, meaning, text, level) VALUES ('word', 'beta', 'b', 'b', 'intermediate')")
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, 'alpha')", chatID)
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, 'beta')", chatID)
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, 'beta')", chatID) // duplicate ignored

	analytics, err := store.ComputeAnalytics(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(analytics.WordBreakdown) == 0 {
		t.Fatal("expected word breakdown data")
	}
	total := 0
	for _, b := range analytics.WordBreakdown {
		total += b.Count
	}
	if total != 2 {
		t.Errorf("expected total 2 words in breakdown, got %d", total)
	}
}

func TestComputeAnalytics_ContentDiversity(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(4003)

	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, 'w1')", chatID)
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_words (chat_id, word) VALUES (?, 'd1')", chatID)
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_idioms (chat_id, word) VALUES (?, 'i1')", chatID)

	analytics, err := store.ComputeAnalytics(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(analytics.ContentDiversity) != 3 {
		t.Errorf("expected 3 content types, got %d", len(analytics.ContentDiversity))
	}
	// Should be sorted by count descending.
	if len(analytics.ContentDiversity) >= 2 {
		for i := 1; i < len(analytics.ContentDiversity); i++ {
			if analytics.ContentDiversity[i].Count > analytics.ContentDiversity[i-1].Count {
				t.Error("content_diversity should be sorted by count descending")
			}
		}
	}
}

func TestComputeAnalytics_ActivityByHour(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(4004)

	// Insert a word sent at a specific hour (14:00 Tehran = 10:30 UTC roughly).
	_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word, sent_at) VALUES (?, 'w1', '2025-01-15 10:30:00')", chatID)

	analytics, err := store.ComputeAnalytics(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(analytics.ActivityByHour) != 24 {
		t.Errorf("expected 24 hour buckets, got %d", len(analytics.ActivityByHour))
	}
	// The word was sent at 10:30 UTC which is ~14:00 Tehran (UTC+3:30).
	total := 0
	for _, h := range analytics.ActivityByHour {
		total += h.Count
	}
	if total < 1 {
		t.Error("expected at least 1 activity in hour buckets")
	}
}

func TestComputeAnalytics_QuizAccuracyTrend(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(4005)

	now := time.Now().Format("2006-01-02 15:04:05")
	_, _ = store.db.Exec("INSERT INTO quiz_results (chat_id, word, correct, answered_at) VALUES (?, 'w1', 1, ?)", chatID, now)
	_, _ = store.db.Exec("INSERT INTO quiz_results (chat_id, word, correct, answered_at) VALUES (?, 'w2', 0, ?)", chatID, now)

	analytics, err := store.ComputeAnalytics(chatID)
	if err != nil {
		t.Fatal(err)
	}
	if len(analytics.QuizAccuracyTrend) != 1 {
		t.Fatalf("expected 1 day in quiz trend, got %d", len(analytics.QuizAccuracyTrend))
	}
	da := analytics.QuizAccuracyTrend[0]
	if da.Total != 2 || da.Correct != 1 || da.Pct != 50 {
		t.Errorf("expected (1/2, 50%%), got (%d/%d, %d%%)", da.Correct, da.Total, da.Pct)
	}
}
