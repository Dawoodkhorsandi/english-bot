package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// Provider names recorded in the identities table. A single canonical account
// (identified by account_id — the same value used as chat_id across every data
// table) may have one identity per provider, all linkable to the same account.
const (
	providerEmail    = "email"
	providerTelegram = "telegram"
	providerGoogle   = "google" // reserved for a future Google login
)

// Login-code policy. The rate limit is deliberately generous (raised from 3) so
// active users — and retries during account linking — don't trip the limiter
// during a normal sign-in.
const (
	loginCodeRateLimit  = 10
	loginCodeRateWindow = 10 * time.Minute
	loginCodeTTL        = 5 * time.Minute
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// Failed-attempt brute-force guard for the email endpoints. Only *failures* are
// counted, so legitimate users (who succeed) never accumulate toward the limit;
// a flood of bad guesses from one IP is throttled.
const (
	authFailLimit  = 15
	authFailWindow = time.Minute
)

var (
	authFailMu   sync.Mutex
	authFailHits = map[string][]time.Time{}
)

// authBlocked reports whether ip has exceeded the failed-attempt budget within
// the rolling window (pruning expired hits). It does not record a hit.
func authBlocked(ip string) bool {
	authFailMu.Lock()
	defer authFailMu.Unlock()
	cutoff := time.Now().Add(-authFailWindow)
	kept := authFailHits[ip][:0]
	for _, t := range authFailHits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	authFailHits[ip] = kept
	return len(kept) >= authFailLimit
}

// recordAuthFailure logs one failed auth attempt for ip.
func recordAuthFailure(ip string) {
	authFailMu.Lock()
	defer authFailMu.Unlock()
	authFailHits[ip] = append(authFailHits[ip], time.Now())
}

// clientIP resolves the caller's IP, preferring the first X-Forwarded-For hop
// (the bot runs behind nginx) and falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hashPassword(password string) string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash)
}

func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// jwtSecretKey returns the HMAC signing key. config.ValidateSecrets (run at
// startup) guarantees JWTSecret is set to a strong, non-default value, so there
// is no insecure in-code fallback here.
func jwtSecretKey() []byte {
	return []byte(config.JWTSecret)
}

// generateJWT issues a 24h HS256 token whose subject is the canonical account id
// — the same value used as chat_id across every data table (a real Telegram chat
// id when the account has one, otherwise a synthetic negative id).
func generateJWT(accountID int64, email string) string {
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	nonceBytes := make([]byte, 8)
	_, _ = rand.Read(nonceBytes)

	payload := map[string]interface{}{
		"sub": accountID,
		"em":  email,
		"iat": now.Unix(),
		"exp": exp.Unix(),
		"jti": hex.EncodeToString(nonceBytes),
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	header := `{"alg":"HS256","typ":"JWT"}`
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))

	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, jwtSecretKey())
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig
}

