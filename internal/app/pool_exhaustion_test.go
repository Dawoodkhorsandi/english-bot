package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// maintainerMessages returns the messages the mock captured for the maintainer
// chat id (parsed from config.MaintainerChatID).
func maintainerMessages(t *testing.T, mock *mockNotifier) []string {
	t.Helper()
	var out []string
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, m := range mock.sent {
		if m.chatID == 999 {
			out = append(out, m.text)
		}
	}
	return out
}

// TestPoolExhaustionNotifiesMaintainer verifies that when a broadcast serve finds
// the user has seen every pooled item (recycle path), the maintainer is alerted,
// and the alert is deduped until the pool grows.
func TestPoolExhaustionNotifiesMaintainer(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	config.MaintainerChatID = "999"

	store.AddSubscriber(100)
	// A single-item pool: the first broadcast serve is unseen; the next is a repeat.
	store.AddToPool(config.KindDrill, config.DefaultLevel, "walk", "", "drill: walk")

	ctx := context.Background()
	// First serve: unseen → no exhaustion notice.
	if _, _, err := serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindDrill, config.DefaultLevel, false); err != nil {
		t.Fatalf("serve 1: %v", err)
	}
	if got := maintainerMessages(t, mock); len(got) != 0 {
		t.Fatalf("expected no maintainer alert after first (unseen) serve, got %v", got)
	}

	// Second serve: everything seen → recycle path → maintainer alerted once.
	if _, _, err := serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindDrill, config.DefaultLevel, false); err != nil {
		t.Fatalf("serve 2: %v", err)
	}
	msgs := maintainerMessages(t, mock)
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 maintainer alert, got %d (%v)", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "Pool exhausted") || !strings.Contains(msgs[0], "drill") {
		t.Errorf("alert text unexpected: %q", msgs[0])
	}

	// Third serve: still exhausted, pool unchanged → no duplicate alert.
	if _, _, err := serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindDrill, config.DefaultLevel, false); err != nil {
		t.Fatalf("serve 3: %v", err)
	}
	if got := maintainerMessages(t, mock); len(got) != 1 {
		t.Fatalf("expected alert to be deduped (still 1), got %d", len(got))
	}
}

// TestPoolExhaustionReAlertsAfterGrowth verifies the maintainer is alerted again
// after the pool is grown and the user re-exhausts it.
func TestPoolExhaustionReAlertsAfterGrowth(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	config.MaintainerChatID = "999"

	store.AddSubscriber(100)
	store.AddToPool(config.KindWord, config.DefaultLevel, "apple", "a fruit", "card: apple")

	ctx := context.Background()
	// Serve once (unseen) then again (recycle → first alert).
	_, _, _ = serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindWord, config.DefaultLevel, false)
	_, _, _ = serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindWord, config.DefaultLevel, false)
	if got := maintainerMessages(t, mock); len(got) != 1 {
		t.Fatalf("expected 1 alert before growth, got %d", len(got))
	}

	// Admin grows the pool. The user serves the new (unseen) item, then re-exhausts.
	store.AddToPool(config.KindWord, config.DefaultLevel, "banana", "a fruit", "card: banana")
	_, _, _ = serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindWord, config.DefaultLevel, false) // serves unseen "banana"
	_, _, _ = serveContent(ctx, emptyProviderChain(), store, mock, 100, config.KindWord, config.DefaultLevel, false) // recycle → re-alert

	if got := maintainerMessages(t, mock); len(got) != 2 {
		t.Fatalf("expected a second alert after pool growth + re-exhaustion, got %d", len(got))
	}
}

// TestPoolExhaustionNilNotifier verifies the recycle path is safe when no
// notifier is supplied (e.g. internal/test serves) and no maintainer is needed.
func TestPoolExhaustionNilNotifier(t *testing.T) {
	store := testStoreHelper(t)
	store.AddSubscriber(100)
	store.AddToPool(config.KindDrill, config.DefaultLevel, "walk", "", "drill: walk")

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := serveContent(ctx, emptyProviderChain(), store, nil, 100, config.KindDrill, config.DefaultLevel, false); err != nil {
			t.Fatalf("serve %d with nil notifier: %v", i, err)
		}
	}
}

// TestPoolExhaustionStoreRoundTrip exercises the store methods directly.
func TestPoolExhaustionStoreRoundTrip(t *testing.T) {
	store := testStoreHelper(t)

	if _, ok, err := store.PoolExhaustionNotice(100, config.KindStory, config.DefaultLevel); err != nil || ok {
		t.Fatalf("expected no notice initially, got ok=%v err=%v", ok, err)
	}
	if err := store.MarkPoolExhaustionNotice(100, config.KindStory, config.DefaultLevel, 80); err != nil {
		t.Fatalf("MarkPoolExhaustionNotice: %v", err)
	}
	got, ok, err := store.PoolExhaustionNotice(100, config.KindStory, config.DefaultLevel)
	if err != nil || !ok || got != 80 {
		t.Fatalf("PoolExhaustionNotice = %d,%v,%v; want 80,true,nil", got, ok, err)
	}
	// Upsert to a new count.
	if err := store.MarkPoolExhaustionNotice(100, config.KindStory, config.DefaultLevel, 150); err != nil {
		t.Fatalf("MarkPoolExhaustionNotice upsert: %v", err)
	}
	if got, _, _ := store.PoolExhaustionNotice(100, config.KindStory, config.DefaultLevel); got != 150 {
		t.Errorf("after upsert got %d, want 150", got)
	}
}
