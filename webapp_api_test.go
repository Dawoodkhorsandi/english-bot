package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers — forge valid Telegram initData for a chat ID.
// ---------------------------------------------------------------------------

const testBotToken = "123456:test-bot-token"

// saveToken pins TelegramBotToken for the duration of a test.
func saveToken(t *testing.T) {
	t.Helper()
	orig := TelegramBotToken
	TelegramBotToken = testBotToken
	t.Cleanup(func() { TelegramBotToken = orig })
}

// signInitData builds a valid initData query string for chatID, signed with the
// current TelegramBotToken, stamped at authDate.
func signInitData(chatID int64, authDate time.Time) string {
	vals := map[string]string{
		"user":      fmt.Sprintf(`{"id":%d,"first_name":"Test"}`, chatID),
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
	}
	var pairs []string
	for k, v := range vals {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	check := strings.Join(pairs, "\n")

	h1 := hmac.New(sha256.New, []byte("WebAppData"))
	h1.Write([]byte(TelegramBotToken))
	secret := h1.Sum(nil)
	h2 := hmac.New(sha256.New, secret)
	h2.Write([]byte(check))
	hash := hex.EncodeToString(h2.Sum(nil))

	q := url.Values{}
	for k, v := range vals {
		q.Set(k, v)
	}
	q.Set("hash", hash)
	return q.Encode()
}

// apiCall invokes a withUser-wrapped handler with signed initData for chatID and
// returns the recorder. body may be nil.
func apiCall(store *Store, handler apiHandler, method, target string, chatID int64, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("X-Init-Data", signInitData(chatID, time.Now()))
	w := httptest.NewRecorder()
	withUser(store, handler)(w, r)
	return w
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

func TestWithUserRejectsBadInitData(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	// No init data.
	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	w := httptest.NewRecorder()
	withUser(store, handleAPIStats)(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing initData: code = %d, want 401", w.Code)
	}

	// Tampered hash.
	bad := signInitData(100, time.Now()) + "tampered"
	r = httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	r.Header.Set("X-Init-Data", bad)
	w = httptest.NewRecorder()
	withUser(store, handleAPIStats)(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("tampered initData: code = %d, want 401", w.Code)
	}
}

func TestValidateInitDataRejectsStale(t *testing.T) {
	saveToken(t)
	// auth_date well past the TTL.
	stale := signInitData(100, time.Now().Add(-2*initDataTTL))
	if _, ok := validateInitData(stale); ok {
		t.Error("expected stale initData to be rejected")
	}
	// Fresh is accepted.
	fresh := signInitData(100, time.Now())
	if id, ok := validateInitData(fresh); !ok || id != 100 {
		t.Errorf("fresh initData: id=%d ok=%v, want 100/true", id, ok)
	}
}

// ---------------------------------------------------------------------------
// Vocabulary
// ---------------------------------------------------------------------------

func TestAPIVocabSearchAndBookmark(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	for _, w := range []string{"apple", "apricot", "banana"} {
		if err := store.RecordSentVocab(100, w); err != nil {
			t.Fatal(err)
		}
	}
	store.AddToPool(kindWord, defaultLevel, "apple", "a round fruit", "card")

	// Search "ap" → apple + apricot (term match), and "round" → apple (meaning).
	w := apiCall(store, handleAPIVocab, http.MethodGet, "/api/vocab?q=ap&limit=50", 100, "")
	var resp struct {
		Items []LearnedWord `json:"items"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if resp.Total != 2 {
		t.Errorf("search 'ap' total = %d, want 2", resp.Total)
	}

	// Bookmark via API, then bookmarks-only filter returns it.
	bw := apiCall(store, handleAPIBookmark, http.MethodPost, "/api/bookmark", 100, `{"term":"banana","on":true}`)
	if bw.Code != http.StatusOK {
		t.Fatalf("bookmark POST code = %d", bw.Code)
	}
	if !store.IsBookmarked(100, "banana") {
		t.Error("banana should be bookmarked after API call")
	}
	w = apiCall(store, handleAPIVocab, http.MethodGet, "/api/vocab?bookmarks=1&limit=50", 100, "")
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].Term != "banana" {
		t.Errorf("bookmarks filter = %+v, want only banana", resp.Items)
	}
}

// ---------------------------------------------------------------------------
// Leaderboard
// ---------------------------------------------------------------------------

func TestLeaderboardRankingAndNames(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	seed := func(chatID int64, n int) {
		for i := 0; i < n; i++ {
			if err := store.RecordSentVocab(chatID, fmt.Sprintf("w%d_%d", chatID, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(100, 5)
	seed(200, 9)
	seed(300, 2)
	store.SetDisplayName(200, "Champion")

	rows, myRank, myValue, err := store.Leaderboard("words", 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Value != 9 || rows[0].Name != "Champion" {
		t.Errorf("top row = %+v, want Champion with 9", rows[0])
	}
	if rows[2].Value != 2 || !rows[2].IsMe {
		t.Errorf("last row = %+v, want me (300) with 2", rows[2])
	}
	// chat 100/300 have no display name → stable funny names, not "Anonymous".
	if strings.Contains(rows[2].Name, "Anonymous") || rows[2].Name == "" {
		t.Errorf("expected funny fallback name, got %q", rows[2].Name)
	}
	if myRank != 3 || myValue != 2 {
		t.Errorf("myRank=%d myValue=%d, want 3/2", myRank, myValue)
	}
}

func TestFunnyNameStable(t *testing.T) {
	if funnyName(12345) != funnyName(12345) {
		t.Error("funnyName must be deterministic")
	}
	if funnyName(12345) == "" {
		t.Error("funnyName must be non-empty")
	}
}

func TestSanitizeDisplayName(t *testing.T) {
	cases := map[string]string{
		"  Word <b>Ninja</b>  ":            "Word bNinja/b",
		"a very long name exceeding limit": "a very long name exceedi",
	}
	for in, want := range cases {
		if got := sanitizeDisplayName(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPILeaderboardName(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	w := apiCall(store, handleAPILeaderboardName, http.MethodPost, "/api/leaderboard/name", 100, `{"name":"WordNinja"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if store.GetDisplayName(100) != "WordNinja" {
		t.Errorf("display name = %q, want WordNinja", store.GetDisplayName(100))
	}
}

// ---------------------------------------------------------------------------
// Review
// ---------------------------------------------------------------------------

func TestAPIReviewFlow(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	now := time.Now()
	if err := store.SeedReview(100, "ephemeral", now); err != nil {
		t.Fatal(err)
	}
	// Force it due now.
	if _, err := store.db.Exec(
		"UPDATE review_schedule SET due_at = ? WHERE chat_id = ? AND word = ?",
		now.Add(-time.Hour).UTC().Format(srsTimeLayout), 100, "ephemeral",
	); err != nil {
		t.Fatal(err)
	}

	w := apiCall(store, handleAPIReviewNext, http.MethodGet, "/api/review/next", 100, "")
	var resp struct {
		Items []map[string]interface{} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 1 || resp.Items[0]["term"] != "ephemeral" {
		t.Fatalf("review next = %+v, want ephemeral due", resp.Items)
	}

	// Answering "known" promotes it so it's no longer due now.
	aw := apiCall(store, handleAPIReviewAnswer, http.MethodPost, "/api/review/answer", 100, `{"term":"ephemeral","known":true}`)
	if aw.Code != http.StatusOK {
		t.Fatalf("answer code = %d", aw.Code)
	}
	due, _ := store.DueReviews(100, time.Now(), 10)
	if len(due) != 0 {
		t.Errorf("after known answer, due = %d, want 0", len(due))
	}
}

// ---------------------------------------------------------------------------
// Leitner decks
// ---------------------------------------------------------------------------

func TestLeitnerNextBox(t *testing.T) {
	if got := leitnerNextBox(1, true); got != 2 {
		t.Errorf("promote from 1 = %d, want 2", got)
	}
	if got := leitnerNextBox(leitnerMaxBox, true); got != leitnerMaxBox {
		t.Errorf("promote at cap = %d, want %d", got, leitnerMaxBox)
	}
	if got := leitnerNextBox(4, false); got != 1 {
		t.Errorf("miss resets to %d, want 1", got)
	}
}

func TestDeckSeedStudySwipe(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	if err := store.SeedDecks(); err != nil {
		t.Fatal(err)
	}

	decks, err := store.Decks(100)
	if err != nil || len(decks) == 0 {
		t.Fatalf("decks = %+v err=%v, want at least one", decks, err)
	}
	deckID := decks[0].ID
	if decks[0].Total == 0 || decks[0].Due == 0 {
		t.Errorf("seeded deck should have cards due: %+v", decks[0])
	}

	cards, err := store.DeckStudy(100, deckID, 5)
	if err != nil || len(cards) == 0 {
		t.Fatalf("study cards = %d err=%v", len(cards), err)
	}
	if cards[0].Box != 0 {
		t.Errorf("a never-seen card should report box 0, got %d", cards[0].Box)
	}
	term := cards[0].Term
	dueBefore := decks[0].Due

	// Swipe known → card moves to box 2 (due tomorrow), so it leaves the due set.
	if err := store.DeckSwipe(100, deckID, term, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	decks, _ = store.Decks(100)
	if decks[0].Due != dueBefore-1 {
		t.Errorf("due after known swipe = %d, want %d", decks[0].Due, dueBefore-1)
	}
	next, _ := store.DeckStudy(100, deckID, 50)
	for _, c := range next {
		if c.Term == term {
			t.Errorf("promoted card %q should not be due immediately", term)
		}
	}

	// Swipe forgot on a fresh card → stays in box 1 (due now).
	other := next[0].Term
	if err := store.DeckSwipe(100, deckID, other, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	again, _ := store.DeckStudy(100, deckID, 50)
	found := false
	for _, c := range again {
		if c.Term == other {
			found = true
		}
	}
	if !found {
		t.Errorf("forgotten card %q should remain due", other)
	}
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

func TestAPISettingsRoundTrip(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	// Toggle idiom off and change level via POST.
	if w := apiCall(store, handleAPISettings, http.MethodPost, "/api/settings", 100, `{"key":"idiom","value":false}`); w.Code != http.StatusOK {
		t.Fatalf("toggle code = %d", w.Code)
	}
	if w := apiCall(store, handleAPISettings, http.MethodPost, "/api/settings", 100, `{"key":"level","value":"advanced"}`); w.Code != http.StatusOK {
		t.Fatalf("level code = %d", w.Code)
	}

	w := apiCall(store, handleAPISettings, http.MethodGet, "/api/settings", 100, "")
	var resp struct {
		Level   string          `json:"level"`
		Toggles map[string]bool `json:"toggles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Level != "advanced" {
		t.Errorf("level = %q, want advanced", resp.Level)
	}
	if resp.Toggles["idiom"] {
		t.Error("idiom toggle should be false after update")
	}

	// Invalid level rejected.
	if w := apiCall(store, handleAPISettings, http.MethodPost, "/api/settings", 100, `{"key":"level","value":"wizard"}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid level code = %d, want 400", w.Code)
	}
}
