package app

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

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Test helpers — forge valid Telegram initData for a chat ID.
// ---------------------------------------------------------------------------

const testBotToken = "123456:test-bot-token"

// saveToken pins config.TelegramBotToken for the duration of a test.
func saveToken(t *testing.T) {
	t.Helper()
	orig := config.TelegramBotToken
	config.TelegramBotToken = testBotToken
	t.Cleanup(func() { config.TelegramBotToken = orig })
}

// signInitData builds a valid initData query string for chatID, signed with the
// current config.TelegramBotToken, stamped at authDate.
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
	h1.Write([]byte(config.TelegramBotToken))
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
	if _, _, ok := validateInitData(stale); ok {
		t.Error("expected stale initData to be rejected")
	}
	// Fresh is accepted.
	fresh := signInitData(100, time.Now())
	if id, _, ok := validateInitData(fresh); !ok || id != 100 {
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
	store.AddToPool(config.KindWord, config.DefaultLevel, "apple", "a round fruit", "card")

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
	if a, b := funnyName(12345), funnyName(12345); a != b {
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

// TestAPIReviewDedupAndEnrich verifies a word present in content_pool at more
// than one level yields a single review card (not one per level), and that the
// payload carries pronunciation/Persian/example parsed from the pooled card.
func TestAPIReviewDedupAndEnrich(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 100

	card := "📘 <b>Word of the Session: vigorous</b>\n" +
		"💬 <b>Meaning</b>\nfull of energy and strength\n" +
		"🔊 <b>Pronunciation</b>\nVIG-er-us · /ˈvɪɡ.ɚ.əs/\n" +
		"📝 <b>Examples</b>\n• She took a <b>vigorous</b> walk.\n" +
		"🇮🇷 <b>Persian</b>\n<tg-spoiler>پرانرژی</tg-spoiler>"
	// Same term pooled at two levels — the old query returned a row per level.
	if err := store.AddToPool(config.KindWord, "beginner", "vigorous", "full of energy and strength", card); err != nil {
		t.Fatal(err)
	}
	if err := store.AddToPool(config.KindWord, "advanced", "vigorous", "full of energy and strength", card); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.SeedReview(chatID, "vigorous", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"UPDATE review_schedule SET due_at = ? WHERE chat_id = ? AND word = ?",
		now.Add(-time.Hour).UTC().Format(srsTimeLayout), chatID, "vigorous",
	); err != nil {
		t.Fatal(err)
	}

	// Store-level dedup.
	due, err := store.DueReviews(chatID, time.Now(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("DueReviews returned %d rows for one word at two levels, want 1", len(due))
	}

	// API payload carries the parsed fields.
	w := apiCall(store, handleAPIReviewNext, http.MethodGet, "/api/review/next", chatID, "")
	var resp struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("payload items = %d, want 1", len(resp.Items))
	}
	it := resp.Items[0]
	if it["pronunciation"] != "VIG-er-us · /ˈvɪɡ.ɚ.əs/" {
		t.Errorf("pronunciation = %q", it["pronunciation"])
	}
	if it["persian"] != "پرانرژی" {
		t.Errorf("persian = %q", it["persian"])
	}
	if it["example"] != "She took a vigorous walk." {
		t.Errorf("example = %q", it["example"])
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

func TestDeckEnrichmentAndDetail(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 321

	// Seed a tiny deck via SeedDecks-like UPSERT directly: insert one rich card.
	if _, err := store.db.Exec(`
		INSERT INTO deck_cards (deck_id, term, definition, example, group_label, ordering, persian, pronunciation, mnemonic)
		VALUES ('504','vigorous','full of energy','She is vigorous.','Lesson 1',0,'پرانرژی','/ˈvɪɡ.ɚ.əs/','vig = vigor')`); err != nil {
		t.Fatal(err)
	}

	// Study payload carries the enriched fields.
	w := apiCall(store, handleAPIDeckStudy, http.MethodGet, "/api/decks/study?deck=504", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("study code = %d (%s)", w.Code, w.Body.String())
	}
	var study struct {
		Items []DeckStudyCard `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &study); err != nil {
		t.Fatal(err)
	}
	if len(study.Items) != 1 {
		t.Fatalf("study items = %d, want 1", len(study.Items))
	}
	c := study.Items[0]
	if c.Persian != "پرانرژی" || c.Pronunciation != "/ˈvɪɡ.ɚ.əs/" || c.Mnemonic != "vig = vigor" {
		t.Errorf("enriched fields missing: %+v", c)
	}

	// Detail: one new card, box distribution sums correctly, study a card → box 2.
	if err := store.DeckSwipe(chatID, "504", "vigorous", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	dw := apiCall(store, handleAPIDeckDetail, http.MethodGet, "/api/decks/detail?deck=504", chatID, "")
	if dw.Code != http.StatusOK {
		t.Fatalf("detail code = %d (%s)", dw.Code, dw.Body.String())
	}
	var det DeckDetail
	if err := json.Unmarshal(dw.Body.Bytes(), &det); err != nil {
		t.Fatal(err)
	}
	if det.Total != 1 || len(det.Boxes) != leitnerMaxBox {
		t.Fatalf("detail = %+v, want total 1 and %d boxes", det, leitnerMaxBox)
	}
	if det.Boxes[1].Count != 1 { // box 2 after a known swipe
		t.Errorf("box 2 count = %d, want 1 (%+v)", det.Boxes[1].Count, det.Boxes)
	}
	if det.New != 0 {
		t.Errorf("new = %d, want 0 after studying the only card", det.New)
	}

	// Unknown deck → 404.
	if w := apiCall(store, handleAPIDeckDetail, http.MethodGet, "/api/decks/detail?deck=nope", chatID, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown deck code = %d, want 404", w.Code)
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

	// Give the user a leaderboard name so the settings GET surfaces it.
	if err := store.SetDisplayName(100, "WordNinja"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	w := apiCall(store, handleAPISettings, http.MethodGet, "/api/settings", 100, "")
	var resp struct {
		Level       string            `json:"level"`
		LevelLabels map[string]string `json:"levelLabels"`
		Name        string            `json:"name"`
		Toggles     map[string]bool   `json:"toggles"`
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
	// Friendly labels are exposed so the UI need not show raw slugs.
	if got := resp.LevelLabels["upper-intermediate"]; got != "Upper-Intermediate" {
		t.Errorf("levelLabels[upper-intermediate] = %q, want Upper-Intermediate", got)
	}
	if resp.Name != "WordNinja" {
		t.Errorf("name = %q, want WordNinja", resp.Name)
	}

	// Invalid level rejected.
	if w := apiCall(store, handleAPISettings, http.MethodPost, "/api/settings", 100, `{"key":"level","value":"wizard"}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid level code = %d, want 400", w.Code)
	}
}

// TestAPIStatsContentKinds verifies the dashboard payload reports per-kind
// Library counts (idioms / collocations / stories / tips).
func TestAPIStatsContentKinds(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	if err := store.RecordSentIdiom(100, "break the ice"); err != nil {
		t.Fatalf("idiom: %v", err)
	}
	if err := store.RecordSentCollocation(100, "make a decision"); err != nil {
		t.Fatalf("collocation: %v", err)
	}
	if err := store.RecordSentStory(100, "The Lost Key"); err != nil {
		t.Fatalf("story: %v", err)
	}
	if err := store.RecordSentTip(100, "present perfect"); err != nil {
		t.Fatalf("tip: %v", err)
	}

	w := apiCall(store, handleAPIStats, http.MethodGet, "/api/stats", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats code = %d", w.Code)
	}
	var resp struct {
		Idioms       int `json:"idioms"`
		Collocations int `json:"collocations"`
		Stories      int `json:"stories"`
		Tips         int `json:"tips"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Idioms != 1 || resp.Collocations != 1 || resp.Stories != 1 || resp.Tips != 1 {
		t.Errorf("content counts = %+v, want all 1", resp)
	}
}

// ---------------------------------------------------------------------------
// Library (content history + quiz history)
// ---------------------------------------------------------------------------

func TestAPIContentAndQuizHistory(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 777

	// Seed an idiom the user has received; the pool keeps the card text.
	if err := store.AddToPool(config.KindIdiom, "intermediate", "break the ice",
		"start a conversation", "💬 <b>break the ice</b>\nTo make people feel relaxed."); err != nil {
		t.Fatalf("AddToPool: %v", err)
	}
	if err := store.RecordSentIdiom(chatID, "break the ice"); err != nil {
		t.Fatalf("RecordSentIdiom: %v", err)
	}

	w := apiCall(store, handleAPIContent, http.MethodGet, "/api/content?kind=idiom", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("content code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Items []libraryItem `json:"items"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("total/items = %d/%d, want 1/1", resp.Total, len(resp.Items))
	}
	if resp.Items[0].Term != "break the ice" {
		t.Errorf("term = %q", resp.Items[0].Term)
	}
	if strings.Contains(resp.Items[0].Text, "<") {
		t.Errorf("text should be stripped of HTML, got %q", resp.Items[0].Text)
	}
	if resp.Items[0].SentAt == "" {
		t.Error("sent_at should be set")
	}

	// Kinds outside the whitelist (including valid pool kinds like "word")
	// are rejected.
	for _, kind := range []string{"word", "drill", "bogus", ""} {
		if w := apiCall(store, handleAPIContent, http.MethodGet, "/api/content?kind="+kind, chatID, ""); w.Code != http.StatusBadRequest {
			t.Errorf("kind %q code = %d, want 400", kind, w.Code)
		}
	}

	// Another user sees nothing.
	w = apiCall(store, handleAPIContent, http.MethodGet, "/api/content?kind=idiom", 888, "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Total != 0 {
		t.Errorf("other user total = %d, want 0 (err %v)", resp.Total, err)
	}

	// Quiz history, most recent first.
	if err := store.RecordQuizResult(chatID, "apple", true); err != nil {
		t.Fatalf("RecordQuizResult: %v", err)
	}
	if err := store.RecordQuizResult(chatID, "pear", false); err != nil {
		t.Fatalf("RecordQuizResult: %v", err)
	}
	w = apiCall(store, handleAPIQuizzes, http.MethodGet, "/api/quizzes", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("quizzes code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var quiz struct {
		Items []quizHistoryItem `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &quiz); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if quiz.Total != 2 || len(quiz.Items) != 2 {
		t.Fatalf("quiz total/items = %d/%d, want 2/2", quiz.Total, len(quiz.Items))
	}
	if quiz.Items[0].Word != "pear" || quiz.Items[0].Correct {
		t.Errorf("first item = %+v, want most recent (pear, wrong)", quiz.Items[0])
	}
}

func TestAPILeaderboardMetricsNoPhoto(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	// Two users learned a word just now (inside the current week and today).
	if err := store.RecordSentVocab(1, "alpha"); err != nil {
		t.Fatalf("RecordSentVocab: %v", err)
	}
	if err := store.RecordSentVocab(2, "beta"); err != nil {
		t.Fatalf("RecordSentVocab: %v", err)
	}

	for _, metric := range []string{"weekly", "today"} {
		w := apiCall(store, handleAPILeaderboard, http.MethodGet, "/api/leaderboard?metric="+metric, 1, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s code = %d, want 200 (%s)", metric, w.Code, w.Body.String())
		}
		var resp struct {
			Metric string                   `json:"metric"`
			Rows   []map[string]interface{} `json:"rows"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Metric != metric {
			t.Errorf("metric = %q, want %q", resp.Metric, metric)
		}
		if len(resp.Rows) != 2 {
			t.Fatalf("%s rows = %d, want 2", metric, len(resp.Rows))
		}
		// Privacy: no profile photo field is ever exposed.
		for _, r := range resp.Rows {
			if _, ok := r["photo"]; ok {
				t.Errorf("%s rows must not expose a photo field", metric)
			}
		}
	}

	// Unknown metrics fall back to "words".
	w := apiCall(store, handleAPILeaderboard, http.MethodGet, "/api/leaderboard?metric=bogus", 1, "")
	var resp struct {
		Metric string `json:"metric"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Metric != "words" {
		t.Errorf("bogus metric = %q, want words (err %v)", resp.Metric, err)
	}
}

func TestAPIConfig(t *testing.T) {
	saveToken(t)
	prevUser, prevURL := config.BotUsername, config.WebAppURL
	config.BotUsername, config.WebAppURL = "@testbot", "https://bot.example.com"
	t.Cleanup(func() { config.BotUsername, config.WebAppURL = prevUser, prevURL })

	r := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	handleAPIConfig(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var resp struct {
		BotUsername string `json:"botUsername"`
		WebAppURL   string `json:"webAppURL"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BotUsername != "@testbot" || resp.WebAppURL != "https://bot.example.com" {
		t.Errorf("config = %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// On-demand practice + in-app quiz
// ---------------------------------------------------------------------------

func TestAPIPracticeServesAndRecords(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 555

	if err := store.AddToPool(config.KindIdiom, config.DefaultLevel, "hit the books",
		"to study hard", "💬 <b>hit the books</b>\nTo study hard."); err != nil {
		t.Fatalf("AddToPool: %v", err)
	}

	w := apiCall(store, handleAPIPractice, http.MethodGet, "/api/practice?kind=idiom", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Available bool   `json:"available"`
		Term      string `json:"term"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Available || resp.Term != "hit the books" {
		t.Fatalf("resp = %+v, want available hit the books", resp)
	}
	if strings.Contains(resp.Text, "<") {
		t.Errorf("text should be HTML-stripped, got %q", resp.Text)
	}
	// The serve was recorded to the per-user history (Library sees it).
	if n := store.ContentHistoryCount(chatID, config.KindIdiom); n != 1 {
		t.Errorf("ContentHistoryCount = %d, want 1", n)
	}

	// Kinds outside the whitelist are rejected.
	for _, kind := range []string{"story", "tip", "drill", ""} {
		if w := apiCall(store, handleAPIPractice, http.MethodGet, "/api/practice?kind="+kind, chatID, ""); w.Code != http.StatusBadRequest {
			t.Errorf("kind %q code = %d, want 400", kind, w.Code)
		}
	}

	// Empty pool for the level → graceful available=false.
	w = apiCall(store, handleAPIPractice, http.MethodGet, "/api/practice?kind=collocation", chatID, "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Available {
		t.Errorf("empty pool resp = %+v (err %v), want available=false", resp, err)
	}
}

func TestAPIQuizNextAndAnswer(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 556

	// One learned word + enough pooled distractors with meanings.
	words := map[string]string{
		"serene": "calm and peaceful", "arduous": "very difficult",
		"candid": "honest and direct", "frugal": "careful with money",
		"vivid": "very bright", "tepid": "slightly warm",
	}
	for term, meaning := range words {
		if err := store.AddToPool(config.KindWord, config.DefaultLevel, term, meaning, "card "+term); err != nil {
			t.Fatalf("AddToPool: %v", err)
		}
	}
	if err := store.RecordSentVocab(chatID, "serene"); err != nil {
		t.Fatalf("RecordSentVocab: %v", err)
	}

	w := apiCall(store, handleAPIQuizNext, http.MethodGet, "/api/quiz/next", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("next code = %d (%s)", w.Code, w.Body.String())
	}
	var q struct {
		Available bool     `json:"available"`
		Prompt    string   `json:"prompt"`
		Options   []string `json:"options"`
		Word      string   `json:"word"`
		Correct   int      `json:"correct"`
		Exp       int64    `json:"exp"`
		Token     string   `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &q); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !q.Available || len(q.Options) != quizOptionCount || q.Token == "" {
		t.Fatalf("question = %+v, want available with %d options and a token", q, quizOptionCount)
	}
	if strings.Contains(q.Prompt, "<b>") {
		t.Errorf("prompt should be HTML-stripped, got %q", q.Prompt)
	}

	// Correct answer is recorded as correct.
	body := fmt.Sprintf(`{"word":%q,"correct":%d,"exp":%d,"token":%q,"answer":%d}`,
		q.Word, q.Correct, q.Exp, q.Token, q.Correct)
	w = apiCall(store, handleAPIQuizAnswer, http.MethodPost, "/api/quiz/answer", chatID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("answer code = %d (%s)", w.Code, w.Body.String())
	}
	var res struct {
		Correct bool `json:"correct"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.Correct {
		t.Errorf("answer result = %+v (err %v), want correct=true", res, err)
	}
	if answered, correct, _ := store.QuizStats(chatID); answered != 1 || correct != 1 {
		t.Errorf("QuizStats = %d/%d, want 1/1", correct, answered)
	}

	// A tampered token (e.g. claiming a different correct index) is rejected.
	tampered := fmt.Sprintf(`{"word":%q,"correct":%d,"exp":%d,"token":%q,"answer":0}`,
		q.Word, (q.Correct+1)%quizOptionCount, q.Exp, q.Token)
	if w := apiCall(store, handleAPIQuizAnswer, http.MethodPost, "/api/quiz/answer", chatID, tampered); w.Code != http.StatusBadRequest {
		t.Errorf("tampered token code = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Grammar lessons
// ---------------------------------------------------------------------------

func TestGrammarLessonsValid(t *testing.T) {
	lessons := loadGrammarLessons()
	if len(lessons) < 10 {
		t.Fatalf("expected a full curriculum, got %d lessons", len(lessons))
	}
	seen := map[string]bool{}
	for i, l := range lessons {
		if l.ID == "" || l.Title == "" || l.Pattern == "" || l.Explanation == "" {
			t.Errorf("lesson %d missing core fields: %+v", i, l)
		}
		if len(l.Examples) == 0 {
			t.Errorf("lesson %q has no examples", l.ID)
		}
		if seen[l.ID] {
			t.Errorf("duplicate lesson id %q", l.ID)
		}
		seen[l.ID] = true
		if i > 0 && l.Order < lessons[i-1].Order {
			t.Errorf("lessons not ordered easy→hard at %q", l.ID)
		}
		for _, p := range l.Practice {
			if p.Answer < 0 || p.Answer >= len(p.Options) {
				t.Errorf("lesson %q practice answer index %d out of range", l.ID, p.Answer)
			}
		}
	}
}

func TestAPIGrammar(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	w := apiCall(store, handleAPIGrammar, http.MethodGet, "/api/grammar", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list code = %d", w.Code)
	}
	var list struct {
		Lessons []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Lessons) == 0 {
		t.Fatal("grammar list is empty")
	}

	// Fetch the first lesson in full.
	id := list.Lessons[0].ID
	lw := apiCall(store, handleAPIGrammarLesson, http.MethodGet, "/api/grammar/lesson?id="+id, 100, "")
	if lw.Code != http.StatusOK {
		t.Fatalf("lesson code = %d", lw.Code)
	}
	var l GrammarLesson
	if err := json.Unmarshal(lw.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if l.ID != id || l.Pattern == "" {
		t.Errorf("lesson payload = %+v", l)
	}

	// Unknown id → 404.
	if w := apiCall(store, handleAPIGrammarLesson, http.MethodGet, "/api/grammar/lesson?id=nope", 100, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown lesson code = %d, want 404", w.Code)
	}
}

// TestAPIReviewSummary verifies the level-suggestion endpoint reads the rolling
// review-performance window: a sustained high-accuracy run nudges the user up,
// then the cooldown suppresses an immediate repeat.
func TestAPIReviewSummary(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 100

	// Before any reviews, nothing to suggest.
	w := apiCall(store, handleAPIReviewSummary, http.MethodPost, "/api/review/summary", chatID, "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("summary code = %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["suggest"] != false {
		t.Fatalf("empty window: suggest = %v, want false", resp["suggest"])
	}

	// Accumulate a sustained run of correct reviews at the default level.
	for i := 0; i < reviewPerfMinSample; i++ {
		if err := store.RecordReviewOutcome(chatID, true); err != nil {
			t.Fatal(err)
		}
	}
	w = apiCall(store, handleAPIReviewSummary, http.MethodPost, "/api/review/summary", chatID, "{}")
	resp = map[string]interface{}{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["suggest"] != true || resp["direction"] != "harder" {
		t.Fatalf("sustained high: resp = %+v, want suggest=true direction=harder", resp)
	}
	if resp["targetLevel"] != config.LevelUpperInt {
		t.Errorf("targetLevel = %v, want %q", resp["targetLevel"], config.LevelUpperInt)
	}

	// A second call right away is suppressed by the cooldown + window reset.
	w = apiCall(store, handleAPIReviewSummary, http.MethodPost, "/api/review/summary", chatID, "{}")
	resp = map[string]interface{}{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["suggest"] != false {
		t.Errorf("within cooldown: suggest = %v, want false", resp["suggest"])
	}
}

// TestPracticeSeedsReview verifies self-paced learning works without any push:
// an on-demand /api/practice word enrols the term for spaced review.
func TestPracticeSeedsReview(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = 100

	if err := store.AddToPool(config.KindWord, config.DefaultLevel, "ephemeral", "fleeting", "card: ephemeral"); err != nil {
		t.Fatal(err)
	}
	w := apiCall(store, handleAPIPractice, http.MethodGet, "/api/practice?kind=word", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("practice code = %d", w.Code)
	}
	var resp struct {
		Available bool   `json:"available"`
		Term      string `json:"term"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Available || resp.Term != "ephemeral" {
		t.Fatalf("practice payload = %+v, want available ephemeral", resp)
	}
	// The practised word must now be enrolled for spaced review.
	if _, _, _, ok, err := store.getReview(chatID, "ephemeral"); err != nil || !ok {
		t.Fatalf("practised word not scheduled for review: ok=%v err=%v", ok, err)
	}
}

// TestPublicIDRoundTrip verifies the opaque public id is deterministic, stored,
// reversible to the chat_id server-side, and never equal to the chat_id.
func TestPublicIDRoundTrip(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const chatID = int64(4242)

	pid := store.PublicID(chatID)
	if pid == "" {
		t.Fatal("PublicID returned empty")
	}
	if pid == strconv.FormatInt(chatID, 10) {
		t.Fatal("public id must not be the chat_id")
	}
	if again := store.PublicID(chatID); again != pid {
		t.Errorf("PublicID not deterministic: %q vs %q", pid, again)
	}
	got, ok := store.ChatIDByPublicID(pid)
	if !ok || got != chatID {
		t.Errorf("ChatIDByPublicID = %d, %v; want %d, true", got, ok, chatID)
	}
	if _, ok := store.ChatIDByPublicID("deadbeefdeadbeef"); ok {
		t.Error("unknown public id resolved unexpectedly")
	}
}

// TestKudosToggle verifies one-toggle-per-pair semantics and the received count.
func TestKudosToggle(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const a, b = int64(1), int64(2)

	gave, count, err := store.ToggleKudos(a, b)
	if err != nil || !gave || count != 1 {
		t.Fatalf("first toggle: gave=%v count=%d err=%v, want true/1", gave, count, err)
	}
	// A second giver adds to the count.
	if _, count, _ = store.ToggleKudos(3, b); count != 2 {
		t.Fatalf("second giver count = %d, want 2", count)
	}
	// a toggles off.
	gave, count, err = store.ToggleKudos(a, b)
	if err != nil || gave || count != 1 {
		t.Fatalf("toggle off: gave=%v count=%d err=%v, want false/1", gave, count, err)
	}
	if c, mine, _ := store.KudosFor(a, b); c != 1 || mine {
		t.Errorf("KudosFor after toggle-off = %d, gaveByMe=%v; want 1/false", c, mine)
	}
}

// TestAPIProfileComparison checks the head-to-head payload, the better signs,
// the 404 for unknown ids, and that no chat_id leaks into the response.
func TestAPIProfileComparison(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const me, them = int64(100), int64(200)

	// me: 2 words; them: 1 word → I'm ahead on words.
	store.RecordSentVocab(me, "alpha")
	store.RecordSentVocab(me, "beta")
	store.RecordSentVocab(them, "gamma")
	pid := store.PublicID(them)

	w := apiCall(store, handleAPIProfile, http.MethodGet, "/api/profile?id="+pid, me, "")
	if w.Code != http.StatusOK {
		t.Fatalf("profile code = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), strconv.FormatInt(them, 10)) {
		t.Fatalf("profile payload leaked a chat_id: %s", w.Body.String())
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	metrics, _ := raw["metrics"].([]interface{})
	if len(metrics) != 5 {
		t.Fatalf("metrics = %d, want 5", len(metrics))
	}
	words := metrics[0].(map[string]interface{})
	if words["key"] != "words" || words["me"].(float64) != 2 || words["them"].(float64) != 1 || words["better"].(float64) != 1 {
		t.Errorf("words metric = %+v, want me=2 them=1 better=1", words)
	}

	// Unknown id → 404.
	if uw := apiCall(store, handleAPIProfile, http.MethodGet, "/api/profile?id=nope", me, ""); uw.Code != http.StatusNotFound {
		t.Errorf("unknown profile code = %d, want 404", uw.Code)
	}
}

// kudosCall invokes the notifier-wrapped kudos handler with a mock notifier.
func kudosCall(store *Store, notifier telegram.Notifier, chatID int64, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/kudos", strings.NewReader(body))
	r.Header.Set("X-Init-Data", signInitData(chatID, time.Now()))
	w := httptest.NewRecorder()
	withUser(store, func(w http.ResponseWriter, r *http.Request, id int64, st *Store) {
		handleAPIKudos(w, r, id, st, notifier)
	})(w, r)
	return w
}

// TestAPIKudosNotify verifies self-kudos is rejected, a non-paused recipient is
// notified once, and a paused (self-paced) recipient is not.
func TestAPIKudosNotify(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	const me, active, paused = int64(100), int64(200), int64(300)

	mePid := store.PublicID(me)
	activePid := store.PublicID(active)
	pausedPid := store.PublicID(paused)
	if err := store.SetPaused(paused, true); err != nil {
		t.Fatal(err)
	}

	// Self-kudos rejected.
	if w := kudosCall(store, &mockNotifier{}, me, `{"id":"`+mePid+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("self-kudos code = %d, want 400", w.Code)
	}

	// Non-paused recipient → notified once.
	n1 := &mockNotifier{}
	w := kudosCall(store, n1, me, `{"id":"`+activePid+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("kudos code = %d", w.Code)
	}
	if len(n1.sent) != 1 || n1.sent[0].chatID != active {
		t.Errorf("active recipient notifications = %+v, want one to %d", n1.sent, active)
	}

	// Paused recipient → not notified, but kudos still recorded.
	n2 := &mockNotifier{}
	kudosCall(store, n2, me, `{"id":"`+pausedPid+`"}`)
	if len(n2.sent) != 0 {
		t.Errorf("paused recipient should not be notified, got %+v", n2.sent)
	}
	if c, _, _ := store.KudosFor(me, paused); c != 1 {
		t.Errorf("paused recipient kudos count = %d, want 1", c)
	}
}

// TestLeaderboardIDsResolve is a regression test for the "Could not load this
// profile" bug: every leaderboard row's opaque id must resolve back to a chat_id
// (previously PublicID's write was swallowed mid-read so public_id stayed empty).
func TestLeaderboardIDsResolve(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	for _, id := range []int64{11, 22, 33} {
		store.RecordSentVocab(id, "alpha")
		store.RecordSentVocab(id, "beta")
	}
	rows, _, _, err := store.Leaderboard("words", 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no leaderboard rows")
	}
	for _, r := range rows {
		if r.ID == "" {
			t.Fatalf("row %q has empty id", r.Name)
		}
		if _, ok := store.ChatIDByPublicID(r.ID); !ok {
			t.Errorf("row id %q did not resolve back to a chat_id", r.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Achievements + Analytics API
// ---------------------------------------------------------------------------

func TestAPIStatsIncludesAchievements(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	chatID := int64(5001)

	// Seed some data so achievements unlock.
	for i := 0; i < 12; i++ {
		store.RecordSentVocab(chatID, "w"+string(rune('a'+i)))
	}

	w := apiCall(store, handleAPIStats, http.MethodGet, "/api/stats", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats code = %d, want 200", w.Code)
	}

	var resp struct {
		Achievements []Achievement `json:"achievements"`
		AchUnlocked  int           `json:"ach_unlocked"`
		AchTotal     int           `json:"ach_total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Achievements) == 0 {
		t.Fatal("expected achievements in stats response")
	}
	if resp.AchTotal != len(resp.Achievements) {
		t.Errorf("ach_total=%d but len(achievements)=%d", resp.AchTotal, len(resp.Achievements))
	}
	if resp.AchUnlocked < 1 {
		t.Error("expected at least 1 unlocked achievement (first_steps)")
	}

	// Verify first_steps is unlocked.
	for _, a := range resp.Achievements {
		if a.ID == "first_steps" && !a.Unlocked {
			t.Error("first_steps should be unlocked with 12 words")
		}
	}
}

func TestAPIAnalytics(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)
	chatID := int64(5002)

	// Seed some data.
	store.RecordSentVocab(chatID, "apple")
	store.RecordSentVocab(chatID, "banana")
	_, _ = store.db.Exec("INSERT INTO quiz_results (chat_id, word, correct) VALUES (?, 'apple', 1)", chatID)

	w := apiCall(store, handleAPIAnalytics, http.MethodGet, "/api/analytics", chatID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("analytics code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var resp LearningAnalytics
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if len(resp.ContentDiversity) < 1 {
		t.Error("expected content diversity data")
	}
	if len(resp.ActivityByHour) != 24 {
		t.Errorf("expected 24 hour buckets, got %d", len(resp.ActivityByHour))
	}
}

func TestAPIAnalyticsRequiresAuth(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	r := httptest.NewRequest(http.MethodGet, "/api/analytics", nil)
	w := httptest.NewRecorder()
	withUser(store, handleAPIAnalytics)(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("analytics without auth: code = %d, want 401", w.Code)
	}
}
