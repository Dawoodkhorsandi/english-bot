package main

import (
	"log"
	"strconv"
	"strings"
	"sync"
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

	// Per-(kind,level), per-kind and per-level pool-size overrides, set by the admin
	// via /config and persisted in bot_config (keys "pool_kl_<kind>_<level>",
	// "pool_kind_<kind>", "pool_level_<level>"). When an override exists it replaces
	// the global poolTarget/poolMin rule for that pool. Resolution precedence (most
	// specific wins): per-(kind,level) → per-kind → per-level → global rule. Guarded
	// by poolOverrideMu because the pool filler goroutine reads them while the admin
	// config callback (poller goroutine) writes them.
	poolOverrideMu       sync.RWMutex
	poolKindLevelTargets = map[string]int{}
	poolKindTargets      = map[string]int{}
	poolLevelTargets     = map[string]int{}
)

// klKey builds the map key for a per-(kind,level) pool override.
func klKey(kind, level string) string { return kind + "\x00" + level }

// configurableKinds is the set of content kinds whose pool size the admin can
// override individually via /config.
var configurableKinds = []string{kindDrill, kindWord, kindIdiom, kindCollocation, kindStory, kindTip}

// isConfigurableKind reports whether kind is one the admin may override.
func isConfigurableKind(kind string) bool {
	for _, k := range configurableKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// resolvePoolTarget returns the effective pool target for a (kind, level) pair,
// applying per-(kind,level) then per-kind then per-level overrides before falling
// back to the global rule (poolTarget at the default level, poolMin elsewhere).
func resolvePoolTarget(kind, level string) int {
	poolOverrideMu.RLock()
	if v, ok := poolKindLevelTargets[klKey(kind, level)]; ok {
		poolOverrideMu.RUnlock()
		return v
	}
	if v, ok := poolKindTargets[kind]; ok {
		poolOverrideMu.RUnlock()
		return v
	}
	if v, ok := poolLevelTargets[level]; ok {
		poolOverrideMu.RUnlock()
		return v
	}
	poolOverrideMu.RUnlock()
	if level == defaultLevel {
		return poolTarget
	}
	return poolMin
}

// poolKindOverride returns the per-kind pool target override and whether one is set.
func poolKindOverride(kind string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolKindTargets[kind]
	return v, ok
}

// poolLevelOverride returns the per-level pool target override and whether one is set.
func poolLevelOverride(level string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolLevelTargets[level]
	return v, ok
}

// setPoolKindOverride sets (n > 0) or clears (n <= 0) the per-kind pool override
// in memory. Returns the cleared/updated state. Persistence is the caller's job.
func setPoolKindOverride(kind string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolKindTargets, kind)
		return
	}
	poolKindTargets[kind] = n
}

// setPoolLevelOverride sets (n > 0) or clears (n <= 0) the per-level pool override.
func setPoolLevelOverride(level string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolLevelTargets, level)
		return
	}
	poolLevelTargets[level] = n
}

// poolKindLevelOverride returns the per-(kind,level) pool override and whether one
// is set. This is the most specific override tier (e.g. "upper-intermediate words").
func poolKindLevelOverride(kind, level string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolKindLevelTargets[klKey(kind, level)]
	return v, ok
}

// setPoolKindLevelOverride sets (n > 0) or clears (n <= 0) the per-(kind,level) override.
func setPoolKindLevelOverride(kind, level string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolKindLevelTargets, klKey(kind, level))
		return
	}
	poolKindLevelTargets[klKey(kind, level)] = n
}

var (

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

	// Collocation of the day scheduler. One collocation card is broadcast daily
	// at this local time. Set COLLOCATION_TIME to "off" to disable the scheduled
	// send (the /collocation command still works).
	collocationTime = getEnv("COLLOCATION_TIME", "13:00")

	// Daily mini story scheduler. One reading-practice story is broadcast daily
	// at this local time. Set STORY_TIME to "off" to disable the scheduled send
	// (the /story command still works).
	storyTime = getEnv("STORY_TIME", "17:00")

	// SQLite backup scheduler (maintainer-only delivery).
	backupTime = getEnv("BACKUP_TIME", "02:00")

	// Mini App stats dashboard. Set WEB_APP_URL to the public HTTPS URL where the
	// bot's web server is reachable (e.g. "https://bot.example.com"). When set,
	// /stats includes a "📊 Full Dashboard" button that opens the web app.
	// WEB_APP_PORT controls the local HTTP port (default 8090).
	webAppURL  = getEnv("WEB_APP_URL", "")
	webAppPort = getEnv("WEB_APP_PORT", "8090")

	// botUsername is the bot's public @handle, used by the Mini App's share/invite
	// link. Defaults to the production handle; override via env when self-hosting.
	botUsername = getEnv("BOT_USERNAME", "@mymusclememorybot")
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
	log.Printf("⚙️  [CONFIG] Timezone=%s | quiet hours %s–%s | tip time=%s | backup time=%s | pool target=%d min=%d", appTimezone, quietStart, quietEnd, tipTime, backupTime, poolTarget, poolMin)
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
