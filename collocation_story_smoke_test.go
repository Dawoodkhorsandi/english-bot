package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Prompt building & parsing
// ---------------------------------------------------------------------------

func TestBuildCollocationPromptExclusion(t *testing.T) {
	p := buildCollocationPrompt(defaultLevel, nil)
	if strings.Contains(p, "Do NOT use") {
		t.Error("prompt without exclusions should not contain an exclusion clause")
	}
	p = buildCollocationPrompt(defaultLevel, []string{"make a decision", "heavy rain"})
	if !strings.Contains(p, "make a decision, heavy rain") {
		t.Errorf("prompt should list excluded collocations, got %q", p)
	}
}

func TestBuildStoryPromptExclusion(t *testing.T) {
	p := buildStoryPrompt(defaultLevel, nil)
	if strings.Contains(p, "Do NOT reuse") {
		t.Error("prompt without exclusions should not contain an exclusion clause")
	}
	p = buildStoryPrompt(defaultLevel, []string{"the lost ticket"})
	if !strings.Contains(p, "the lost ticket") {
		t.Errorf("prompt should list excluded story titles, got %q", p)
	}
}

func TestStoryLevelInstructionPerLevel(t *testing.T) {
	seen := map[string]bool{}
	for _, level := range allLevels {
		instr := storyLevelInstruction(level)
		if instr == "" {
			t.Errorf("empty story instruction for level %q", level)
		}
		if seen[instr] {
			t.Errorf("duplicate story instruction for level %q", level)
		}
		seen[instr] = true
	}
}

func TestParseCollocation(t *testing.T) {
	card := "🔗 <b>Collocation of the Day: Make a Decision</b>\n————\n\n💬 <b>Meaning</b>\nTo choose something."
	if got := parseCollocation(card); got != "make a decision" {
		t.Errorf("parseCollocation = %q, want %q", got, "make a decision")
	}
	if got := parseCollocation("no labeled line here"); got != "" {
		t.Errorf("parseCollocation on unlabeled text = %q, want empty", got)
	}
}

func TestParseStoryTitle(t *testing.T) {
	card := "📖 <b>Mini Story: The Lost Ticket</b>\n————\n\nSam was late."
	if got := parseStoryTitle(card); got != "the lost ticket" {
		t.Errorf("parseStoryTitle = %q, want %q", got, "the lost ticket")
	}
	if got := parseStoryTitle("just a story with no header"); got != "" {
		t.Errorf("parseStoryTitle on unlabeled text = %q, want empty", got)
	}
}

// parseIdiom delegates to the shared phrase parser; make sure the refactor
// keeps the old behaviour.
func TestParseIdiomStillWorks(t *testing.T) {
	card := "🗣️ <b>Idiom of the Day: Break the Ice</b>\n————"
	if got := parseIdiom(card); got != "break the ice" {
		t.Errorf("parseIdiom = %q, want %q", got, "break the ice")
	}
}

// ---------------------------------------------------------------------------
// On-demand command handlers
// ---------------------------------------------------------------------------

func TestHandleMessageCollocation(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindCollocation, defaultLevel, "make a decision", "to choose", "collocation card text")

	msg := &TelegramMessage{MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/collocation"}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if texts[len(texts)-1] != "collocation card text" {
		t.Errorf("last message should be the collocation card, got %q", texts[len(texts)-1])
	}
	// And it should be recorded so it isn't repeated.
	if _, _, _, ok, _ := store.PooledUnseen(kindCollocation, defaultLevel, 100); ok {
		t.Error("served collocation should be recorded as seen")
	}
}

func TestHandleMessageStory(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindStory, defaultLevel, "the lost ticket", "", "mini story text")

	msg := &TelegramMessage{MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/story"}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if texts[len(texts)-1] != "mini story text" {
		t.Errorf("last message should be the story, got %q", texts[len(texts)-1])
	}
	if _, _, _, ok, _ := store.PooledUnseen(kindStory, defaultLevel, 100); ok {
		t.Error("served story should be recorded as seen")
	}
}

// ---------------------------------------------------------------------------
// Collocation-of-the-day scheduler sweep
// ---------------------------------------------------------------------------

func TestSendCollocationOfDay_DeliversAndMarks(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindCollocation, defaultLevel, "make a decision", "to choose", "collocation card")

	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected 1 collocation sent, got %d", mock.sentCount())
	}
	delivered, err := store.CollocationDelivered(100, "2026-06-03")
	if err != nil {
		t.Fatalf("CollocationDelivered: %v", err)
	}
	if !delivered {
		t.Fatal("expected collocation marked delivered")
	}
}

func TestSendCollocationOfDay_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindCollocation, defaultLevel, "heavy rain", "", "collocation card")

	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected idempotent send count=1, got %d", mock.sentCount())
	}
}

func TestSendCollocationOfDay_SkipsPausedAndDisabled(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddSubscriber(101)
	store.SetPaused(100, true)
	store.SetCollocationEnabled(101, false)
	store.AddToPool(kindCollocation, defaultLevel, "fast asleep", "", "collocation card")

	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no collocations sent, got %d", mock.sentCount())
	}
}

