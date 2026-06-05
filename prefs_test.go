package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
		{"upper-intermediate", "upper-intermediate", true},
		{"advanced", "advanced", true},
		{"BEGINNER", "beginner", true},
		{"Advanced", "advanced", true},
		{"  intermediate  ", "intermediate", true},
		{" Beginner ", "beginner", true},
		{"UPPER-INTERMEDIATE", "upper-intermediate", true},
		{" Upper-Intermediate ", "upper-intermediate", true},
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
		{"upper-intermediate", "Upper-Intermediate"},
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

func TestStoreGetPrefsNewDefaults(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(499)
	store.AddSubscriber(chatID)

	prefs, err := store.GetPrefs(chatID)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if !prefs.QuizEnabled {
		t.Error("default quiz_enabled should be true")
	}
	if !prefs.IdiomEnabled {
		t.Error("default idiom_enabled should be true")
	}
	if !prefs.ReviewEnabled {
		t.Error("default review_enabled should be true")
	}
	if !prefs.DigestEnabled {
		t.Error("default digest_enabled should be true")
	}
	if !prefs.DailyReviewEnabled {
		t.Error("default daily_review_enabled should be true")
	}
	if prefs.QuizIntervalHours != defaultQuizIntervalHours {
		t.Errorf("default quiz_interval_hours = %d, want %d", prefs.QuizIntervalHours, defaultQuizIntervalHours)
	}
}

func TestStoreNewToggles(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(500)
	store.AddSubscriber(chatID)

	for _, tc := range []struct {
		name   string
		get    func() bool
		setOn  func() error
		setOff func() error
	}{
		{"quiz", func() bool { return store.GetQuizEnabled(chatID) },
			func() error { return store.SetQuizEnabled(chatID, true) },
			func() error { return store.SetQuizEnabled(chatID, false) }},
		{"idiom", func() bool { return store.GetIdiomEnabled(chatID) },
			func() error { return store.SetIdiomEnabled(chatID, true) },
			func() error { return store.SetIdiomEnabled(chatID, false) }},
		{"review", func() bool { return store.GetReviewEnabled(chatID) },
			func() error { return store.SetReviewEnabled(chatID, true) },
			func() error { return store.SetReviewEnabled(chatID, false) }},
		{"digest", func() bool { return store.GetDigestEnabled(chatID) },
			func() error { return store.SetDigestEnabled(chatID, true) },
			func() error { return store.SetDigestEnabled(chatID, false) }},
		{"daily_review", func() bool { return store.GetDailyReviewEnabled(chatID) },
			func() error { return store.SetDailyReviewEnabled(chatID, true) },
			func() error { return store.SetDailyReviewEnabled(chatID, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.get() {
				t.Errorf("%s: should be enabled by default", tc.name)
			}
			if err := tc.setOff(); err != nil {
				t.Fatalf("setOff: %v", err)
			}
			if tc.get() {
				t.Errorf("%s: should be disabled after setOff", tc.name)
			}
			if err := tc.setOn(); err != nil {
				t.Fatalf("setOn: %v", err)
			}
			if !tc.get() {
				t.Errorf("%s: should be enabled after setOn", tc.name)
			}
		})
	}
}

func TestStoreQuizIntervalHours(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(501)
	store.AddSubscriber(chatID)

	if got := store.GetQuizIntervalHours(chatID); got != defaultQuizIntervalHours {
		t.Errorf("default quiz interval = %d, want %d", got, defaultQuizIntervalHours)
	}
	if err := store.SetQuizIntervalHours(chatID, 12); err != nil {
		t.Fatalf("SetQuizIntervalHours(12): %v", err)
	}
	if got := store.GetQuizIntervalHours(chatID); got != 12 {
		t.Errorf("quiz interval after set = %d, want 12", got)
	}
	// Invalid value should normalize to default.
	if err := store.SetQuizIntervalHours(chatID, 99); err != nil {
		t.Fatalf("SetQuizIntervalHours(invalid): %v", err)
	}
	if got := store.GetQuizIntervalHours(chatID); got != defaultQuizIntervalHours {
		t.Errorf("quiz interval after invalid = %d, want %d", got, defaultQuizIntervalHours)
	}
}

func TestQuizDueForUser(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		hour     int
		interval int
		want     bool
	}{
		{0, 6, true},
		{6, 6, true},
		{12, 6, true},
		{3, 6, false},
		{10, 6, false},
		{0, 3, true},
		{3, 3, true},
		{1, 3, false},
		{0, 24, true},
		{12, 24, false},
	} {
		t.Run(fmt.Sprintf("h%d_int%d", tc.hour, tc.interval), func(t *testing.T) {
			appLocation = time.UTC
			now := base.Add(time.Duration(tc.hour) * time.Hour)
			if got := quizDueForUser(now, tc.interval); got != tc.want {
				t.Errorf("quizDueForUser(hour=%d, interval=%d) = %v, want %v", tc.hour, tc.interval, got, tc.want)
			}
		})
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
	if got := poolTargetFor("upper-intermediate"); got != poolMin {
		t.Errorf("poolTargetFor(upper-intermediate) = %d, want %d", got, poolMin)
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
		msg := formatStats(UserStats{Level: defaultLevel}, "")
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
		msg := formatStats(UserStats{Level: defaultLevel, QuizAnswered: 10, QuizCorrect: 7}, "")
		if !strings.Contains(msg, "Quiz accuracy") {
			t.Error("missing 'Quiz accuracy'")
		}
		if !strings.Contains(msg, "70%") {
			t.Error("missing correct percentage")
		}
	})

	t.Run("paused", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel, Paused: true}, "")
		if !strings.Contains(msg, "paused") {
			t.Error("missing 'paused'")
		}
	})

	t.Run("streak with flame", func(t *testing.T) {
		msg := formatStats(UserStats{Level: defaultLevel, CurrentStreak: 5}, "")
		if !strings.Contains(msg, "🔥") {
			t.Error("missing flame emoji for streak >= 3")
		}
	})
}

