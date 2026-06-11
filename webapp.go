package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webappFiles holds the embedded Mini App frontend (HTML/CSS/JS and, in later
// phases, bundled deck JSON). Served at the web root by startWebServer.
//
//go:embed webapp
var webappFiles embed.FS

// initDataTTL bounds how old a signed Telegram initData payload may be before we
// reject it. Telegram initData is signed once when the Mini App opens; rejecting
// stale payloads limits replay if one leaks.
const initDataTTL = 24 * time.Hour

// startWebServer starts the embedded HTTP server that serves the Telegram Mini
// App. It is only called when WEB_APP_URL is configured.
func startWebServer(store *Store) {
	sub, err := fs.Sub(webappFiles, "webapp")
	if err != nil {
		log.Printf("⚠️  [WEBAPP] Could not open embedded assets: %v", err)
		return
	}
	assets := http.FileServer(http.FS(sub))

	mux := http.NewServeMux()
	// JSON API — every handler is wrapped with withUser for initData auth.
	mux.HandleFunc("/api/stats", withUser(store, handleAPIStats))
	mux.HandleFunc("/api/vocab", withUser(store, handleAPIVocab))
	mux.HandleFunc("/api/bookmark", withUser(store, handleAPIBookmark))
	mux.HandleFunc("/api/leaderboard", withUser(store, handleAPILeaderboard))
	mux.HandleFunc("/api/leaderboard/name", withUser(store, handleAPILeaderboardName))
	mux.HandleFunc("/api/review/next", withUser(store, handleAPIReviewNext))
	mux.HandleFunc("/api/review/answer", withUser(store, handleAPIReviewAnswer))
	mux.HandleFunc("/api/decks", withUser(store, handleAPIDecks))
	mux.HandleFunc("/api/decks/study", withUser(store, handleAPIDeckStudy))
	mux.HandleFunc("/api/decks/swipe", withUser(store, handleAPIDeckSwipe))
	mux.HandleFunc("/api/settings", withUser(store, handleAPISettings))
	mux.HandleFunc("/api/content", withUser(store, handleAPIContent))
	mux.HandleFunc("/api/quizzes", withUser(store, handleAPIQuizzes))
	mux.HandleFunc("/api/vocab/card", withUser(store, handleAPIVocabCard))
	mux.HandleFunc("/api/quiz/next", withUser(store, handleAPIQuizNext))
	mux.HandleFunc("/api/quiz/answer", withUser(store, handleAPIQuizAnswer))
	mux.HandleFunc("/api/practice", withUser(store, handleAPIPractice))
	// Frontend: the SPA shell on the app routes, static files otherwise.
	// Embedded files carry no modtime, so without an explicit Cache-Control
	// webviews cache them heuristically and users keep running a stale SPA
	// after a deploy. no-cache forces a revalidation on every open (the whole
	// frontend is ~50 KB, so the cost is negligible).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		switch r.URL.Path {
		case "/", "/stats", "/app":
			serveIndex(w)
		default:
			assets.ServeHTTP(w, r) // app.js, styles.css, decks/*.json …
		}
	})

	log.Printf("🌐 [WEBAPP] Starting web server on :%s", webAppPort)
	go func() {
		if err := http.ListenAndServe(":"+webAppPort, mux); err != nil {
			log.Printf("⚠️  [WEBAPP] Web server error: %v", err)
		}
	}()
}

