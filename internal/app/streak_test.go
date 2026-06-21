package app

import (
	"testing"
	"time"
)

// TestParseFreezePayload covers the invoice-payload parser bounds.
func TestParseFreezePayload(t *testing.T) {
	cases := map[string]int{
		"freeze:1":  1,
		"freeze:5":  5,
		"freeze:10": 10,
		"freeze:0":  0, // below min
		"freeze:99": 0, // above max
		"freeze:x":  0, // non-numeric
		"other:3":   0, // wrong prefix
		"":          0,
	}
	for payload, want := range cases {
		if got := parseFreezePayload(payload); got != want {
			t.Errorf("parseFreezePayload(%q) = %d, want %d", payload, got, want)
		}
	}
}

// TestAddStreakFreezes verifies granting, spending, and the zero floor.
func TestAddStreakFreezes(t *testing.T) {
	store, err := openStore(t.TempDir() + "/freeze.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(1)
	if bal, _ := store.AddStreakFreezes(chatID, 3); bal != 3 {
		t.Fatalf("after +3: %d, want 3", bal)
	}
	if bal, _ := store.AddStreakFreezes(chatID, -1); bal != 2 {
		t.Fatalf("after -1: %d, want 2", bal)
	}
	if bal, _ := store.AddStreakFreezes(chatID, -10); bal != 0 {
		t.Fatalf("floored balance: %d, want 0", bal)
	}
}

// TestProtectStreak verifies a saver rescues a missed day exactly once.
func TestProtectStreak(t *testing.T) {
	store, err := openStore(t.TempDir() + "/protect.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(7)
	now := time.Date(2026, 6, 10, 0, 5, 0, 0, time.UTC)
	dayBefore := now.AddDate(0, 0, -2)

	// A live streak through the day before yesterday, but yesterday missed.
	if err := store.RecordActivity(chatID, dayBefore); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	mock := &mockNotifier{}

	// No saver yet → no rescue.
	if protectStreak(store, mock, chatID, now) {
		t.Fatal("rescued with zero savers")
	}

	// Grant a saver → rescue fills yesterday and spends the token.
	if _, err := store.AddStreakFreezes(chatID, 1); err != nil {
		t.Fatal(err)
	}
	if !protectStreak(store, mock, chatID, now) {
		t.Fatal("expected a rescue")
	}
	if bal := store.GetStreakFreezes(chatID); bal != 0 {
		t.Fatalf("saver not spent: balance %d", bal)
	}

	// Idempotent: yesterday is now filled, a re-run is a no-op even with a saver.
	store.AddStreakFreezes(chatID, 1)
	if protectStreak(store, mock, chatID, now) {
		t.Fatal("double-rescued the same day")
	}
}
