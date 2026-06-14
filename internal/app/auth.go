package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func hashPassword(password string) string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash)
}

func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func jwtSecretKey() []byte {
	secret := config.JWTSecret
	if secret == "" {
		secret = "change-me-to-a-random-string"
	}
	return []byte(secret)
}

func generateJWT(chatID int64, email string) string {
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	nonceBytes := make([]byte, 8)
	_, _ = rand.Read(nonceBytes)

	payload := map[string]interface{}{
		"sub": chatID,
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

func validateJWT(tokenStr string) (chatID int64, email string, ok bool) {
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

	var exists int
	_ = store.db.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", body.Email).Scan(&exists)
	if exists > 0 {
		writeJSON(w, map[string]interface{}{"error": "email already registered"})
		return
	}

	hash := hashPassword(body.Password)
	result, err := store.db.Exec(
		"INSERT INTO users (email, password_hash, name) VALUES (?, ?, ?)",
		body.Email, hash, body.Name,
	)
	if err != nil {
		log.Printf("❌ [AUTH] Register DB error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	userID, _ := result.LastInsertId()
	token := generateJWT(userID, body.Email)

	writeJSON(w, map[string]interface{}{
		"token": token,
		"user": map[string]interface{}{
			"id":    userID,
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

	body.Email = strings.TrimSpace(strings.ToLower(body.Email))
	body.Password = strings.TrimSpace(body.Password)

	if body.Email == "" || body.Password == "" {
		writeJSON(w, map[string]interface{}{"error": "email and password are required"})
		return
	}

	var (
		id     int64
		hash   string
		name   string
		chatID sql.NullInt64
	)
	err := store.db.QueryRow(
		"SELECT id, password_hash, name, telegram_chat_id FROM users WHERE email = ?",
		body.Email,
	).Scan(&id, &hash, &name, &chatID)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": "invalid email or password"})
		return
	}

	if !checkPassword(body.Password, hash) {
		writeJSON(w, map[string]interface{}{"error": "invalid email or password"})
		return
	}

	token := generateJWT(id, body.Email)

	resp := map[string]interface{}{
		"token": token,
		"user": map[string]interface{}{
			"id":    id,
			"email": body.Email,
			"name":  name,
		},
	}
	if chatID.Valid {
		resp["user"].(map[string]interface{})["telegram_chat_id"] = chatID.Int64
	}

	writeJSON(w, resp)
}

func handleAuthRefresh(w http.ResponseWriter, r *http.Request, _ *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenStr == "" || tokenStr == authHeader {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	chatID, email, ok := validateJWT(tokenStr)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	token := generateJWT(chatID, email)
	writeJSON(w, map[string]interface{}{"token": token})
}

func handleAuthMe(w http.ResponseWriter, r *http.Request, store *Store) {
	authHeader := r.Header.Get("Authorization")
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenStr == "" || tokenStr == authHeader {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	chatID, email, ok := validateJWT(tokenStr)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var (
		name    string
		tgID    sql.NullInt64
		created string
	)
	err := store.db.QueryRow(
		"SELECT name, telegram_chat_id, created_at FROM users WHERE id = ?",
		chatID,
	).Scan(&name, &tgID, &created)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	resp := map[string]interface{}{
		"id":         chatID,
		"email":      email,
		"name":       name,
		"created_at": created,
	}
	if tgID.Valid {
		resp["telegram_chat_id"] = tgID.Int64
	}

	writeJSON(w, resp)
}

func withJWTAuth(next apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenStr == "" || tokenStr == authHeader {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		chatID, _, ok := validateJWT(tokenStr)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", "*")
		next(w, r, chatID, nil)
	}
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
}