// ---------------------------------------------------------------------------
// /setup — time-budget presets
// ---------------------------------------------------------------------------

func TestTimeBudgetPresetsExhaustive(t *testing.T) {
	expectedBudgets := []int{5, 15, 30, 60, 120}
	for _, budget := range expectedBudgets {
		p, ok := presetByMinutes(budget)
		if !ok {
			t.Errorf("presetByMinutes(%d) not found", budget)
			continue
		}
		if _, ok := normalizeInterval(p.interval); !ok {
			t.Errorf("preset %d min/day: interval %d not in allIntervals", budget, p.interval)
		}
		if _, ok := normalizeQuizIntervalHours(p.quizIntervalHours); !ok {
			t.Errorf("preset %d min/day: quizIntervalHours %d not in allQuizIntervalHours", budget, p.quizIntervalHours)
		}
	}
}

func TestApplyTimeBudgetPreset(t *testing.T) {
	store := openTestStore(t)
	chatID := int64(999)
	store.AddSubscriber(chatID)

	for _, budget := range []int{5, 15, 30, 60, 120} {
		p, _ := presetByMinutes(budget)
		if err := applyTimeBudgetPreset(store, chatID, p); err != nil {
			t.Fatalf("budget=%d: applyTimeBudgetPreset: %v", budget, err)
		}
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			t.Fatalf("budget=%d: GetPrefs: %v", budget, err)
		}
		if prefs.Interval != p.interval {
			t.Errorf("budget=%d: interval = %d, want %d", budget, prefs.Interval, p.interval)
		}
		if prefs.QuizIntervalHours != p.quizIntervalHours {
			t.Errorf("budget=%d: quizIntervalHours = %d, want %d", budget, prefs.QuizIntervalHours, p.quizIntervalHours)
		}
		if prefs.IdiomEnabled != p.idiomEnabled {
			t.Errorf("budget=%d: idiomEnabled = %v, want %v", budget, prefs.IdiomEnabled, p.idiomEnabled)
		}
		if prefs.TipsEnabled != p.tipsEnabled {
			t.Errorf("budget=%d: tipsEnabled = %v, want %v", budget, prefs.TipsEnabled, p.tipsEnabled)
		}
		if prefs.DigestEnabled != p.digestEnabled {
			t.Errorf("budget=%d: digestEnabled = %v, want %v", budget, prefs.DigestEnabled, p.digestEnabled)
		}
	}
}

func TestPresetProgressionInterval(t *testing.T) {
	// Higher budgets should have shorter or equal intervals (more frequent content).
	for i := 1; i < len(timeBudgetPresets); i++ {
		prev := timeBudgetPresets[i-1]
		curr := timeBudgetPresets[i]
		if curr.interval > prev.interval {
			t.Errorf("preset %q has longer interval (%d) than lower-budget %q (%d) — expected <= ",
				curr.label, curr.interval, prev.label, prev.interval)
		}
	}
}

func TestPresetProgressionQuizInterval(t *testing.T) {
	// Higher budgets should have shorter or equal quiz intervals.
	for i := 1; i < len(timeBudgetPresets); i++ {
		prev := timeBudgetPresets[i-1]
		curr := timeBudgetPresets[i]
		if curr.quizIntervalHours > prev.quizIntervalHours {
			t.Errorf("preset %q has longer quiz interval (%dh) than lower-budget %q (%dh) — expected <=",
				curr.label, curr.quizIntervalHours, prev.label, prev.quizIntervalHours)
		}
	}
}
