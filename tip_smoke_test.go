package main

import (
	"context"
	"testing"
	"time"
)

func TestSendDailyTip_DeliversTip(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindTip, defaultLevel, "used to vs would", "", "tip card")

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
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddSubscriber(101)
	store.SetPaused(100, true)
	store.SetTipsEnabled(101, false)
	store.AddToPool(kindTip, defaultLevel, "conditionals", "", "tip card")

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
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "00:00"

	store.AddSubscriber(100)
	store.AddToPool(kindTip, defaultLevel, "articles", "", "tip card")

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
	appLocation = time.UTC
	quietStart, quietEnd = "00:00", "23:59"

	store.AddSubscriber(100)
	store.AddToPool(kindTip, defaultLevel, "prepositions", "", "tip card")

	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	sendDailyTip(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() != 0 {
		t.Fatalf("expected no sends during quiet hours, got %d", mock.sentCount())
	}
}
