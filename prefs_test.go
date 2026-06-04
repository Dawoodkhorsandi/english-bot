package main

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// normalizeLevel
// ---------------------------------------------------------------------------

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		input  string
		want   string
		wantOK bool
	}{
		{"beginner", "beginner", true},
		{"intermediate", "intermediate", true},
		{"advanced", "advanced", true},
		{"BEGINNER", "beginner", true},
		{"Advanced", "advanced", true},
		{"  intermediate  ", "intermediate", true},
		{" Beginner ", "beginner", true},
		{"", defaultLevel, false},
		{"unknown", defaultLevel, false},
		{"pro", defaultLevel, false},
	}
	for _, tt := range tests {
		got, ok := normalizeLevel(tt.input)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("normalizeLevel(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.wantOK)
		}
	}
}

// ---------------------------------------------------------------------------
// levelLabel
// ---------------------------------------------------------------------------

func TestLevelLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"beginner", "Beginner"},
		{"intermediate", "Intermediate"},
		{"advanced", "Advanced"},
		{"unknown", "Intermediate"},
		{"", "Intermediate"},
	}
	for _, tt := range tests {
		if got := levelLabel(tt.input); got != tt.want {
			t.Errorf("levelLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// normalizeInterval
// ---------------------------------------------------------------------------

func TestNormalizeIntervalAll(t *testing.T) {
	tests := []struct {
		input  int
		want   int
		wantOK bool
	}{
		{30, 30, true},
		{60, 60, true},
		{120, 120, true},
		{180, 180, true},
		{240, 240, true},
		{360, 360, true},
		{480, 480, true},
		{720, 720, true},
		{45, defaultInterval, false},
		{0, defaultInterval, false},
		{-1, defaultInterval, false},
		{999, defaultInterval, false},
	}
	for _, tt := range tests {
		got, ok := normalizeInterval(tt.input)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("normalizeInterval(%d) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.wantOK)
		}
	}
}

// ---------------------------------------------------------------------------
// intervalLabel
// ---------------------------------------------------------------------------

func TestIntervalLabel(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{30, "30 minutes"},
		{60, "1 hour"},
		{120, "2 hours"},
		{720, "12 hours"},
		{45, "45 minutes"},
	}
	for _, tt := range tests {
		if got := intervalLabel(tt.input); got != tt.want {
			t.Errorf("intervalLabel(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Store methods (prefs)
// ---------------------------------------------------------------------------

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := openStore(t.TempDir() + "/prefs.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStoreGetPrefsDefaults(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(100)
	if _, err := store.AddSubscriber(chatID); err != nil {
		t.Fatalf("AddSubscriber: %v", err)
	}

	prefs, err := store.GetPrefs(chatID)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if prefs.Level != defaultLevel {
		t.Errorf("default level = %q, want %q", prefs.Level, defaultLevel)
	}
	if prefs.Paused {
		t.Error("default paused should be false")
	}
	if prefs.Interval != defaultInterval {
		t.Errorf("default interval = %d, want %d", prefs.Interval, defaultInterval)
	}
	if !prefs.TTSEnabled {
		t.Error("default TTS should be enabled")
	}
	if !prefs.TipsEnabled {
		t.Error("default tips_enabled should be true")
	}
}

func TestStoreSetAndGetLevel(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(200)
	store.AddSubscriber(chatID)

	if err := store.SetLevel(chatID, "advanced"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	if got := store.GetLevel(chatID); got != "advanced" {
		t.Errorf("GetLevel = %q, want %q", got, "advanced")
	}

	// invalid level should normalize to default
	if err := store.SetLevel(chatID, "garbage"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	if got := store.GetLevel(chatID); got != defaultLevel {
		t.Errorf("GetLevel after invalid = %q, want %q", got, defaultLevel)
	}
}

func TestStoreSetPausedAndIsPaused(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(300)
	store.AddSubscriber(chatID)

	if store.IsPaused(chatID) {
		t.Error("should not be paused initially")
	}
	if err := store.SetPaused(chatID, true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if !store.IsPaused(chatID) {
		t.Error("should be paused after SetPaused(true)")
	}
	if err := store.SetPaused(chatID, false); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if store.IsPaused(chatID) {
		t.Error("should not be paused after SetPaused(false)")
	}
}

func TestStoreSetAndGetInterval(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(400)
	store.AddSubscriber(chatID)

	if err := store.SetInterval(chatID, 120); err != nil {
		t.Fatalf("SetInterval: %v", err)
	}
	if got := store.GetInterval(chatID); got != 120 {
		t.Errorf("GetInterval = %d, want 120", got)
	}

	// invalid interval normalizes to default
	if err := store.SetInterval(chatID, 45); err != nil {
		t.Fatalf("SetInterval: %v", err)
	}
	if got := store.GetInterval(chatID); got != defaultInterval {
		t.Errorf("GetInterval after invalid = %d, want %d", got, defaultInterval)
	}
}

func TestStoreSetAndGetTTSEnabled(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(450)
	store.AddSubscriber(chatID)

	if !store.GetTTSEnabled(chatID) {
		t.Error("TTS should be enabled by default")
	}

	if err := store.SetTTSEnabled(chatID, false); err != nil {
		t.Fatalf("SetTTSEnabled(false): %v", err)
	}
	if store.GetTTSEnabled(chatID) {
		t.Error("TTS should be disabled after SetTTSEnabled(false)")
	}

	if err := store.SetTTSEnabled(chatID, true); err != nil {
		t.Fatalf("SetTTSEnabled(true): %v", err)
	}
	if !store.GetTTSEnabled(chatID) {
		t.Error("TTS should be enabled after SetTTSEnabled(true)")
	}
}

func TestStoreSetAndGetTipsEnabled(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(401)
	store.AddSubscriber(chatID)

	if !store.GetTipsEnabled(chatID) {
		t.Error("tips should be enabled by default")
	}
	if err := store.SetTipsEnabled(chatID, false); err != nil {
		t.Fatalf("SetTipsEnabled(false): %v", err)
	}
	if store.GetTipsEnabled(chatID) {
		t.Error("tips should be disabled after SetTipsEnabled(false)")
	}
	if err := store.SetTipsEnabled(chatID, true); err != nil {
		t.Fatalf("SetTipsEnabled(true): %v", err)
	}
	if !store.GetTipsEnabled(chatID) {
		t.Error("tips should be enabled after SetTipsEnabled(true)")
	}
}

func TestStoreActiveLevels(t *testing.T) {
	store := openTestStore(t)

	// With no prefs set, should return just the default level.
	levels, err := store.ActiveLevels()
	if err != nil {
		t.Fatalf("ActiveLevels: %v", err)
	}
	if len(levels) != 1 || levels[0] != defaultLevel {
		t.Errorf("ActiveLevels (empty) = %v, want [%s]", levels, defaultLevel)
	}

	// Add a user with a different level.
	store.AddSubscriber(500)
	store.SetLevel(500, "beginner")
	store.AddSubscriber(501)
	store.SetLevel(501, "advanced")

	levels, err = store.ActiveLevels()
	if err != nil {
		t.Fatalf("ActiveLevels: %v", err)
	}
	has := map[string]bool{}
	for _, l := range levels {
		has[l] = true
	}
	for _, want := range []string{defaultLevel, "beginner", "advanced"} {
		if !has[want] {
			t.Errorf("ActiveLevels missing %q, got %v", want, levels)
		}
	}
}

// ---------------------------------------------------------------------------
// getEnvWeekday (config.go)
// ---------------------------------------------------------------------------

func TestGetEnvWeekday(t *testing.T) {
	const key = "TEST_WEEKDAY"
	tests := []struct {
		name     string
		value    string
		set      bool
		fallback int
		want     int
	}{
		{"unset", "", false, 3, 3},
		{"sunday", "sunday", true, 0, 0},
		{"Sun", "Sun", true, 0, 0},
		{"off", "off", true, 0, -1},
		{"none", "none", true, 0, -1},
		{"disabled", "disabled", true, 0, -1},
		{"empty", "", true, 5, -1},
		{"numeric_0", "0", true, 5, 0},
		{"numeric_6", "6", true, 0, 6},
		{"invalid", "invalid", true, 4, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			}
			if got := getEnvWeekday(key, tt.fallback); got != tt.want {
				t.Errorf("getEnvWeekday(%q, %d) [env=%q set=%v] = %d, want %d",
					key, tt.fallback, tt.value, tt.set, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncate (providers.go)
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello…"},
		{"hello", 5, "hello"},
	}
	for _, tt := range tests {
		if got := truncate(tt.s, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// sentTableFor (pool.go)
// ---------------------------------------------------------------------------

func TestSentTableFor(t *testing.T) {
	if got := sentTableFor("word"); got != "sent_vocab" {
		t.Errorf("sentTableFor(word) = %q, want sent_vocab", got)
	}
	if got := sentTableFor("drill"); got != "sent_words" {
		t.Errorf("sentTableFor(drill) = %q, want sent_words", got)
	}
	if got := sentTableFor("tip"); got != "sent_tips" {
		t.Errorf("sentTableFor(tip) = %q, want sent_tips", got)
	}
}

// ---------------------------------------------------------------------------
// poolTargetFor (pool.go)
// ---------------------------------------------------------------------------

func TestPoolTargetFor(t *testing.T) {
	if got := poolTargetFor(defaultLevel); got != poolTarget {
		t.Errorf("poolTargetFor(%q) = %d, want %d", defaultLevel, got, poolTarget)
	}
	if got := poolTargetFor("beginner"); got != poolMin {
		t.Errorf("poolTargetFor(beginner) = %d, want %d", got, poolMin)
	}
	if got := poolTargetFor("advanced"); got != poolMin {
		t.Errorf("poolTargetFor(advanced) = %d, want %d", got, poolMin)
	}
}

// ---------------------------------------------------------------------------
// plural (stats.go)
// ---------------------------------------------------------------------------

func TestPlural(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "s"}, {1, ""}, {2, "s"}, {100, "s"},
	}
	for _, tt := range tests {
		if got := plural(tt.n); got != tt.want {
			t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatStats (stats.go)
// ---------------------------------------------------------------------------

func TestFormatStatsVariants(t *testing.T) {
	t.Run("base case", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel})
		if !strings.Contains(msg, "Your Progress") {
			t.Error("missing 'Your Progress'")
		}
		if !strings.Contains(msg, "Grammar drills") {
			t.Error("missing 'Grammar drills'")
		}
		if !strings.Contains(msg, "Vocabulary words") {
			t.Error("missing 'Vocabulary words'")
		}
	})

	t.Run("with quiz stats", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel, QuizAnswered: 10, QuizCorrect: 7})
		if !strings.Contains(msg, "Quiz accuracy") {
			t.Error("missing 'Quiz accuracy'")
		}
		if !strings.Contains(msg, "70%") {
			t.Error("missing correct percentage")
		}
	})

	t.Run("paused", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel, Paused: true})
		if !strings.Contains(msg, "paused") {
			t.Error("missing 'paused'")
		}
	})

	t.Run("streak with flame", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel, CurrentStreak: 5})
		if !strings.Contains(msg, "🔥") {
			t.Error("missing flame emoji for streak >= 3")
		}
	})
}
