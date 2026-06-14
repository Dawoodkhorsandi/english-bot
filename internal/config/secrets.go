package config

import (
	"errors"
	"fmt"
)

// defaultJWTSecret is the placeholder shipped in the repo. Booting with this
// value (or an empty one) would let anyone forge a valid JWT for any account, so
// ValidateSecrets refuses to start unless JWT_SECRET is overridden.
const defaultJWTSecret = "change-me-to-a-random-string"

// minJWTSecretLen is the shortest JWT_SECRET we accept. HMAC-SHA256 keys shorter
// than the 32-byte hash output weaken the signature; require at least that.
const minJWTSecretLen = 32

// Credentials and identities, read from the environment at startup. GeminiAPIKey
// is consumed by the ai package and TelegramBotToken by the telegram package, so
// they live here in the shared leaf rather than in app.
var (
	TelegramBotToken = GetEnv("TELEGRAM_BOT_TOKEN", "YOUR_TELEGRAM_BOT_TOKEN")
	GeminiAPIKey     = GetEnv("GEMINI_API_KEY", "YOUR_GEMINI_API_KEY")
	MaintainerChatID = GetEnv("MAINTAINER_CHAT_ID", "YOUR_PERSONAL_CHAT_ID")
	JWTSecret        = GetEnv("JWT_SECRET", defaultJWTSecret)
)

// ValidateSecrets fails fast on misconfigured credentials that would otherwise
// surface as silent security holes at request time. Today it guards JWT_SECRET:
// an empty or default secret makes every issued JWT forgeable, so the bot must
// refuse to boot rather than serve auth with a publicly-known key.
func ValidateSecrets() error {
	switch {
	case JWTSecret == "" || JWTSecret == defaultJWTSecret:
		return errors.New("JWT_SECRET is unset or still the placeholder — set a strong, random JWT_SECRET (inside ENV_FILE in prod) before starting")
	case len(JWTSecret) < minJWTSecretLen:
		return fmt.Errorf("JWT_SECRET is too short (%d chars) — use at least %d random characters", len(JWTSecret), minJWTSecretLen)
	}
	return nil
}