// serveIndex writes the Mini App HTML shell.
func serveIndex(w http.ResponseWriter) {
	page, err := webappFiles.ReadFile("webapp/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// apiHandler is an authenticated JSON handler: the caller's chat ID has already
// been validated from the Telegram initData.
type apiHandler func(w http.ResponseWriter, r *http.Request, chatID int64, store *Store)

// withUser validates the Telegram initData (from the X-Init-Data header, or the
// initData query param as a fallback) and invokes fn with the resolved chat ID.
// It replies 401 when validation fails.
func withUser(store *Store, fn apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.Header.Get("X-Init-Data")
		if initData == "" {
			initData = r.URL.Query().Get("initData")
		}
		chatID, photoURL, ok := validateInitData(initData)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Best-effort avatar cache for the leaderboard (no-op when unchanged).
		if photoURL != "" {
			_ = store.SetPhotoURL(chatID, photoURL)
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fn(w, r, chatID, store)
	}
}

// writeJSON encodes v as a JSON response.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// validateInitData validates Telegram WebApp initData using HMAC-SHA256 and
// returns the user ID (and their avatar URL, when Telegram includes one) on
// success. It also rejects initData whose auth_date is older than initDataTTL
// (replay protection).
func validateInitData(initData string) (userID int64, photoURL string, ok bool) {
	params, err := url.ParseQuery(initData)
	if err != nil {
		return 0, "", false
	}
	hash := params.Get("hash")
	if hash == "" {
		return 0, "", false
	}

	// Build check string: all fields except "hash", sorted alphabetically.
	var pairs []string
	for k, vs := range params {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+vs[0])
	}
	sort.Strings(pairs)
	checkString := strings.Join(pairs, "\n")

	// secret_key = HMAC-SHA256("WebAppData", bot_token)
	h1 := hmac.New(sha256.New, []byte("WebAppData"))
	h1.Write([]byte(TelegramBotToken))
	secretKey := h1.Sum(nil)

	// expected = HMAC-SHA256(secret_key, check_string)
	h2 := hmac.New(sha256.New, secretKey)
	h2.Write([]byte(checkString))
	expected := hex.EncodeToString(h2.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(hash)) {
		return 0, "", false
	}

	// Reject stale payloads (replay protection). auth_date is unix seconds.
	if ad := params.Get("auth_date"); ad != "" {
		if sec, err := strconv.ParseInt(ad, 10, 64); err == nil {
			if time.Since(time.Unix(sec, 0)) > initDataTTL {
				return 0, "", false
			}
		}
	}

	userJSON := params.Get("user")
	if userJSON == "" {
		return 0, "", false
	}
	var u struct {
		ID       int64  `json:"id"`
		PhotoURL string `json:"photo_url"`
	}
	if err := json.Unmarshal([]byte(userJSON), &u); err != nil || u.ID == 0 {
		return 0, "", false
	}
	return u.ID, u.PhotoURL, true
}

// ---------------------------------------------------------------------------
// API handlers
// ---------------------------------------------------------------------------

// handleAPIStats returns the progress dashboard payload for the current user.
func handleAPIStats(w http.ResponseWriter, _ *http.Request, chatID int64, store *Store) {
	st, err := store.UserStats(chatID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	quizPct := 0
	if st.QuizAnswered > 0 {
		quizPct = st.QuizCorrect * 100 / st.QuizAnswered
	}
	memberSince := ""
	if st.HasMemberSince {
		memberSince = st.MemberSince.Format("2 Jan 2006")
	}

	writeJSON(w, map[string]interface{}{
		"current_streak":  st.CurrentStreak,
		"longest_streak":  st.LongestStreak,
		"words":           st.Words,
		"mastered":        st.Mastered,
		"verbs":           st.Verbs,
		"quiz_answered":   st.QuizAnswered,
		"quiz_correct":    st.QuizCorrect,
		"quiz_pct":        quizPct,
		"active_days":     st.ActiveDays,
		"activity_days":   st.ActivityDays,
		"activity_counts": st.ActivityCounts,
		"level":           levelLabel(st.Level),
		"paused":          st.Paused,
		"member_since":    memberSince,
	})
}

// handleAPIVocab returns a page of the user's learned words, with optional
// bookmark-only filter and a text search over term/meaning.
func handleAPIVocab(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	q := r.URL.Query()
	offset := atoiOr(q.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	limit := atoiOr(q.Get("limit"), 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}
	bookmarksOnly := q.Get("bookmarks") == "1"
	search := strings.TrimSpace(q.Get("q"))

	items, err := store.LearnedWordsFiltered(chatID, offset, limit, bookmarksOnly, search)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []LearnedWord{}
	}
	writeJSON(w, map[string]interface{}{
		"items":  items,
		"total":  store.LearnedWordsCountFiltered(chatID, bookmarksOnly, search),
		"offset": offset,
	})
}

// handleAPIBookmark toggles a bookmark for the current user.
func handleAPIBookmark(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Term string `json:"term"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Term) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var err error
	if body.On {
		err = store.AddBookmark(chatID, body.Term)
	} else {
		err = store.RemoveBookmark(chatID, body.Term)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "bookmarked": body.On})
}

// handleAPILeaderboard returns the ranked board for a metric plus the caller's
// own rank, and whether they've chosen a display name yet.
func handleAPILeaderboard(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	metric := r.URL.Query().Get("metric")
	if metric != "mastered" && metric != "weekly" {
		metric = "words"
	}
	rows, myRank, myValue, err := store.Leaderboard(metric, chatID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []LeaderRow{}
	}
	writeJSON(w, map[string]interface{}{
		"metric": metric,
		"rows":   rows,
		"me": map[string]interface{}{
			"rank":    myRank,
			"value":   myValue,
			"hasName": store.GetDisplayName(chatID) != "",
		},
	})
}

// handleAPILeaderboardName stores the caller's chosen leaderboard display name.
func handleAPILeaderboardName(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := sanitizeDisplayName(body.Name)
	if name == "" {
		http.Error(w, "empty name", http.StatusBadRequest)
		return
	}
	if err := store.SetDisplayName(chatID, name); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "name": name})
}

// handleAPIReviewNext returns the user's due spaced-repetition cards.
func handleAPIReviewNext(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	limit := atoiOr(r.URL.Query().Get("limit"), 20)
	if limit < 1 || limit > 50 {
		limit = 20
	}
	due, err := store.DueReviews(chatID, time.Now(), limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]interface{}, 0, len(due))
	for _, d := range due {
		items = append(items, map[string]interface{}{"term": d.term, "meaning": d.meaning})
	}
	writeJSON(w, map[string]interface{}{"items": items})
}

// handleAPIReviewAnswer applies a Knew-it / Forgot answer to a due word.
func handleAPIReviewAnswer(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Term  string `json:"term"`
		Known bool   `json:"known"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Term) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var err error
	if body.Known {
		_, err = store.ApplyReviewKnown(chatID, body.Term, time.Now())
	} else {
		_, err = store.ApplyReviewForgot(chatID, body.Term, time.Now())
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleAPIDecks lists the curated decks with the user's progress.
func handleAPIDecks(w http.ResponseWriter, _ *http.Request, chatID int64, store *Store) {
	decks, err := store.Decks(chatID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if decks == nil {
		decks = []DeckProgress{}
	}
	writeJSON(w, map[string]interface{}{"decks": decks})
}

// handleAPIDeckStudy returns the next cards to study in a deck.
func handleAPIDeckStudy(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	deckID := strings.TrimSpace(r.URL.Query().Get("deck"))
	if deckID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	limit := atoiOr(r.URL.Query().Get("limit"), 20)
	if limit < 1 || limit > 50 {
		limit = 20
	}
	cards, err := store.DeckStudy(chatID, deckID, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if cards == nil {
		cards = []DeckStudyCard{}
	}
	writeJSON(w, map[string]interface{}{"items": cards})
}

// handleAPIDeckSwipe records a Leitner answer for a deck card.
func handleAPIDeckSwipe(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Deck  string `json:"deck"`
		Term  string `json:"term"`
		Known bool   `json:"known"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Deck) == "" || strings.TrimSpace(body.Term) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := store.DeckSwipe(chatID, body.Deck, body.Term, body.Known, time.Now()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// settingToggles maps an API toggle key to its Store setter.
func settingSetters(store *Store) map[string]func(int64, bool) error {
	return map[string]func(int64, bool) error{
		"tts":          store.SetTTSEnabled,
		"tips":         store.SetTipsEnabled,
		"quiz":         store.SetQuizEnabled,
		"idiom":        store.SetIdiomEnabled,
		"collocation":  store.SetCollocationEnabled,
		"story":        store.SetStoryEnabled,
		"review":       store.SetReviewEnabled,
		"daily_review": store.SetDailyReviewEnabled,
		"digest":       store.SetDigestEnabled,
	}
}

// handleAPISettings serves the user's settings (GET) and applies a single-field
// update (POST {key, value}). Writes reuse the existing per-pref Store setters.
func handleAPISettings(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method == http.MethodPost {
		updateAPISetting(w, r, chatID, store)
		return
	}
	p, err := store.GetPrefs(chatID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	levelLabels := make(map[string]string, len(allLevels))
	for _, l := range allLevels {
		levelLabels[l] = levelLabel(l)
	}
	writeJSON(w, map[string]interface{}{
		"level":       p.Level,
		"levels":      allLevels,
		"levelLabels": levelLabels,
		"name":        store.GetDisplayName(chatID),
		"paused":      p.Paused,
		"interval":    p.Interval,
		"toggles": map[string]bool{
			"tts":          p.TTSEnabled,
			"tips":         p.TipsEnabled,
			"quiz":         p.QuizEnabled,
			"idiom":        p.IdiomEnabled,
			"collocation":  p.CollocationEnabled,
			"story":        p.StoryEnabled,
			"review":       p.ReviewEnabled,
			"daily_review": p.DailyReviewEnabled,
			"digest":       p.DigestEnabled,
		},
	})
}

// updateAPISetting applies one {key, value} change.
func updateAPISetting(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	var body struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var err error
	switch body.Key {
	case "level":
		var lv string
		if json.Unmarshal(body.Value, &lv) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		norm, ok := normalizeLevel(lv)
		if !ok {
			http.Error(w, "invalid level", http.StatusBadRequest)
			return
		}
		err = store.SetLevel(chatID, norm)
	case "paused":
		var b bool
		if json.Unmarshal(body.Value, &b) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		err = store.SetPaused(chatID, b)
	case "interval":
		var n int
		if json.Unmarshal(body.Value, &n) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if n < 15 {
			n = 15
		} else if n > 1440 {
			n = 1440
		}
		err = store.SetInterval(chatID, n)
	default:
		setter, ok := settingSetters(store)[body.Key]
		if !ok {
			http.Error(w, "unknown setting", http.StatusBadRequest)
			return
		}
		var b bool
		if json.Unmarshal(body.Value, &b) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		err = setter(chatID, b)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// ---------------------------------------------------------------------------
// Library — browse received idioms / collocations / stories / tips and the
// quiz history (Mini App "Library" chips on the Words tab).
// ---------------------------------------------------------------------------

// libraryKinds whitelists the content kinds browsable via /api/content. The
// kind also picks the per-user history table via sentTableFor, so the
// whitelist doubles as SQL-injection protection for the table name.
var libraryKinds = map[string]bool{
	kindIdiom:       true,
	kindCollocation: true,
	kindStory:       true,
	kindTip:         true,
}

// libraryItem is one row of the Library listing.
type libraryItem struct {
	Term    string `json:"term"`
	Meaning string `json:"meaning"`
	Text    string `json:"text"`
	SentAt  string `json:"sent_at"` // "2 Jan 2006", appLocation
}

// stripTelegramHTML removes the simple HTML markup (<b>, <i>, …) that pool
// card texts carry for Telegram, so the Mini App can escape and render them
// as plain text.
func stripTelegramHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ContentHistory returns one page of the content of a kind the user has
// received, most recent first, enriched with the card text from content_pool
// (the pool keeps consumed rows, mirroring LearnedWordsFiltered's join).
func (s *Store) ContentHistory(chatID int64, kind string, offset, limit int) ([]libraryItem, error) {
	table := sentTableFor(kind)
	rows, err := s.db.Query(`
		SELECT st.word, COALESCE(MAX(cp.meaning), ''), COALESCE(MAX(cp.text), ''), MAX(st.sent_at)
		FROM `+table+` st
		LEFT JOIN content_pool cp ON cp.kind = ? AND cp.term = st.word
		WHERE st.chat_id = ?
		GROUP BY st.word
		ORDER BY MAX(st.sent_at) DESC
		LIMIT ? OFFSET ?`,
		kind, chatID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []libraryItem
	for rows.Next() {
		var (
			it    libraryItem
			rawAt any
		)
		if err := rows.Scan(&it.Term, &it.Meaning, &it.Text, &rawAt); err != nil {
			return nil, err
		}
		it.Text = stripTelegramHTML(it.Text)
		if ts, ok := parseStoredUTC(rawAt); ok {
			it.SentAt = ts.In(appLocation).Format("2 Jan 2006")
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ContentHistoryCount returns how many distinct items of a kind the user has
// received.
func (s *Store) ContentHistoryCount(chatID int64, kind string) int {
	var n int
	_ = s.db.QueryRow(
		"SELECT COUNT(DISTINCT word) FROM "+sentTableFor(kind)+" WHERE chat_id = ?", chatID,
	).Scan(&n)
	return n
}

// quizHistoryItem is one past quiz attempt.
type quizHistoryItem struct {
	Word       string `json:"word"`
	Correct    bool   `json:"correct"`
	AnsweredAt string `json:"answered_at"` // "2 Jan 2006", appLocation
}

// QuizHistory returns one page of the user's quiz attempts, most recent first.
func (s *Store) QuizHistory(chatID int64, offset, limit int) ([]quizHistoryItem, error) {
	rows, err := s.db.Query(`
		SELECT word, correct, answered_at FROM quiz_results
		WHERE chat_id = ? ORDER BY answered_at DESC, id DESC LIMIT ? OFFSET ?`,
		chatID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []quizHistoryItem
	for rows.Next() {
		var (
			it      quizHistoryItem
			correct int
			rawAt   any
		)
		if err := rows.Scan(&it.Word, &correct, &rawAt); err != nil {
			return nil, err
		}
		it.Correct = correct == 1
		if ts, ok := parseStoredUTC(rawAt); ok {
			it.AnsweredAt = ts.In(appLocation).Format("2 Jan 2006")
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// handleAPIContent returns a page of the user's received idioms /
// collocations / stories / tips for the Library.
func handleAPIContent(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if !libraryKinds[kind] {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	offset := atoiOr(q.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	limit := atoiOr(q.Get("limit"), 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items, err := store.ContentHistory(chatID, kind, offset, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []libraryItem{}
	}
	writeJSON(w, map[string]interface{}{
		"items":  items,
		"total":  store.ContentHistoryCount(chatID, kind),
		"offset": offset,
	})
}

// handleAPIQuizzes returns a page of the user's quiz attempt history.
func handleAPIQuizzes(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	q := r.URL.Query()
	offset := atoiOr(q.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	limit := atoiOr(q.Get("limit"), 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items, err := store.QuizHistory(chatID, offset, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []quizHistoryItem{}
	}
	answered, _, _ := store.QuizStats(chatID)
	writeJSON(w, map[string]interface{}{
		"items":  items,
		"total":  answered,
		"offset": offset,
	})
}

// handleAPIVocabCard returns the full (HTML-stripped) pooled card text for one
// learned word, so Library word rows can expand into a detail view.
func handleAPIVocabCard(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"term":    term,
		"text":    stripTelegramHTML(store.PooledCardText(term)),
		"meaning": store.MeaningForWord(term),
	})
}

// ---------------------------------------------------------------------------
// On-demand practice — in-app quiz + fresh pool content (roadmap phase 5).
// ---------------------------------------------------------------------------

// practiceKinds whitelists what /api/practice may serve. Pool-only (never
// generates inline with AI), so a tap can't trigger provider spend.
var practiceKinds = map[string]bool{
	kindWord:        true,
	kindIdiom:       true,
	kindCollocation: true,
}

// practiceHourlyLimit bounds /api/practice and /api/quiz/next taps per user per
// rolling hour, echoing the bot's hourly-limiter approach for on-demand content.
const practiceHourlyLimit = 60

var (
	practiceMu   sync.Mutex
	practiceHits = map[int64][]time.Time{}
)

// practiceAllowed records a hit for chatID and reports whether they are still
// under the rolling-hour budget.
func practiceAllowed(chatID int64) bool {
	practiceMu.Lock()
	defer practiceMu.Unlock()
	cutoff := time.Now().Add(-time.Hour)
	kept := practiceHits[chatID][:0]
	for _, t := range practiceHits[chatID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= practiceHourlyLimit {
		practiceHits[chatID] = kept
		return false
	}
	practiceHits[chatID] = append(kept, time.Now())
	return true
}

// PracticeContent serves one pool item of a kind at the user's level and
// records it to the per-user history (so SRS/daily recaps treat it like a
// scheduled send). It mirrors serveContent's pool path but never generates
// inline — the Mini App must not trigger AI spend.
func (s *Store) PracticeContent(chatID int64, kind, level string) (term, text string, err error) {
	term, _, text, ok, err := s.PooledUnseen(kind, level, chatID)
	if err != nil {
		return "", "", err
	}
	if !ok {
		term, _, text, ok, err = s.PooledRecycled(kind, level, chatID)
		if err != nil {
			return "", "", err
		}
	}
	if !ok {
		term, _, text, ok, err = s.PooledOldest(kind, level)
		if err != nil {
			return "", "", err
		}
	}
	if !ok {
		return "", "", errPoolEmpty
	}
	if err := s.recordSentFor(kind, chatID, term); err != nil {
		log.Printf("⚠️  [WEBAPP] Could not record practice %s %q for chat %d: %v", kind, term, chatID, err)
	}
	return term, text, nil
}

var errPoolEmpty = errors.New("content pool empty")

// handleAPIPractice serves one fresh card (word/idiom/collocation) from the
// content pool at the user's level.
func handleAPIPractice(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	kind := r.URL.Query().Get("kind")
	if !practiceKinds[kind] {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !practiceAllowed(chatID) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	term, text, err := store.PracticeContent(chatID, kind, store.GetLevel(chatID))
	if err != nil {
		if errors.Is(err, errPoolEmpty) {
			writeJSON(w, map[string]interface{}{"available": false})
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"available": true,
		"kind":      kind,
		"term":      term,
		"text":      stripTelegramHTML(text),
	})
}

// quizTokenTTL bounds how long an issued in-app quiz question stays answerable.
const quizTokenTTL = 10 * time.Minute

// quizTokenMAC signs the quiz payload with a key derived from the bot token,
// binding the answer to the user, subject word, correct index and expiry —
// the server stays stateless between /api/quiz/next and /api/quiz/answer.
func quizTokenMAC(chatID int64, word string, correctIdx int, exp int64) string {
	h1 := hmac.New(sha256.New, []byte("QuizToken"))
	h1.Write([]byte(TelegramBotToken))
	key := h1.Sum(nil)
	h2 := hmac.New(sha256.New, key)
	fmt.Fprintf(h2, "%d|%s|%d|%d", chatID, word, correctIdx, exp)
	return hex.EncodeToString(h2.Sum(nil))
}

// handleAPIQuizNext returns one multiple-choice question built from the user's
// learned words (same generator the chat quizzes use).
func handleAPIQuizNext(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if !practiceAllowed(chatID) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	q, ok, err := makeQuiz(store, chatID, time.Now(), newRand())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, map[string]interface{}{"available": false})
		return
	}
	exp := time.Now().Add(quizTokenTTL).Unix()
	writeJSON(w, map[string]interface{}{
		"available": true,
		"prompt":    stripTelegramHTML(q.prompt),
		"options":   q.options,
		"word":      q.word,
		"correct":   q.correctIdx,
		"exp":       exp,
		"token":     quizTokenMAC(chatID, q.word, q.correctIdx, exp),
	})
}

// handleAPIQuizAnswer validates a signed quiz token and records the result.
func handleAPIQuizAnswer(w http.ResponseWriter, r *http.Request, chatID int64, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Word    string `json:"word"`
		Correct int    `json:"correct"`
		Exp     int64  `json:"exp"`
		Token   string `json:"token"`
		Answer  int    `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Word == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	expected := quizTokenMAC(chatID, body.Word, body.Correct, body.Exp)
	if !hmac.Equal([]byte(expected), []byte(body.Token)) || time.Now().Unix() > body.Exp {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	correct := body.Answer == body.Correct
	if err := store.RecordQuizResult(chatID, body.Word, correct); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "correct": correct})
}

// atoiOr parses s as an int, returning def on failure.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
