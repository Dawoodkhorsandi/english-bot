package app

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// at builds a time at the given local hour:minute on a fixed date, in the app's
// scheduling location, for deterministic slot math.
func at(hour, min int) time.Time {
	return time.Date(2026, 6, 20, hour, min, 0, 0, config.AppLocation)
}

func TestFeedKindForSlot(t *testing.T) {
	// interval=60: even hour-slots → drill, odd → word (mirrors dueAndKind).
	cases := []struct {
		hour, min, interval int
		want                string
	}{
		{0, 30, 60, config.KindDrill}, // slot 0
		{1, 0, 60, config.KindWord},   // slot 1
		{2, 15, 60, config.KindDrill}, // slot 2
		{0, 0, 0, config.KindDrill},   // interval<=0 falls back to default (60), slot 0
	}
	for _, c := range cases {
		if got := feedKindForSlot(at(c.hour, c.min), c.interval); got != c.want {
			t.Errorf("feedKindForSlot(%02d:%02d, %d) = %q, want %q", c.hour, c.min, c.interval, got, c.want)
		}
	}
}

func TestFeedNextSelectsKindAndDoesNotRecord(t *testing.T) {
	store := testStoreHelper(t)
	const chatID = 100

	store.AddToPool(config.KindWord, config.DefaultLevel, "vigorous", "energetic", "<b>word card</b>")
	store.AddToPool(config.KindDrill, config.DefaultLevel, "walk", "", "<b>drill card</b>")

	// Explicit kind override returns that kind.
	kind, term, _, text, err := store.FeedNext(chatID, at(1, 0), config.KindWord)
	if err != nil {
		t.Fatalf("FeedNext word: %v", err)
	}
	if kind != config.KindWord || term != "vigorous" || text != "<b>word card</b>" {
		t.Errorf("FeedNext word = (%q,%q,%q), want word/vigorous/<b>word card</b>", kind, term, text)
	}

	// No override at a drill slot (00:30, interval default 60) → drill.
	kind, term, _, _, err = store.FeedNext(chatID, at(0, 30), "")
	if err != nil {
		t.Fatalf("FeedNext slot: %v", err)
	}
	if kind != config.KindDrill || term != "walk" {
		t.Errorf("FeedNext slot = (%q,%q), want drill/walk", kind, term)
	}

	// The feed is read-only: it must not record to the user's history (so it can't
	// perturb broadcast dedup / SRS). sent_vocab stays empty.
	var n int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM sent_vocab WHERE chat_id = ?", chatID).Scan(&n); err != nil {
		t.Fatalf("count sent_vocab: %v", err)
	}
	if n != 0 {
		t.Errorf("sent_vocab count = %d after FeedNext, want 0 (feed must not record)", n)
	}
}

func TestFeedNextEmptyPool(t *testing.T) {
	store := testStoreHelper(t)
	if _, _, _, _, err := store.FeedNext(100, at(1, 0), config.KindWord); err != errPoolEmpty {
		t.Errorf("FeedNext on empty pool err = %v, want errPoolEmpty", err)
	}
}

func TestAPIFeedNextPreservesHTML(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 100

	const raw = "<b>vigorous</b> <tg-spoiler>با نیرو</tg-spoiler>"
	store.AddToPool(config.KindWord, config.DefaultLevel, "vigorous", "energetic", raw)

	w := apiCall(store, handleAPIFeedNext, http.MethodGet, "/api/feed/next?kind=word", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Available bool   `json:"available"`
		Kind      string `json:"kind"`
		Term      string `json:"term"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if !resp.Available || resp.Kind != config.KindWord || resp.Term != "vigorous" {
		t.Errorf("resp = %+v, want available word/vigorous", resp)
	}
	// HTML must be preserved (not stripped) so the app can render rich bubbles.
	if resp.Text != raw {
		t.Errorf("text = %q, want raw HTML %q", resp.Text, raw)
	}
}

func TestAPIFeedNextRejectsBadKind(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	w := apiCall(store, handleAPIFeedNext, http.MethodGet, "/api/feed/next?kind=bogus", 100, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad kind status = %d, want 400", w.Code)
	}
}

func TestAPILookupRejectsSentence(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	chain := emptyProviderChain() // never reached for the guard path
	handler := func(w http.ResponseWriter, r *http.Request, chatID int64, st *Store) {
		handleAPILookup(w, r, chatID, st, chain)
	}
	w := apiCall(store, handler, http.MethodGet, "/api/lookup?term=this+is+a+long+sentence+here", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Available bool `json:"available"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Available {
		t.Errorf("sentence input: available = true, want false")
	}
}

func TestAPILookupSuccess(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 100

	const card = "📘 <b>Word of the Session: serendipity</b>\n————————————————————\n\n💬 <b>Meaning</b>\nA pleasant surprise.\n"
	chain := mockProviderChain(card)
	handler := func(w http.ResponseWriter, r *http.Request, chatID int64, st *Store) {
		handleAPILookup(w, r, chatID, st, chain)
	}

	w := apiCall(store, handler, http.MethodGet, "/api/lookup?term=serendipity", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Available bool   `json:"available"`
		Term      string `json:"term"`
		Text      string `json:"text"`
		Meaning   string `json:"meaning"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if !resp.Available || resp.Term != "serendipity" || resp.Text != card {
		t.Errorf("resp = %+v, want available serendipity with full card", resp)
	}

	// A successful lookup pools and records the word (counts toward stats / SRS).
	var n int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM sent_vocab WHERE chat_id = ? AND word = ?", chatID, "serendipity").Scan(&n); err != nil {
		t.Fatalf("count sent_vocab: %v", err)
	}
	if n != 1 {
		t.Errorf("sent_vocab count = %d, want 1 (lookup must record)", n)
	}
}
