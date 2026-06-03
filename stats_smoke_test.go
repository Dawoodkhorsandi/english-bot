package main

import (
	"testing"
	"time"
)

func TestComputeStreaks(t *testing.T) {
	mk := func(ds ...string) map[string]bool {
		m := map[string]bool{}
		for _, d := range ds {
			m[d] = true
		}
		return m
	}
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) // today = 2026-06-03

	cases := []struct {
		name        string
		days        map[string]bool
		wantCurrent int
		wantLongest int
	}{
		{"empty", mk(), 0, 0},
		{"today only", mk("2026-06-03"), 1, 1},
		{"3-day run ending today", mk("2026-06-01", "2026-06-02", "2026-06-03"), 3, 3},
		{"no today but yesterday", mk("2026-06-01", "2026-06-02"), 2, 2},
		{"broken 2 days ago", mk("2026-06-01"), 0, 1},
		{"gap then current", mk("2026-05-20", "2026-05-21", "2026-05-22", "2026-06-02", "2026-06-03"), 2, 3},
	}
	for _, c := range cases {
		gotC, gotL := computeStreaks(c.days, now)
		if gotC != c.wantCurrent || gotL != c.wantLongest {
			t.Errorf("%s: got current=%d longest=%d, want current=%d longest=%d",
				c.name, gotC, gotL, c.wantCurrent, c.wantLongest)
		}
	}
}

func TestUserStatsDB(t *testing.T) {
	store, err := openStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chat = int64(42)
	if _, err := store.AddSubscriber(chat); err != nil {
		t.Fatalf("AddSubscriber: %v", err)
	}

	// Seed history across distinct UTC days with explicit sent_at.
	seed := func(table, word, ts string) {
		if _, err := store.db.Exec(
			"INSERT INTO "+table+" (chat_id, word, sent_at) VALUES (?, ?, ?)", chat, word, ts,
		); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}
	seed("sent_words", "go", "2026-06-01 10:00:00")
	seed("sent_words", "run", "2026-06-02 10:00:00")
	seed("sent_vocab", "ephemeral", "2026-06-02 11:00:00")
	seed("sent_vocab", "lucid", "2026-06-05 11:00:00")

	st, err := store.UserStats(chat)
	if err != nil {
		t.Fatalf("UserStats: %v", err)
	}
	if st.Verbs != 2 {
		t.Errorf("Verbs = %d, want 2", st.Verbs)
	}
	if st.Words != 2 {
		t.Errorf("Words = %d, want 2", st.Words)
	}
	// Distinct active days (UTC): 06-01, 06-02, 06-05 = 3.
	if st.ActiveDays != 3 {
		t.Errorf("ActiveDays = %d, want 3", st.ActiveDays)
	}
	// Longest run: 06-01..06-02 = 2.
	if st.LongestStreak != 2 {
		t.Errorf("LongestStreak = %d, want 2", st.LongestStreak)
	}
	if st.Level != defaultLevel {
		t.Errorf("Level = %q, want %q", st.Level, defaultLevel)
	}
	if !st.HasMemberSince {
		t.Errorf("HasMemberSince = false, want true")
	}

	// formatStats must produce a non-empty HTML message without panicking.
	if msg := formatStats(st); len(msg) == 0 {
		t.Errorf("formatStats returned empty")
	}
}

func TestParseStoredUTC(t *testing.T) {
	cases := []struct {
		name   string
		input  any
		wantOK bool
		wantTS string // expected UTC time formatted as "2006-01-02 15:04:05"
	}{
		{"standard datetime string", "2026-06-03 14:30:00", true, "2026-06-03 14:30:00"},
		{"ISO 8601 Z", "2026-06-03T14:30:00Z", true, "2026-06-03 14:30:00"},
		{"RFC3339", "2026-06-03T14:30:00+00:00", true, "2026-06-03 14:30:00"},
		{"byte slice", []byte("2026-06-03 14:30:00"), true, "2026-06-03 14:30:00"},
		{"time.Time value", time.Date(2026, 6, 3, 14, 30, 0, 0, time.UTC), true, "2026-06-03 14:30:00"},
		{"empty string", "", false, ""},
		{"garbage string", "not-a-date", false, ""},
		{"nil int", 42, false, ""},
	}
	for _, c := range cases {
		ts, ok := parseStoredUTC(c.input)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && ts.Format("2006-01-02 15:04:05") != c.wantTS {
			t.Errorf("%s: ts = %s, want %s", c.name, ts.Format("2006-01-02 15:04:05"), c.wantTS)
		}
	}
}
