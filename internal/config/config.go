// Package config holds the bot's runtime configuration: environment-variable
// helpers, env-overridable tuning knobs, the resolved time location, and the
// admin-adjustable content-pool size overrides. It imports only the standard
// library so every other package can depend on it without creating cycles.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// v2 runtime configuration, all overridable via environment variables.
var (
	// Timezone & quiet hours (Change C)
	AppTimezone = GetEnv("TIMEZONE", "Asia/Tehran")
	QuietStart  = GetEnv("QUIET_START", "00:00")
	QuietEnd    = GetEnv("QUIET_END", "09:00")

	// Generation pool tuning (Change B). Targets are deliberately large so that
	// even very active users go a long time before exhausting the unseen pool and
	// falling into recycle-rotation. Override via POOL_TARGET/POOL_MIN to trade
	// content variety against background generation load.
	PoolTarget     = GetEnvInt("POOL_TARGET", 300)
	PoolMin        = GetEnvInt("POOL_MIN", 100)
	RefillInterval = GetEnvDuration("REFILL_INTERVAL", 20*time.Second)
	GenSpacing     = GetEnvDuration("GEN_SPACING", 3*time.Second)

	// Per-(kind,level), per-kind and per-level pool-size overrides, set by the admin
	// via /config and persisted in bot_config (keys "pool_kl_<kind>_<level>",
	// "pool_kind_<kind>", "pool_level_<level>"). When an override exists it replaces
	// the global PoolTarget/PoolMin rule for that pool. Resolution precedence (most
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

// ResolvePoolTarget returns the effective pool target for a (kind, level) pair,
// applying per-(kind,level) then per-kind then per-level overrides before falling
// back to the global rule (PoolTarget at the default level, PoolMin elsewhere).
func ResolvePoolTarget(kind, level string) int {
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
	if level == DefaultLevel {
		return PoolTarget
	}
	return PoolMin
}

// PoolKindOverride returns the per-kind pool target override and whether one is set.
func PoolKindOverride(kind string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolKindTargets[kind]
	return v, ok
}

// PoolLevelOverride returns the per-level pool target override and whether one is set.
func PoolLevelOverride(level string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolLevelTargets[level]
	return v, ok
}

// SetPoolKindOverride sets (n > 0) or clears (n <= 0) the per-kind pool override
// in memory. Returns the cleared/updated state. Persistence is the caller's job.
func SetPoolKindOverride(kind string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolKindTargets, kind)
		return
	}
	poolKindTargets[kind] = n
}

// SetPoolLevelOverride sets (n > 0) or clears (n <= 0) the per-level pool override.
func SetPoolLevelOverride(level string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolLevelTargets, level)
		return
	}
	poolLevelTargets[level] = n
}

// PoolKindLevelOverride returns the per-(kind,level) pool override and whether one
// is set. This is the most specific override tier (e.g. "upper-intermediate words").
func PoolKindLevelOverride(kind, level string) (int, bool) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	v, ok := poolKindLevelTargets[klKey(kind, level)]
	return v, ok
}

// SetPoolKindLevelOverride sets (n > 0) or clears (n <= 0) the per-(kind,level) override.
func SetPoolKindLevelOverride(kind, level string, n int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	if n <= 0 {
		delete(poolKindLevelTargets, klKey(kind, level))
		return
	}
	poolKindLevelTargets[klKey(kind, level)] = n
}

// OverrideCounts returns how many per-(kind,level), per-kind and per-level pool
// overrides are currently set, for the admin config panel.
func OverrideCounts() (kindLevel, kind, level int) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	return len(poolKindLevelTargets), len(poolKindTargets), len(poolLevelTargets)
}

