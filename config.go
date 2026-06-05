package main

import (
	"log"
	"strconv"
	"strings"
	"time"
)

// v2 runtime configuration, all overridable via environment variables.
var (
	// Timezone & quiet hours (Change C)
	appTimezone = getEnv("TIMEZONE", "Asia/Tehran")
	quietStart  = getEnv("QUIET_START", "00:00")
	quietEnd    = getEnv("QUIET_END", "09:00")

	// Generation pool tuning (Change B). Targets are deliberately large so that
	// even very active users go a long time before exhausting the unseen pool and
	// falling into recycle-rotation. Override via POOL_TARGET/POOL_MIN to trade
	// content variety against background generation load.
	poolTarget     = getEnvInt("POOL_TARGET", 300)
	poolMin        = getEnvInt("POOL_MIN", 100)
	refillInterval = getEnvDuration("REFILL_INTERVAL", 20*time.Second)
	genSpacing     = getEnvDuration("GEN_SPACING", 3*time.Second)

	// Provider chain order (Change A). Comma-separated provider names; any
	// provider whose key/config is unset at runtime is skipped automatically.
	providerOrder = getEnv("AI_PROVIDER_ORDER", "gemini,groq,cerebras,openrouter,github,cloudflare,mistral,gemini2,sambanova,cohere")

	// Spaced-repetition review tuning (Change D). Batch max is 1 by default so
	// an SRS sweep never sends more than one reminder per user per hour (works
	// together with the global hourly rate limiter in schedule.go).
	reviewCheckInterval = getEnvDuration("REVIEW_CHECK_INTERVAL", time.Hour)
	reviewBatchMax      = getEnvInt("REVIEW_BATCH_MAX", 1)

	// Quiz / active-recall tuning (Change E). Set QUIZ_INTERVAL=0 to disable
	// scheduled quizzes (the /quiz command still works).
	quizInterval = getEnvDuration("QUIZ_INTERVAL", 6*time.Hour)

	// Weekly digest tuning (Change K). Set DIGEST_DAY to "off" to disable
	// the scheduled weekly recap; the default fires Sunday at 20:00 local.
	digestDay  = getEnvWeekday("DIGEST_DAY", int(time.Sunday))
	digestTime = getEnv("DIGEST_TIME", "20:00")

	// Idiom of the day (Change Q). One idiom is broadcast daily at this local
	// time. Set IDIOM_TIME to "off" to disable the scheduled send (the /idiom
	// command still works).
	idiomTime = getEnv("IDIOM_TIME", "09:00")

	// Audio pronunciation (Change I). Set TTS_ENABLED=false to disable globally.
	ttsEnabled = getEnvBool("TTS_ENABLED", true)

	// Daily grammar tip scheduler.
	tipTime = getEnv("TIP_TIME", "10:00")
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
	log.Printf("⚙️  [CONFIG] Timezone=%s | quiet hours %s–%s | tip time=%s | pool target=%d min=%d", appTimezone, quietStart, quietEnd, tipTime, poolTarget, poolMin)
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

func getEnvBool(key string, fallback bool) bool {
	v, ok := lookupEnv(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("⚠️  [CONFIG] %s=%q is not a boolean; using default %t.", key, v, fallback)
		return fallback
	}
}

// getEnvWeekday parses a weekday name (or abbreviation) from an env var.
// Returns -1 when set to "off"/"none"/"disabled" (used to disable the feature).
func getEnvWeekday(key string, fallback int) int {
	v, ok := lookupEnv(key)
	if !ok {
		return fallback
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "off" || v == "none" || v == "disabled" || v == "" {
		return -1
	}
	weekdays := map[string]int{
		"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3,
		"thursday": 4, "friday": 5, "saturday": 6,
		"sun": 0, "mon": 1, "tue": 2, "wed": 3,
		"thu": 4, "fri": 5, "sat": 6,
	}
	if d, found := weekdays[v]; found {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 6 {
		return n
	}
	log.Printf("⚠️  [CONFIG] %s=%q is not a valid weekday; using default.", key, v)
	return fallback
}
