package main

import (
	"log"
	"strconv"
	"time"
)

// v2 runtime configuration, all overridable via environment variables.
var (
	// Timezone & quiet hours (Change C)
	appTimezone = getEnv("TIMEZONE", "Asia/Tehran")
	quietStart  = getEnv("QUIET_START", "00:00")
	quietEnd    = getEnv("QUIET_END", "09:00")

	// Generation pool tuning (Change B)
	poolTarget     = getEnvInt("POOL_TARGET", 30)
	poolMin        = getEnvInt("POOL_MIN", 10)
	refillInterval = getEnvDuration("REFILL_INTERVAL", 20*time.Second)
	genSpacing     = getEnvDuration("GEN_SPACING", 3*time.Second)

	// Provider chain order (Change A). Comma-separated provider names; any
	// provider whose key/config is unset at runtime is skipped automatically.
	providerOrder = getEnv("AI_PROVIDER_ORDER", "gemini,groq,cerebras,openrouter,github,cloudflare,mistral")
)

// appLocation is the time.Location used for all scheduling decisions.
var appLocation = time.UTC

// loadLocation resolves appTimezone into appLocation, falling back to UTC.
func loadLocation() {
	loc, err := time.LoadLocation(appTimezone)
	if err != nil {
		log.Printf("⚠️  [CONFIG] Could not load timezone %q: %v. Falling back to UTC.", appTimezone, err)
		appLocation = time.UTC
		return
	}
	appLocation = loc
	log.Printf("⚙️  [CONFIG] Timezone=%s | quiet hours %s–%s | pool target=%d min=%d", appTimezone, quietStart, quietEnd, poolTarget, poolMin)
}

func getEnvInt(key string, fallback int) int {
	if v, ok := lookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("⚠️  [CONFIG] %s=%q is not an integer; using default %d.", key, v, fallback)
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := lookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("⚠️  [CONFIG] %s=%q is not a duration; using default %s.", key, v, fallback)
	}
	return fallback
}