// SnapshotOverrides returns copies of the three override maps. Together with
// RestoreOverrides it lets tests save and restore the override state.
func SnapshotOverrides() (kindLevel, kind, level map[string]int) {
	poolOverrideMu.RLock()
	defer poolOverrideMu.RUnlock()
	cp := func(m map[string]int) map[string]int {
		out := make(map[string]int, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return cp(poolKindLevelTargets), cp(poolKindTargets), cp(poolLevelTargets)
}

// RestoreOverrides replaces the override maps with the provided snapshots.
func RestoreOverrides(kindLevel, kind, level map[string]int) {
	poolOverrideMu.Lock()
	defer poolOverrideMu.Unlock()
	poolKindLevelTargets = kindLevel
	poolKindTargets = kind
	poolLevelTargets = level
}

var (

	// Provider chain order (Change A). Comma-separated provider names; any
	// provider whose key/config is unset at runtime is skipped automatically.
	ProviderOrder = GetEnv("AI_PROVIDER_ORDER", "gemini,groq,cerebras,openrouter,github,cloudflare,mistral,gemini2,sambanova,cohere")

	// Spaced-repetition review tuning (Change D). Batch max is 1 by default so
	// an SRS sweep never sends more than one reminder per user per hour (works
	// together with the global hourly rate limiter in schedule.go).
	ReviewCheckInterval = GetEnvDuration("REVIEW_CHECK_INTERVAL", time.Hour)
	ReviewBatchMax      = GetEnvInt("REVIEW_BATCH_MAX", 1)

	// Quiz / active-recall tuning (Change E). Set QUIZ_INTERVAL=0 to disable
	// scheduled quizzes (the /quiz command still works).
	QuizInterval = GetEnvDuration("QUIZ_INTERVAL", 6*time.Hour)

	// Weekly digest tuning (Change K). Set DIGEST_DAY to "off" to disable
	// the scheduled weekly recap; the default fires Sunday at 20:00 local.
	DigestDay  = GetEnvWeekday("DIGEST_DAY", int(time.Sunday))
	DigestTime = GetEnv("DIGEST_TIME", "20:00")

	// Idiom of the day (Change Q). One idiom is broadcast daily at this local
	// time. Set IDIOM_TIME to "off" to disable the scheduled send (the /idiom
	// command still works).
	IdiomTime = GetEnv("IDIOM_TIME", "09:00")

	// Audio pronunciation (Change I). Set TTS_ENABLED=false to disable globally.
	TTSEnabled = GetEnvBool("TTS_ENABLED", true)

	// Daily grammar tip scheduler.
	TipTime = GetEnv("TIP_TIME", "10:00")

	// Collocation of the day scheduler. One collocation card is broadcast daily
	// at this local time. Set COLLOCATION_TIME to "off" to disable the scheduled
	// send (the /collocation command still works).
	CollocationTime = GetEnv("COLLOCATION_TIME", "13:00")

	// Daily mini story scheduler. One reading-practice story is broadcast daily
	// at this local time. Set STORY_TIME to "off" to disable the scheduled send
	// (the /story command still works).
	StoryTime = GetEnv("STORY_TIME", "17:00")

	// SQLite backup scheduler (maintainer-only delivery).
	BackupTime = GetEnv("BACKUP_TIME", "02:00")

	// Mini App stats dashboard. Set WEB_APP_URL to the public HTTPS URL where the
	// bot's web server is reachable (e.g. "https://bot.example.com"). When set,
	// /stats includes a "📊 Full Dashboard" button that opens the web app.
	// WEB_APP_PORT controls the local HTTP port (default 8090).
	WebAppURL  = GetEnv("WEB_APP_URL", "")
	WebAppPort = GetEnv("WEB_APP_PORT", "8090")

	// BotUsername is the bot's public @handle, used by the Mini App's share/invite
	// link. Defaults to the production handle; override via env when self-hosting.
	BotUsername = GetEnv("BOT_USERNAME", "@mymusclememorybot")
)

// AppLocation is the time.Location used for all scheduling decisions.
var AppLocation = time.UTC

// LoadLocation resolves AppTimezone into AppLocation, falling back to UTC.
func LoadLocation() {
	loc, err := time.LoadLocation(AppTimezone)
	if err != nil {
		log.Printf("⚠️  [CONFIG] Could not load timezone %q: %v. Falling back to UTC.", AppTimezone, err)
		AppLocation = time.UTC
		return
	}
	AppLocation = loc
	log.Printf("⚙️  [CONFIG] Timezone=%s | quiet hours %s–%s | tip time=%s | backup time=%s | pool target=%d min=%d", AppTimezone, QuietStart, QuietEnd, TipTime, BackupTime, PoolTarget, PoolMin)
}

// GetEnv returns the value of the environment variable key, or fallback when unset.
func GetEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// lookupEnv reports whether an environment variable is set and returns its value.
func lookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// GetEnvInt parses an integer env var, falling back when unset or unparsable.
func GetEnvInt(key string, fallback int) int {
	if v, ok := lookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("⚠️  [CONFIG] %s=%q is not an integer; using default %d.", key, v, fallback)
	}
	return fallback
}

// GetEnvDuration parses a duration env var, falling back when unset or unparsable.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := lookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("⚠️  [CONFIG] %s=%q is not a duration; using default %s.", key, v, fallback)
	}
	return fallback
}

// GetEnvBool parses a boolean env var, falling back when unset or unrecognized.
func GetEnvBool(key string, fallback bool) bool {
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

// GetEnvWeekday parses a weekday name (or abbreviation) from an env var.
// Returns -1 when set to "off"/"none"/"disabled" (used to disable the feature).
func GetEnvWeekday(key string, fallback int) int {
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
