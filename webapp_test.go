package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// saveWebAppURL snapshots the WEB_APP_URL global and restores it after the test
// so toggling it never leaks into other tests.
func saveWebAppURL(t *testing.T) {
	t.Helper()
	orig := config.WebAppURL
	t.Cleanup(func() { config.WebAppURL = orig })
}

// TestStatsButtonRequiresWebAppURL is the regression guard for the "no Dashboard
// button / 502" bug: the button only appears when WEB_APP_URL is set, and when
// it is the button must open <WEB_APP_URL>/stats as a Telegram web_app.
func TestStatsButtonRequiresWebAppURL(t *testing.T) {
	saveWebAppURL(t)

	newStatsMsg := func() *TelegramMessage {
		return &TelegramMessage{
			MessageID: 1,
			Chat:      TelegramChat{ID: 100},
			Text:      "/stats",
			From:      &TelegramUser{ID: 100, Username: "testuser"},
		}
	}

	// 1. Unset → plain text, no keyboard button.
	t.Run("unset sends plain text", func(t *testing.T) {
		store := testStoreHelper(t)
		mock := &mockNotifier{}
		config.WebAppURL = ""

		handleMessage(context.Background(), emptyProviderChain(), store, mock, newStatsMsg())

		if len(mock.keyboard) != 0 {
			t.Errorf("expected no keyboard when WEB_APP_URL unset, got %d", len(mock.keyboard))
		}
		if mock.sentCount() == 0 {
			t.Error("expected a plain-text stats message when WEB_APP_URL unset")
		}
	})

	// 2. Set → Dashboard button opening <url>/stats.
	t.Run("set sends web_app button", func(t *testing.T) {
		store := testStoreHelper(t)
		mock := &mockNotifier{}
		config.WebAppURL = "https://bot.example.com"

		handleMessage(context.Background(), emptyProviderChain(), store, mock, newStatsMsg())

		if len(mock.keyboard) != 1 {
			t.Fatalf("expected exactly 1 keyboard message when WEB_APP_URL set, got %d", len(mock.keyboard))
		}
		kb := mock.keyboard[0].keyboard
		if len(kb) == 0 || len(kb[0]) == 0 {
			t.Fatal("expected a button row in the stats keyboard")
		}
		btn := kb[0][0]
		if btn.WebApp == nil {
			t.Fatalf("expected a web_app button, got %+v", btn)
		}
		if want := "https://bot.example.com/stats"; btn.WebApp.URL != want {
			t.Errorf("web_app URL = %q, want %q", btn.WebApp.URL, want)
		}
		if !strings.Contains(btn.Text, "Dashboard") {
			t.Errorf("button text = %q, want it to mention Dashboard", btn.Text)
		}
	})
}