func TestSendCollocationOfDay_SkipsQuietHours(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "23:59"

	store.AddSubscriber(100)
	store.AddToPool(kindCollocation, defaultLevel, "strong coffee", "", "collocation card")

	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no sends during quiet hours, got %d", mock.sentCount())
	}
}

// ---------------------------------------------------------------------------
// Daily mini story scheduler sweep
// ---------------------------------------------------------------------------

func TestSendMiniStory_DeliversAndMarks(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindStory, defaultLevel, "the lost ticket", "", "mini story text")

	now := time.Date(2026, 6, 3, 17, 0, 0, 0, time.UTC)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected 1 story sent, got %d", mock.sentCount())
	}
	delivered, err := store.StoryDelivered(100, "2026-06-03")
	if err != nil {
		t.Fatalf("StoryDelivered: %v", err)
	}
	if !delivered {
		t.Fatal("expected story marked delivered")
	}
}

func TestSendMiniStory_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindStory, defaultLevel, "a rainy morning", "", "mini story text")

	now := time.Date(2026, 6, 3, 17, 0, 0, 0, time.UTC)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected idempotent send count=1, got %d", mock.sentCount())
	}
}

func TestSendMiniStory_SkipsPausedAndDisabled(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddSubscriber(101)
	store.SetPaused(100, true)
	store.SetStoryEnabled(101, false)
	store.AddToPool(kindStory, defaultLevel, "the new job", "", "mini story text")

	now := time.Date(2026, 6, 3, 17, 0, 0, 0, time.UTC)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no stories sent, got %d", mock.sentCount())
	}
}

func TestSendMiniStory_SkipsQuietHours(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "23:59"

	store.AddSubscriber(100)
	store.AddToPool(kindStory, defaultLevel, "the quiet night", "", "mini story text")

	now := time.Date(2026, 6, 3, 17, 0, 0, 0, time.UTC)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no sends during quiet hours, got %d", mock.sentCount())
	}
}

// ---------------------------------------------------------------------------
// Rate limiter interplay: a collocation and a story in the same interval slot
// must not both go out (one message per user per interval window).
// ---------------------------------------------------------------------------

func TestCollocationAndStoryShareHourlySlot(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindCollocation, defaultLevel, "make a decision", "", "collocation card")
	store.AddToPool(kindStory, defaultLevel, "the lost ticket", "", "mini story text")

	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	sendCollocationOfDay(context.Background(), emptyProviderChain(), store, mock, now)
	sendMiniStory(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected the shared rate limiter to allow only 1 send this slot, got %d", mock.sentCount())
	}
}

// ---------------------------------------------------------------------------
// Settings & prefs
// ---------------------------------------------------------------------------

func TestCollocationAndStoryPrefsDefaultsAndToggle(t *testing.T) {
	store := testStoreHelper(t)

	prefs, err := store.GetPrefs(100)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if !prefs.CollocationEnabled || !prefs.StoryEnabled {
		t.Fatal("collocation and story should default to enabled")
	}

	if err := store.SetCollocationEnabled(100, false); err != nil {
		t.Fatalf("SetCollocationEnabled: %v", err)
	}
	if err := store.SetStoryEnabled(100, false); err != nil {
		t.Fatalf("SetStoryEnabled: %v", err)
	}
	prefs, _ = store.GetPrefs(100)
	if prefs.CollocationEnabled || prefs.StoryEnabled {
		t.Fatal("expected both toggles off after disabling")
	}
}

func TestSettingsToggleCollocationAndStoryCallbacks(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	for _, key := range []string{"collocation", "story"} {
		cb := &TelegramCallbackQuery{
			ID:      "cb-" + key,
			Data:    "settings:toggle:" + key,
			Message: &TelegramMessage{MessageID: 7, Chat: TelegramChat{ID: 100}},
		}
		handleCallback(store, mock, cb)
	}

	prefs, err := store.GetPrefs(100)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if prefs.CollocationEnabled {
		t.Error("collocation should be toggled off")
	}
	if prefs.StoryEnabled {
		t.Error("story should be toggled off")
	}
	if len(mock.edits) != 2 {
		t.Errorf("expected the settings hub to refresh twice, got %d edits", len(mock.edits))
	}
}

// TestCollocationStoryPrefsMigration verifies a pre-v1.23.0 user_prefs table
// (no collocation_enabled/story_enabled columns) is migrated additively and
// the new toggles default to enabled.
func TestCollocationStoryPrefsMigration(t *testing.T) {
	path := t.TempDir() + "/legacy.db"

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
	if _, err := pre.Exec("INSERT INTO user_prefs (chat_id, level) VALUES (?, 'advanced')", 7); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	pre.Close()

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
	if !prefs.CollocationEnabled || !prefs.StoryEnabled {
		t.Error("migrated rows should default collocation/story toggles to enabled")
	}
}
