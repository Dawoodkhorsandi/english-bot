package app

import (
	"testing"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// TestFsrsKnownGrowsStability verifies a correct recall grows stability (and so
// the next interval), and that intervals get longer across repeated recalls.
func TestFsrsKnownGrowsStability(t *testing.T) {
	s := fsrsInitStability(gradeGood)
	d := fsrsInitDifficulty(gradeGood)
	iv1 := fsrsNextInterval(s, defaultRetention)
	if iv1 < 1 {
		t.Fatalf("first interval = %d, want >= 1", iv1)
	}
	// A subsequent recall after the interval elapses must grow stability.
	r := fsrsRetrievability(float64(iv1), s)
	d = fsrsNextDifficulty(d, gradeGood)
	s2 := fsrsNextStabilityRecall(d, s, r)
	if s2 <= s {
		t.Fatalf("stability did not grow: %v -> %v", s, s2)
	}
	if iv2 := fsrsNextInterval(s2, defaultRetention); iv2 <= iv1 {
		t.Fatalf("interval did not grow: %d -> %d", iv1, iv2)
	}
}

// TestFsrsForgotShrinksStability verifies a lapse never increases stability.
func TestFsrsForgotShrinksStability(t *testing.T) {
	const s = 20.0
	r := fsrsRetrievability(s, s)
	sf := fsrsNextStabilityForget(fsrsNextDifficulty(6, gradeAgain), s, r)
	if sf > s {
		t.Fatalf("lapse increased stability: %v -> %v", s, sf)
	}
	if sf <= 0 {
		t.Fatalf("stability non-positive: %v", sf)
	}
}

// TestFsrsRetentionDial verifies the desired-retention dial trades reviews for
// retention: a higher target schedules the next review sooner.
func TestFsrsRetentionDial(t *testing.T) {
	const s = 10.0
	hi := fsrsNextInterval(s, 0.95)
	lo := fsrsNextInterval(s, 0.85)
	if hi >= lo {
		t.Fatalf("retention dial backwards: 0.95=>%d 0.85=>%d", hi, lo)
	}
	if got := normalizeRetention(1.5); got != defaultRetention {
		t.Fatalf("normalizeRetention(1.5) = %v, want default %v", got, defaultRetention)
	}
}

// TestReviewScheduleLifecycle exercises seed → due → grade against a real DB.
func TestReviewScheduleLifecycle(t *testing.T) {
	store, err := openStore(t.TempDir() + "/srs.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(42)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	if err := store.SeedReview(chatID, "Serendipity", now); err != nil {
		t.Fatalf("SeedReview: %v", err)
	}
	// Seeding is idempotent — a second seed must not reset or duplicate.
	if err := store.SeedReview(chatID, "serendipity", now); err != nil {
		t.Fatalf("SeedReview (2nd): %v", err)
	}

	// Not due yet (due_at is one day out).
	if due, err := store.DueReviews(chatID, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("DueReviews now: got %d (err %v), want 0", len(due), err)
	}

	// Due a day and a bit later.
	later := now.AddDate(0, 0, 1).Add(time.Minute)
	due, err := store.DueReviews(chatID, later, 10)
	if err != nil {
		t.Fatalf("DueReviews later: %v", err)
	}
	if len(due) != 1 || due[0].term != "serendipity" {
		t.Fatalf("DueReviews later: got %+v, want [serendipity]", due)
	}

	// Grade as known — must promote and push due_at out so it's not due again now.
	ok, err := store.ApplyReviewKnown(chatID, "serendipity", later)
	if err != nil || !ok {
		t.Fatalf("ApplyReviewKnown: ok=%v err=%v", ok, err)
	}
	if due, err := store.DueReviews(chatID, later, 10); err != nil || len(due) != 0 {
		t.Fatalf("DueReviews after known: got %d (err %v), want 0", len(due), err)
	}

	// Grading an unscheduled word reports ok=false (stale button).
	if ok, err := store.ApplyReviewForgot(chatID, "ghostword", later); err != nil || ok {
		t.Fatalf("ApplyReviewForgot unknown: ok=%v err=%v, want false/nil", ok, err)
	}
}

// TestRecordSentForSeedsReview verifies sending a word enrols it for review but
// sending a drill verb does not.
func TestRecordSentForSeedsReview(t *testing.T) {
	store, err := openStore(t.TempDir() + "/seed.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(7)
	if err := store.recordSentFor(config.KindWord, chatID, "ephemeral"); err != nil {
		t.Fatalf("recordSentFor word: %v", err)
	}
	if err := store.recordSentFor(config.KindDrill, chatID, "run"); err != nil {
		t.Fatalf("recordSentFor drill: %v", err)
	}

	if _, _, _, ok, err := store.getReview(chatID, "ephemeral"); err != nil || !ok {
		t.Fatalf("word not scheduled: ok=%v err=%v", ok, err)
	}
	if _, _, _, ok, err := store.getReview(chatID, "run"); err != nil || ok {
		t.Fatalf("drill verb should not be scheduled: ok=%v err=%v", ok, err)
	}
}

// TestSuggestLevelChange covers the pure threshold logic.
func TestSuggestLevelChange(t *testing.T) {
	cases := []struct {
		name           string
		level          string
		correct, total int
		wantTarget     string
		wantDir        string
		wantOK         bool
	}{
		{"too small a sample", config.LevelIntermediate, 5, 4, "", "", false},
		{"high accuracy nudges up", config.LevelIntermediate, 19, 20, config.LevelUpperInt, "harder", true},
		{"low accuracy nudges down", config.LevelUpperInt, 8, 20, config.LevelIntermediate, "easier", true},
		{"middling stays put", config.LevelIntermediate, 14, 20, "", "", false},
		{"already hardest, no up", config.LevelAdvanced, 20, 20, "", "", false},
		{"already easiest, no down", config.LevelBeginner, 2, 20, "", "", false},
		{"unknown level", "wizard", 20, 20, "", "", false},
	}
	for _, c := range cases {
		target, dir, ok := suggestLevelChange(c.level, c.correct, c.total)
		if ok != c.wantOK || target != c.wantTarget || dir != c.wantDir {
			t.Errorf("%s: got (%q,%q,%v), want (%q,%q,%v)",
				c.name, target, dir, ok, c.wantTarget, c.wantDir, c.wantOK)
		}
	}
}

// TestLevelSuggestionWindow verifies the rolling-window gate: no suggestion
// until enough reviews accumulate, one fires on a sustained mismatch, then the
// cooldown + window reset suppress repeats.
func TestLevelSuggestionWindow(t *testing.T) {
	store, err := openStore(t.TempDir() + "/perf.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(99)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	// A handful of correct reviews — below the minimum sample, so no suggestion.
	for i := 0; i < 4; i++ {
		if err := store.RecordReviewOutcome(chatID, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, ok, err := store.LevelSuggestion(chatID, now); err != nil || ok {
		t.Fatalf("below sample: ok=%v err=%v, want false", ok, err)
	}

	// Accumulate a sustained run of correct answers at the default level.
	for i := 0; i < reviewPerfMinSample; i++ {
		if err := store.RecordReviewOutcome(chatID, true); err != nil {
			t.Fatal(err)
		}
	}
	target, dir, acc, ok, err := store.LevelSuggestion(chatID, now)
	if err != nil || !ok || dir != "harder" || target != config.LevelUpperInt {
		t.Fatalf("sustained high: target=%q dir=%q ok=%v err=%v", target, dir, ok, err)
	}
	if acc < levelUpAccuracy*100 {
		t.Errorf("accuracy = %d, want high", acc)
	}

	// Immediately asking again is suppressed: window was reset + cooldown active.
	if _, _, _, ok, err := store.LevelSuggestion(chatID, now.Add(time.Hour)); err != nil || ok {
		t.Fatalf("within cooldown: ok=%v err=%v, want false", ok, err)
	}
}
