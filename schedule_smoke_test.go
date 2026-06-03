package main

import (
	"database/sql"
	"testing"
	"time"
)

func TestDueAndKind(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 6, 3, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		name     string
		t        time.Time
		interval int
		wantDue  bool
		wantKind string
	}{
		// 30-min interval alternates every base slot.
		{"30m @00:00 drill", at(0, 0), 30, true, kindDrill},
		{"30m @00:30 word", at(0, 30), 30, true, kindWord},
		{"30m @01:00 drill", at(1, 0), 30, true, kindDrill},
		// 60-min interval: due on the hour only, alternating each hour.
		{"60m @00:00 drill", at(0, 0), 60, true, kindDrill},
		{"60m @00:30 not due", at(0, 30), 60, false, ""},
		{"60m @01:00 word", at(1, 0), 60, true, kindWord},
		{"60m @02:00 drill", at(2, 0), 60, true, kindDrill},
		// 120-min interval: due every 2h, alternating each due slot.
		{"120m @00:00 drill", at(0, 0), 120, true, kindDrill},
		{"120m @01:00 not due", at(1, 0), 120, false, ""},
		{"120m @02:00 word", at(2, 0), 120, true, kindWord},
		{"120m @04:00 drill", at(4, 0), 120, true, kindDrill},
		// Zero/invalid interval falls back to default (30).
		{"invalid falls back @00:30 word", at(0, 30), 0, true, kindWord},
	}
	for _, c := range cases {
		due, kind := dueAndKind(c.t, c.interval)
		if due != c.wantDue || (due && kind != c.wantKind) {
			t.Errorf("%s: got due=%v kind=%q, want due=%v kind=%q",
				c.name, due, kind, c.wantDue, c.wantKind)
		}
	}
}

func TestNormalizeInterval(t *testing.T) {
	if v, ok := normalizeInterval(60); !ok || v != 60 {
		t.Errorf("normalizeInterval(60) = %d,%v; want 60,true", v, ok)
	}
	if v, ok := normalizeInterval(45); ok || v != defaultInterval {
		t.Errorf("normalizeInterval(45) = %d,%v; want %d,false", v, ok, defaultInterval)
	}
}

// TestIntervalMigration verifies a pre-v1.6.0 user_prefs table (no
// interval_minutes column) is migrated additively without data loss.
func TestIntervalMigration(t *testing.T) {
	path := t.TempDir() + "/legacy.db"

	// Seed a pre-v1.6.0 user_prefs table (level + paused only) with a row.
	pre, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := pre.Exec(`CREATE TABLE user_prefs (
		chat_id INTEGER PRIMARY KEY,
		level TEXT NOT NULL DEFAULT 'intermediate',
		paused INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := pre.Exec("INSERT INTO user_prefs (chat_id, level, paused) VALUES (?, 'advanced', 1)", 7); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	pre.Close()

	// openStore should run the migration and add interval_minutes.
	store, err := openStore(path)
	if err != nil {
		t.Fatalf("openStore (migration): %v", err)
	}
	defer store.Close()

	prefs, err := store.GetPrefs(7)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if prefs.Level != levelAdvanced {
		t.Errorf("level = %q, want advanced (data lost?)", prefs.Level)
	}
	if !prefs.Paused {
		t.Errorf("paused = false, want true (data lost?)")
	}
	if prefs.Interval != defaultInterval {
		t.Errorf("interval = %d, want default %d", prefs.Interval, defaultInterval)
	}
}
