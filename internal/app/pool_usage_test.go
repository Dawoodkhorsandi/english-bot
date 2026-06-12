package app

import (
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// TestPoolUsageLeaderPicksBusiestUser verifies that PoolUsageLeader returns the
// chat that has seen the most of the currently-pooled items, counted only over
// items still in the pool.
func TestPoolUsageLeaderPicksBusiestUser(t *testing.T) {
	store := testStoreHelper(t)

	store.AddToPool(config.KindWord, config.DefaultLevel, "apple", "a fruit", "card: apple")
	store.AddToPool(config.KindWord, config.DefaultLevel, "banana", "a fruit", "card: banana")
	store.AddToPool(config.KindWord, config.DefaultLevel, "cherry", "a fruit", "card: cherry")
	store.AddToPool(config.KindWord, config.DefaultLevel, "date", "a fruit", "card: date")

	// Chat 100 has seen 3 of 4 pooled words; chat 101 only 1.
	for _, w := range []string{"apple", "banana", "cherry"} {
		if err := store.RecordSentVocab(100, w); err != nil {
			t.Fatalf("record vocab 100: %v", err)
		}
	}
	if err := store.RecordSentVocab(101, "apple"); err != nil {
		t.Fatalf("record vocab 101: %v", err)
	}
	// Chat 100 has also seen a word that is no longer pooled — must NOT count.
	if err := store.RecordSentVocab(100, "obsolete"); err != nil {
		t.Fatalf("record obsolete: %v", err)
	}

	leader, seen, ok, err := store.PoolUsageLeader(config.KindWord, config.DefaultLevel)
	if err != nil {
		t.Fatalf("PoolUsageLeader: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if leader != 100 {
		t.Errorf("leader = %d, want 100", leader)
	}
	if seen != 3 {
		t.Errorf("seen = %d, want 3 (only pooled items count)", seen)
	}
}

// TestPoolUsageLeaderEmptyPool verifies ok=false when nothing matches.
func TestPoolUsageLeaderEmptyPool(t *testing.T) {
	store := testStoreHelper(t)

	if _, _, ok, err := store.PoolUsageLeader(config.KindWord, config.DefaultLevel); err != nil || ok {
		t.Fatalf("expected ok=false on empty pool, got ok=%v err=%v", ok, err)
	}

	// Pool has items but no user has seen any of them.
	store.AddToPool(config.KindWord, config.DefaultLevel, "apple", "a fruit", "card: apple")
	if _, _, ok, err := store.PoolUsageLeader(config.KindWord, config.DefaultLevel); err != nil || ok {
		t.Fatalf("expected ok=false when nobody has seen the pool, got ok=%v err=%v", ok, err)
	}
}

// TestHandlePoolUsageReport exercises the /poolusage handler end to end and checks
// the percentage and busiest-chat reporting.
func TestHandlePoolUsageReport(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveBotConfig(t)
	config.PoolTarget, config.PoolMin = 300, 100 // pin the target so "pool 4/300" is deterministic

	// A 4-item word pool at the default level; chat 100 has consumed 2 → 50%.
	store.AddToPool(config.KindWord, config.DefaultLevel, "apple", "a fruit", "card: apple")
	store.AddToPool(config.KindWord, config.DefaultLevel, "banana", "a fruit", "card: banana")
	store.AddToPool(config.KindWord, config.DefaultLevel, "cherry", "a fruit", "card: cherry")
	store.AddToPool(config.KindWord, config.DefaultLevel, "date", "a fruit", "card: date")
	for _, w := range []string{"apple", "banana"} {
		if err := store.RecordSentVocab(100, w); err != nil {
			t.Fatalf("record vocab: %v", err)
		}
	}

	handlePoolUsage(store, mock, 999)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) != 1 {
		t.Fatalf("expected exactly 1 report message, got %d", len(mock.sent))
	}
	report := mock.sent[0].text
	if !strings.Contains(report, "Pool usage") {
		t.Errorf("report missing title: %q", report)
	}
	// word/<level>: 50% (chat 100 saw 2/4) · pool 4/300
	// config.PoolTarget defaults to 300 and the word pool here is at the default level,
	// so the configured target is 300 while only 4 items exist (pool 4/300).
	wantLine := config.KindWord + "/" + config.DefaultLevel + ": <b>50%</b> (chat 100 saw 2/4) · pool 4/300"
	if !strings.Contains(report, wantLine) {
		t.Errorf("report missing expected usage line %q in:\n%s", wantLine, report)
	}
}
