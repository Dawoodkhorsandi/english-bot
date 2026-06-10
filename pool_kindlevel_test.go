package main

import (
	"context"
	"strings"
	"testing"
)

// TestConfigCallbackPerKindLevelPool exercises setting and clearing a per-(kind,level)
// pool override via the /config callback flow (e.g. "upper-intermediate words").
func TestConfigCallbackPerKindLevelPool(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	saveBotConfig(t)
	MaintainerChatID = "300"
	poolTarget, poolMin = 300, 100

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
	if v, ok := poolKindLevelOverride(kindWord, levelUpperInt); !ok || v != 400 {
		t.Fatalf("poolKindLevelOverride(word, upper-int) = %d,%v; want 400,true", v, ok)
	}
	if v, ok := store.GetBotConfig("pool_kl_word_upper-intermediate"); !ok || v != "400" {
		t.Errorf("bot_config pool_kl_word_upper-intermediate = %q (ok=%v), want '400'", v, ok)
	}
	// The exact pool gets the override...
	if got := poolTargetFor(kindWord, levelUpperInt); got != 400 {
		t.Errorf("poolTargetFor(word, upper-int) = %d, want 400 (kind+level override)", got)
	}
	// ...but the same kind at another level, and another kind at this level, do not.
	if got := poolTargetFor(kindWord, levelAdvanced); got != 100 {
		t.Errorf("poolTargetFor(word, advanced) = %d, want 100 (global min)", got)
	}
	if got := poolTargetFor(kindStory, levelUpperInt); got != 100 {
		t.Errorf("poolTargetFor(story, upper-int) = %d, want 100 (global min)", got)
	}

	// Clearing (n=0) removes the override and the persisted key.
	set("cfg:kl:word:upper-intermediate:0")
	if _, ok := poolKindLevelOverride(kindWord, levelUpperInt); ok {
		t.Error("expected per-(kind,level) override cleared")
	}
	if _, ok := store.GetBotConfig("pool_kl_word_upper-intermediate"); ok {
		t.Error("expected pool_kl_word_upper-intermediate deleted from bot_config")
	}
	if got := poolTargetFor(kindWord, levelUpperInt); got != 100 {
		t.Errorf("poolTargetFor(word, upper-int) after clear = %d, want 100", got)
	}
}

// TestResolvePoolTargetPrecedence verifies the most-specific-wins ordering:
// per-(kind,level) → per-kind → per-level → global.
func TestResolvePoolTargetPrecedence(t *testing.T) {
	saveBotConfig(t)
	poolTarget, poolMin = 300, 100

	// Global only.
	if got := resolvePoolTarget(kindWord, levelUpperInt); got != 100 {
		t.Errorf("global: got %d, want 100", got)
	}
	// Per-level applies.
	setPoolLevelOverride(levelUpperInt, 250)
	if got := resolvePoolTarget(kindWord, levelUpperInt); got != 250 {
		t.Errorf("per-level: got %d, want 250", got)
	}
	// Per-kind beats per-level.
	setPoolKindOverride(kindWord, 175)
	if got := resolvePoolTarget(kindWord, levelUpperInt); got != 175 {
		t.Errorf("per-kind: got %d, want 175", got)
	}
	// Per-(kind,level) beats both.
	setPoolKindLevelOverride(kindWord, levelUpperInt, 400)
	if got := resolvePoolTarget(kindWord, levelUpperInt); got != 400 {
		t.Errorf("per-(kind,level): got %d, want 400", got)
	}
	// A different kind at the same level still sees the per-level value.
	if got := resolvePoolTarget(kindStory, levelUpperInt); got != 250 {
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

	if v, ok := poolKindLevelOverride(kindWord, levelUpperInt); !ok || v != 400 {
		t.Errorf("after load: poolKindLevelOverride(word, upper-int) = %d,%v; want 400,true", v, ok)
	}
	if _, ok := poolKindLevelOverride("bogus", levelAdvanced); ok {
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
	MaintainerChatID = "999"

	// Non-maintainer is refused.
	handleMessage(context.Background(), emptyProviderChain(), store, mock, &TelegramMessage{
		MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/admin", From: &TelegramUser{ID: 100},
	})
	if len(mock.sentTexts()) == 0 || !strings.Contains(mock.sentTexts()[0], "maintainer") {
		t.Fatalf("expected maintainer-only refusal, got %v", mock.sentTexts())
	}
}
