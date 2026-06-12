package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// TestConfigCallbackPerKindLevelPool exercises setting and clearing a per-(kind,level)
// pool override via the /config callback flow (e.g. "upper-intermediate words").
func TestConfigCallbackPerKindLevelPool(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	saveBotConfig(t)
	config.MaintainerChatID = "300"
	config.PoolTarget, config.PoolMin = 300, 100

	set := func(data string) {
		handleCallback(store, mock, &TelegramCallbackQuery{
			ID:      "cb",
			From:    &TelegramUser{ID: 300},
			Message: &TelegramMessage{MessageID: 10, Chat: TelegramChat{ID: 300}},
			Data:    data,
		})
	}

	// Set upper-intermediate words to 400.
	set("cfg:kl:word:upper-intermediate:400")
	if v, ok := config.PoolKindLevelOverride(config.KindWord, config.LevelUpperInt); !ok || v != 400 {
		t.Fatalf("config.PoolKindLevelOverride(word, upper-int) = %d,%v; want 400,true", v, ok)
	}
	if v, ok := store.GetBotConfig("pool_kl_word_upper-intermediate"); !ok || v != "400" {
		t.Errorf("bot_config pool_kl_word_upper-intermediate = %q (ok=%v), want '400'", v, ok)
	}
	// The exact pool gets the override...
	if got := poolTargetFor(config.KindWord, config.LevelUpperInt); got != 400 {
		t.Errorf("poolTargetFor(word, upper-int) = %d, want 400 (kind+level override)", got)
	}
	// ...but the same kind at another level, and another kind at this level, do not.
	if got := poolTargetFor(config.KindWord, config.LevelAdvanced); got != 100 {
		t.Errorf("poolTargetFor(word, advanced) = %d, want 100 (global min)", got)
	}
	if got := poolTargetFor(config.KindStory, config.LevelUpperInt); got != 100 {
		t.Errorf("poolTargetFor(story, upper-int) = %d, want 100 (global min)", got)
	}

	// Clearing (n=0) removes the override and the persisted key.
	set("cfg:kl:word:upper-intermediate:0")
	if _, ok := config.PoolKindLevelOverride(config.KindWord, config.LevelUpperInt); ok {
		t.Error("expected per-(kind,level) override cleared")
	}
	if _, ok := store.GetBotConfig("pool_kl_word_upper-intermediate"); ok {
		t.Error("expected pool_kl_word_upper-intermediate deleted from bot_config")
	}
	if got := poolTargetFor(config.KindWord, config.LevelUpperInt); got != 100 {
		t.Errorf("poolTargetFor(word, upper-int) after clear = %d, want 100", got)
	}
}

// TestResolvePoolTargetPrecedence verifies the most-specific-wins ordering:
// per-(kind,level) → per-kind → per-level → global.
func TestResolvePoolTargetPrecedence(t *testing.T) {
	saveBotConfig(t)
	config.PoolTarget, config.PoolMin = 300, 100

	// Global only.
	if got := config.ResolvePoolTarget(config.KindWord, config.LevelUpperInt); got != 100 {
		t.Errorf("global: got %d, want 100", got)
	}
	// Per-level applies.
	config.SetPoolLevelOverride(config.LevelUpperInt, 250)
	if got := config.ResolvePoolTarget(config.KindWord, config.LevelUpperInt); got != 250 {
		t.Errorf("per-level: got %d, want 250", got)
	}
	// Per-kind beats per-level.
	config.SetPoolKindOverride(config.KindWord, 175)
	if got := config.ResolvePoolTarget(config.KindWord, config.LevelUpperInt); got != 175 {
		t.Errorf("per-kind: got %d, want 175", got)
	}
	// Per-(kind,level) beats both.
	config.SetPoolKindLevelOverride(config.KindWord, config.LevelUpperInt, 400)
	if got := config.ResolvePoolTarget(config.KindWord, config.LevelUpperInt); got != 400 {
		t.Errorf("per-(kind,level): got %d, want 400", got)
	}
	// A different kind at the same level still sees the per-level value.
	if got := config.ResolvePoolTarget(config.KindStory, config.LevelUpperInt); got != 250 {
		t.Errorf("other kind at level: got %d, want 250 (per-level)", got)
	}
}

// TestLoadBotConfigKindLevelOverride verifies a persisted per-(kind,level) key is
// reloaded on startup, and bogus kinds/levels are ignored.
func TestLoadBotConfigKindLevelOverride(t *testing.T) {
	store := testStoreHelper(t)
	saveBotConfig(t)

	_ = store.SetBotConfig("pool_kl_word_upper-intermediate", "400")
	_ = store.SetBotConfig("pool_kl_bogus_advanced", "999") // ignored: not a real kind
	_ = store.SetBotConfig("pool_kl_word_nonsense", "999")  // ignored: not a real level

	store.LoadBotConfig()

	if v, ok := config.PoolKindLevelOverride(config.KindWord, config.LevelUpperInt); !ok || v != 400 {
		t.Errorf("after load: config.PoolKindLevelOverride(word, upper-int) = %d,%v; want 400,true", v, ok)
	}
	if _, ok := config.PoolKindLevelOverride("bogus", config.LevelAdvanced); ok {
		t.Error("bogus kind override should be ignored")
	}
}

// TestHandleAdminHelp verifies /admin lists the maintainer commands.
func TestHandleAdminHelp(t *testing.T) {
	mock := &mockNotifier{}
	handleAdminHelp(mock, 999)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.sent))
	}
	report := mock.sent[0].text
	for _, cmd := range []string{"/metrics", "/poolusage", "/health", "/config", "/users", "/announce", "/backup", "/admin"} {
		if !strings.Contains(report, cmd) {
			t.Errorf("admin help missing command %s in:\n%s", cmd, report)
		}
	}
}

// TestHandleMessageAdminHelpGated verifies /admin is maintainer-only.
func TestHandleMessageAdminHelpGated(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	config.MaintainerChatID = "999"

	// Non-maintainer is refused.
	handleMessage(context.Background(), emptyProviderChain(), store, mock, &TelegramMessage{
		MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/admin", From: &TelegramUser{ID: 100},
	})
	if len(mock.sentTexts()) == 0 || !strings.Contains(mock.sentTexts()[0], "maintainer") {
		t.Fatalf("expected maintainer-only refusal, got %v", mock.sentTexts())
	}
}
