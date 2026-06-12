package app

import (
	"context"
	"testing"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

func TestSendDailyTip_DeliversTip(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	config.AppLocation = time.UTC
	config.QuietStart, config.QuietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(config.KindTip, config.DefaultLevel, "used to vs would", "", "tip card")

	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected 1 tip sent, got %d", mock.sentCount())
	}
	delivered, err := store.TipDelivered(100, "2026-06-03")
	if err != nil {
		t.Fatalf("TipDelivered: %v", err)
	}
	if !delivered {
		t.Fatal("expected tip marked delivered")
	}
}

func TestSendDailyTip_SkipsPausedAndDisabled(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	config.AppLocation = time.UTC
	config.QuietStart, config.QuietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddSubscriber(101)
	store.SetPaused(100, true)
	store.SetTipsEnabled(101, false)
	store.AddToPool(config.KindTip, config.DefaultLevel, "conditionals", "", "tip card")

	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no tips sent, got %d", mock.sentCount())
	}
}

func TestSendDailyTip_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	resetHourlyLimiter(t)
	config.AppLocation = time.UTC
	config.QuietStart, config.QuietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(config.KindTip, config.DefaultLevel, "articles", "", "tip card")

	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 1 {
		t.Fatalf("expected idempotent send count=1, got %d", mock.sentCount())
	}
}

func TestSendDailyTip_SkipsQuietHours(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	config.AppLocation = time.UTC
	config.QuietStart, config.QuietEnd = "00:00", "23:59"

	store.AddSubscriber(100)
	store.AddToPool(config.KindTip, config.DefaultLevel, "prepositions", "", "tip card")

	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no sends during quiet hours, got %d", mock.sentCount())
	}
}