// validateJWT verifies the HMAC signature and expiry and returns the account id
// and email claim. The algorithm is fixed to HS256 (the header is not trusted to
// choose it), so "alg":"none" or RS256-confusion tokens fail signature checks.
func validateJWT(tokenStr string) (accountID int64, email string, ok bool) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return 0, "", false
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, jwtSecretKey())
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return 0, "", false
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, "", false
	}

	var payload struct {
		Sub int64  `json:"sub"`
		Em  string `json:"em"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return 0, "", false
	}

	if payload.Exp < time.Now().Unix() {
		return 0, "", false
	}

	return payload.Sub, payload.Em, true
}

// bearerAccount extracts and validates the Bearer JWT from the request, returning
// the caller's account id. It is the shared front door for the JWT-authenticated
// endpoints (refresh, me, link).
func bearerAccount(r *http.Request) (accountID int64, email string, ok bool) {
	authHeader := r.Header.Get("Authorization")
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenStr == "" || tokenStr == authHeader {
		return 0, "", false
	}
	return validateJWT(tokenStr)
}

// ---------------------------------------------------------------------------
// Identity store helpers
// ---------------------------------------------------------------------------

// identityAccount returns the account id, stored credential and name for a
// provider/uid pair, and whether it exists.
func (s *Store) identityAccount(provider, uid string) (accountID int64, credential, name string, ok bool, err error) {
	err = s.db.QueryRow(
		"SELECT account_id, credential, name FROM identities WHERE provider = ? AND provider_uid = ?",
		provider, uid,
	).Scan(&accountID, &credential, &name)
	if err == sql.ErrNoRows {
		return 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", "", false, err
	}
	return accountID, credential, name, true, nil
}

// nextSyntheticAccountID allocates an unused negative account id for an account
// with no Telegram chat. Real Telegram user chat ids are positive, so negatives
// never collide. Must run inside the same transaction as the insert that uses it.
func nextSyntheticAccountID(tx *sql.Tx) (int64, error) {
	var min sql.NullInt64
	if err := tx.QueryRow("SELECT MIN(account_id) FROM identities").Scan(&min); err != nil {
		return 0, err
	}
	if !min.Valid || min.Int64 >= 0 {
		return -1, nil
	}
	return min.Int64 - 1, nil
}

// createEmailAccount registers a new email identity on a fresh synthetic account
// and returns the allocated account id.
func (s *Store) createEmailAccount(email, passwordHash, name string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	accountID, err := nextSyntheticAccountID(tx)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		"INSERT INTO identities (provider, provider_uid, account_id, credential, name) VALUES (?, ?, ?, ?, ?)",
		providerEmail, email, accountID, passwordHash, name,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return accountID, nil
}

// ensureTelegramIdentity records the Telegram identity for a chat (account id ==
// chat id) when it isn't already present. Idempotent.
func (s *Store) ensureTelegramIdentity(chatID int64, name string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO identities (provider, provider_uid, account_id, name) VALUES (?, ?, ?, ?)",
		providerTelegram, strconv.FormatInt(chatID, 10), chatID, name,
	)
	return err
}

// accountInfo is the resolved profile for an account, assembled from its
// identities for /api/auth/me.
type accountInfo struct {
	email      string
	name       string
	createdAt  string
	telegramID sql.NullInt64
}

// accountInfo gathers the display profile for an account across its identities.
func (s *Store) accountProfile(accountID int64) (accountInfo, error) {
	rows, err := s.db.Query(
		"SELECT provider, provider_uid, name, created_at FROM identities WHERE account_id = ? ORDER BY created_at",
		accountID,
	)
	if err != nil {
		return accountInfo{}, err
	}
	defer rows.Close()

	var info accountInfo
	for rows.Next() {
		var provider, uid, name, created string
		if err := rows.Scan(&provider, &uid, &name, &created); err != nil {
			return accountInfo{}, err
		}
		if info.createdAt == "" {
			info.createdAt = created
		}
		if info.name == "" {
			info.name = name
		}
		switch provider {
		case providerEmail:
			info.email = uid
		case providerTelegram:
			if id, perr := strconv.ParseInt(uid, 10, 64); perr == nil {
				info.telegramID = sql.NullInt64{Int64: id, Valid: true}
			}
		}
	}
	return info, rows.Err()
}

// backfillIdentities seeds the identities table from any legacy users rows so
// accounts created before the unified identity model keep working. Idempotent:
// existing identities are left untouched, so email-only users keep their
// first-assigned synthetic id across restarts.
func (s *Store) backfillIdentities() error {
	rows, err := s.db.Query(
		"SELECT COALESCE(email, ''), COALESCE(password_hash, ''), COALESCE(name, ''), telegram_chat_id FROM users",
	)
	if err != nil {
		return err
	}
	type legacyUser struct {
		email, hash, name string
		chatID            sql.NullInt64
	}
	var users []legacyUser
	for rows.Next() {
		var u legacyUser
		if err := rows.Scan(&u.email, &u.hash, &u.name, &u.chatID); err != nil {
			_ = rows.Close()
			return err
		}
		users = append(users, u)
	}
	if cerr := rows.Err(); cerr != nil {
		_ = rows.Close()
		return cerr
	}
	_ = rows.Close()

	for _, u := range users {
		if u.chatID.Valid {
			if err := s.ensureTelegramIdentity(u.chatID.Int64, u.name); err != nil {
				return err
			}
		}
		if u.email == "" {
			continue
		}
		// Skip emails already represented (keeps the backfill idempotent).
		var exists int
		_ = s.db.QueryRow(
			"SELECT COUNT(*) FROM identities WHERE provider = ? AND provider_uid = ?",
			providerEmail, u.email,
		).Scan(&exists)
		if exists > 0 {
			continue
		}
		if u.chatID.Valid {
			// Email already tied to a Telegram chat: link it to that account.
			if _, err := s.db.Exec(
				"INSERT OR IGNORE INTO identities (provider, provider_uid, account_id, credential, name) VALUES (?, ?, ?, ?, ?)",
				providerEmail, u.email, u.chatID.Int64, u.hash, u.name,
			); err != nil {
				return err
			}
		} else if _, err := s.createEmailAccount(u.email, u.hash, u.name); err != nil {
			return err
		}
	}
	return nil
}

// chatKeyedTables lists every (table, column) partitioned by chat_id. mergeAccount
// re-keys all of them from the loser account to the winner.
var chatKeyedTables = [][2]string{
	{"subscribers", "chat_id"},
	{"sent_words", "chat_id"},
	{"sent_vocab", "chat_id"},
	{"sent_idioms", "chat_id"},
	{"sent_tips", "chat_id"},
	{"sent_collocations", "chat_id"},
	{"sent_stories", "chat_id"},
	{"changelog_delivery", "chat_id"},
	{"daily_review_delivery", "chat_id"},
	{"idiom_delivery", "chat_id"},
	{"daily_tip_delivery", "chat_id"},
	{"collocation_delivery", "chat_id"},
	{"story_delivery", "chat_id"},
	{"pool_exhaustion_notice", "chat_id"},
	{"user_prefs", "chat_id"},
	{"kudos", "from_chat_id"},
	{"kudos", "to_chat_id"},
	{"review_schedule", "chat_id"},
	{"activity_log", "chat_id"},
	{"review_perf", "chat_id"},
	{"leitner_progress", "chat_id"},
	{"quiz_results", "chat_id"},
	{"weekly_digest_delivery", "chat_id"},
	{"bookmarks", "chat_id"},
	{"auth_codes", "chat_id"},
}

// mergeAccount folds loser into winner: every chat_id-keyed row and identity is
// re-pointed at winner, then the loser's leftovers are dropped. UPDATE OR IGNORE
// keeps the winner's row on a primary-key clash (winner wins) and the trailing
// DELETE clears the loser's now-duplicate row. Table/column names come from the
// fixed chatKeyedTables allowlist, never user input. Caller guarantees
// loser != winner.
func (s *Store) mergeAccount(loser, winner int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, tc := range chatKeyedTables {
		tbl, col := tc[0], tc[1]
		if _, err := tx.Exec("UPDATE OR IGNORE "+tbl+" SET "+col+" = ? WHERE "+col+" = ?", winner, loser); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM "+tbl+" WHERE "+col+" = ?", loser); err != nil {
			return err
		}
	}
	// Move the loser's identities (email/google) onto the winner; a Telegram
	// identity for the loser cannot exist because the loser is the non-Telegram
	// side of the merge.
	if _, err := tx.Exec("UPDATE OR IGNORE identities SET account_id = ? WHERE account_id = ?", winner, loser); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM identities WHERE account_id = ?", loser); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateLoginCode mints a single-use Telegram login code for chatID, enforcing
// the per-chat rate limit. It is shared by the Mini App endpoint and the /login
// chat command. Returns limited=true when the caller is over the rate limit.
func (s *Store) CreateLoginCode(chatID int64) (code string, limited bool, err error) {
	var recent int
	modifier := fmt.Sprintf("-%d minutes", int(loginCodeRateWindow.Minutes()))
	if err = s.db.QueryRow(
		"SELECT COUNT(*) FROM auth_codes WHERE chat_id = ? AND created_at > datetime('now', ?)",
		chatID, modifier,
	).Scan(&recent); err != nil {
		return "", false, err
	}
	if recent >= loginCodeRateLimit {
		return "", true, nil
	}
	code = generateShortCode()
	if _, err = s.db.Exec("INSERT INTO auth_codes (chat_id, code) VALUES (?, ?)", chatID, code); err != nil {
		return "", false, err
	}
	return code, false, nil
}

// claimLoginCode validates a login code and atomically marks it used. It returns
// the chat id the code was minted for. The mark-used UPDATE is guarded on
// used = 0 and checked via RowsAffected, so concurrent redemptions of the same
// code cannot both succeed (closes the check-then-act race).
func (s *Store) claimLoginCode(code string) (chatID int64, status string) {
	var createdRaw any
	var used int
	err := s.db.QueryRow(
		"SELECT chat_id, created_at, used FROM auth_codes WHERE code = ?",
		code,
	).Scan(&chatID, &createdRaw, &used)
	if err == sql.ErrNoRows {
		return 0, "invalid"
	}
	if err != nil {
		log.Printf("auth: claim query error: %v", err)
		return 0, "error"
	}
	if used != 0 {
		return 0, "used"
	}
	// created_at is a SQLite DATETIME stored in UTC; the driver may hand it back
	// in the process-local timezone, so normalise via parseStoredUTC instead of
	// scanning straight into time.Time (which made every code look expired on a
	// non-UTC server, e.g. Asia/Tehran in production).
	createdAt, ok := parseStoredUTC(createdRaw)
	if !ok {
		log.Printf("auth: claim cannot parse created_at %v (%T)", createdRaw, createdRaw)
		return 0, "error"
	}
	if time.Since(createdAt) > loginCodeTTL {
		return 0, "expired"
	}
	res, err := s.db.Exec("UPDATE auth_codes SET used = 1 WHERE code = ? AND used = 0", code)
	if err != nil {
		log.Printf("auth: claim update error: %v", err)
		return 0, "error"
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Lost the race to another redemption.
		return 0, "used"
	}
	return chatID, "ok"
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func handleAuthRegister(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ip := clientIP(r)
	if authBlocked(ip) {
		http.Error(w, "too many attempts — please wait a minute", http.StatusTooManyRequests)
		return
	}

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Password = strings.TrimSpace(body.Password)
	body.Name = strings.TrimSpace(body.Name)

	if !emailRegex.MatchString(body.Email) {
		recordAuthFailure(ip)
		writeJSON(w, map[string]interface{}{"error": "invalid email format"})
		return
	}
	if len(body.Password) < 8 {
		recordAuthFailure(ip)
		writeJSON(w, map[string]interface{}{"error": "password must be at least 8 characters"})
		return
	}

	if _, _, _, exists, err := store.identityAccount(providerEmail, body.Email); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if exists {
		recordAuthFailure(ip)
		writeJSON(w, map[string]interface{}{"error": "email already registered"})
		return
	}

	accountID, err := store.createEmailAccount(body.Email, hashPassword(body.Password), body.Name)
	if err != nil {
		log.Printf("❌ [AUTH] Register error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"token": generateJWT(accountID, body.Email),
		"user": map[string]interface{}{
			"id":    accountID,
			"email": body.Email,
			"name":  body.Name,
		},
	})
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ip := clientIP(r)
	if authBlocked(ip) {
		http.Error(w, "too many attempts — please wait a minute", http.StatusTooManyRequests)
		return
	}

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Password = strings.TrimSpace(body.Password)

	if body.Email == "" || body.Password == "" {
		recordAuthFailure(ip)
		writeJSON(w, map[string]interface{}{"error": "email and password are required"})
		return
	}

	accountID, credential, name, ok, err := store.identityAccount(providerEmail, body.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Always run the password check (even on a miss, against a dummy hash) so the
	// response time doesn't reveal whether the email exists.
	if !ok {
		credential = "$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidin"
	}
	if !checkPassword(body.Password, credential) || !ok {
		recordAuthFailure(ip)
		writeJSON(w, map[string]interface{}{"error": "invalid email or password"})
		return
	}

	resp := map[string]interface{}{
		"token": generateJWT(accountID, body.Email),
		"user": map[string]interface{}{
			"id":    accountID,
			"email": body.Email,
			"name":  name,
		},
	}
	if tgID := store.accountTelegramID(accountID); tgID.Valid {
		resp["user"].(map[string]interface{})["telegram_chat_id"] = tgID.Int64
	}
	writeJSON(w, resp)
}

// accountTelegramID returns the linked Telegram chat id for an account, if any.
func (s *Store) accountTelegramID(accountID int64) sql.NullInt64 {
	var uid string
	err := s.db.QueryRow(
		"SELECT provider_uid FROM identities WHERE account_id = ? AND provider = ? LIMIT 1",
		accountID, providerTelegram,
	).Scan(&uid)
	if err != nil {
		return sql.NullInt64{}
	}
	id, perr := strconv.ParseInt(uid, 10, 64)
	if perr != nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}

func handleAuthRefresh(w http.ResponseWriter, r *http.Request, _ *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	accountID, email, ok := bearerAccount(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]interface{}{"token": generateJWT(accountID, email)})
}

func handleAuthMe(w http.ResponseWriter, r *http.Request, store *Store) {
	accountID, email, ok := bearerAccount(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	info, err := store.accountProfile(accountID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if info.email != "" {
		email = info.email
	}

	resp := map[string]interface{}{
		"id":         accountID,
		"email":      email,
		"name":       info.name,
		"created_at": info.createdAt,
	}
	if info.telegramID.Valid {
		resp["telegram_chat_id"] = info.telegramID.Int64
	}
	writeJSON(w, resp)
}

func registerAuthRoutes(mux *http.ServeMux, store *Store) {
	mux.HandleFunc("/api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		handleAuthRegister(w, r, store)
	})
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		handleAuthLogin(w, r, store)
	})
	mux.HandleFunc("/api/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		handleAuthRefresh(w, r, store)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		handleAuthMe(w, r, store)
	})
	mux.HandleFunc("/api/auth/telegram/code", func(w http.ResponseWriter, r *http.Request) {
		handleTelegramCode(w, r, store)
	})
	mux.HandleFunc("/api/auth/telegram/verify", func(w http.ResponseWriter, r *http.Request) {
		handleTelegramVerify(w, r, store)
	})
	mux.HandleFunc("/api/auth/link/email", func(w http.ResponseWriter, r *http.Request) {
		handleAuthLinkEmail(w, r, store)
	})
	mux.HandleFunc("/api/auth/link/telegram", func(w http.ResponseWriter, r *http.Request) {
		handleAuthLinkTelegram(w, r, store)
	})
}

// CleanupAuthCodes deletes expired auth codes (older than the rate-limit window).
func CleanupAuthCodes(db *sql.DB) {
	for {
		time.Sleep(loginCodeRateWindow)
		result, err := db.Exec("DELETE FROM auth_codes WHERE created_at < datetime('now', '-10 minutes')")
		if err != nil {
			log.Printf("auth: cleanup error: %v", err)
			continue
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			log.Printf("auth: cleaned up %d expired codes", n)
		}
	}
}

// generateShortCode creates a 6-character alphanumeric code (e.g. "A3F-K9M").
func generateShortCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 7)
	_, _ = rand.Read(b)
	b[3] = '-'
	for i := range b {
		if i != 3 {
			b[i] = chars[b[i]%byte(len(chars))]
		}
	}
	return string(b)
}

// handleTelegramCode creates a short login code for the mobile app.
// Auth: Telegram initData (X-Init-Data header, or initData query fallback).
func handleTelegramCode(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	initData := r.Header.Get("X-Init-Data")
	if initData == "" {
		initData = r.URL.Query().Get("initData")
	}
	chatID, _, ok := validateInitData(initData)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	code, limited, err := store.CreateLoginCode(chatID)
	if err != nil {
		log.Printf("auth: failed to create code: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if limited {
		http.Error(w, "too many requests — wait a moment", http.StatusTooManyRequests)
		return
	}

	writeJSON(w, map[string]interface{}{
		"code":       code,
		"expires_in": int(loginCodeTTL.Seconds()),
	})
}

// handleTelegramVerify exchanges a short login code for a JWT.
// Auth: None (public endpoint for the mobile app; the code itself is the proof).
func handleTelegramVerify(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}

	chatID, status := store.claimLoginCode(strings.TrimSpace(strings.ToUpper(req.Code)))
	switch status {
	case "ok":
		// fall through
	case "invalid":
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	case "used":
		http.Error(w, "code already used", http.StatusUnauthorized)
		return
	case "expired":
		http.Error(w, "code expired", http.StatusUnauthorized)
		return
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("User %d", chatID)
	if err := store.ensureTelegramIdentity(chatID, name); err != nil {
		log.Printf("auth: failed to ensure telegram identity: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	info, _ := store.accountProfile(chatID)
	if info.name != "" {
		name = info.name
	}

	writeJSON(w, map[string]interface{}{
		"token": generateJWT(chatID, info.email),
		"user": map[string]interface{}{
			"id":      chatID,
			"email":   info.email,
			"name":    name,
			"chat_id": chatID,
		},
	})
}

// handleAuthLinkEmail attaches an email/password identity to the caller's account
// (e.g. a Telegram user adding email so they can sign in without Telegram).
// Auth: Bearer JWT.
func handleAuthLinkEmail(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	accountID, _, ok := bearerAccount(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Password = strings.TrimSpace(body.Password)
	body.Name = strings.TrimSpace(body.Name)

	if !emailRegex.MatchString(body.Email) {
		writeJSON(w, map[string]interface{}{"error": "invalid email format"})
		return
	}
	if len(body.Password) < 8 {
		writeJSON(w, map[string]interface{}{"error": "password must be at least 8 characters"})
		return
	}

	existingAccount, _, _, exists, err := store.identityAccount(providerEmail, body.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if exists {
		if existingAccount == accountID {
			writeJSON(w, map[string]interface{}{"ok": true, "alreadyLinked": true})
			return
		}
		// Linking an email already owned by a different account is rejected rather
		// than silently merging — merges flow through link/telegram, where the
		// Telegram side is the unambiguous canonical winner.
		writeJSON(w, map[string]interface{}{"error": "email already in use by another account"})
		return
	}

	if _, err := store.db.Exec(
		"INSERT INTO identities (provider, provider_uid, account_id, credential, name) VALUES (?, ?, ?, ?, ?)",
		providerEmail, body.Email, accountID, hashPassword(body.Password), body.Name,
	); err != nil {
		log.Printf("auth: link email error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleAuthLinkTelegram links a Telegram account to the caller's account using a
// login code the user generated in the bot (e.g. an email user adding Telegram).
// The Telegram chat id is the canonical winner, so the caller's account is merged
// into it and a fresh token for the merged account is returned. Auth: Bearer JWT.
func handleAuthLinkTelegram(w http.ResponseWriter, r *http.Request, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	accountID, _, ok := bearerAccount(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}

	telegramChatID, status := store.claimLoginCode(strings.TrimSpace(strings.ToUpper(body.Code)))
	switch status {
	case "ok":
	case "invalid":
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	case "used":
		http.Error(w, "code already used", http.StatusUnauthorized)
		return
	case "expired":
		http.Error(w, "code expired", http.StatusUnauthorized)
		return
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := store.ensureTelegramIdentity(telegramChatID, fmt.Sprintf("User %d", telegramChatID)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fold the caller's account into the Telegram account (the canonical winner)
	// unless they are already the same account.
	if accountID != telegramChatID {
		if err := store.mergeAccount(accountID, telegramChatID); err != nil {
			log.Printf("auth: merge error (%d into %d): %v", accountID, telegramChatID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	info, _ := store.accountProfile(telegramChatID)
	writeJSON(w, map[string]interface{}{
		"token": generateJWT(telegramChatID, info.email),
		"user": map[string]interface{}{
			"id":      telegramChatID,
			"email":   info.email,
			"name":    info.name,
			"chat_id": telegramChatID,
		},
	})
}
