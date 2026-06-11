package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
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
	// Frontend: the SPA shell on the app routes, static files otherwise.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
		chatID, ok := validateInitData(initData)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
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
// returns the user ID on success. It also rejects initData whose auth_date is
// older than initDataTTL (replay protection).
func validateInitData(initData string) (userID int64, ok bool) {
	params, err := url.ParseQuery(initData)
	if err != nil {
		return 0, false
	}
	hash := params.Get("hash")
	if hash == "" {
		return 0, false
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
		return 0, false
	}

	// Reject stale payloads (replay protection). auth_date is unix seconds.
	if ad := params.Get("auth_date"); ad != "" {
		if sec, err := strconv.ParseInt(ad, 10, 64); err == nil {
			if time.Since(time.Unix(sec, 0)) > initDataTTL {
				return 0, false
			}
		}
	}

	userJSON := params.Get("user")
	if userJSON == "" {
		return 0, false
	}
	var u struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(userJSON), &u); err != nil || u.ID == 0 {
		return 0, false
	}
	return u.ID, true
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
		"current_streak": st.CurrentStreak,
		"longest_streak": st.LongestStreak,
		"words":          st.Words,
		"mastered":       st.Mastered,
		"verbs":          st.Verbs,
		"quiz_answered":  st.QuizAnswered,
		"quiz_correct":   st.QuizCorrect,
		"quiz_pct":       quizPct,
		"active_days":    st.ActiveDays,
		"activity_days":  st.ActivityDays,
		"level":          levelLabel(st.Level),
		"paused":         st.Paused,
		"member_since":   memberSince,
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
	if metric != "mastered" {
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

// atoiOr parses s as an int, returning def on failure.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
