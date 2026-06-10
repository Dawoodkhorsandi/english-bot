# English Muscle Memory Bot -- Technical Documentation

> For setup instructions, bot commands, and configuration, see **[README.md](README.md)**.
> For contribution guidelines, see **[CONTRIBUTING.md](CONTRIBUTING.md)**.
>
> This document covers internal architecture, data flows, prompt design, schema
> details, and the development roadmap.

## Overview

A Telegram bot written in Go that sends subscribers AI-generated English practice every 30 minutes, alternating between two formats:

- 🎯 a **grammar drill** — one verb across 21 forms (12 tenses, 4 conditionals, modals, passive, imperative, *used to*), delivered as five navigable pages
- 📘 a **vocabulary word** — meaning, pronunciation, synonyms, opposites and example sentences
- 💡 a **daily grammar tip** — one focused rule/usage nuance with correct/incorrect examples

The bot uses Google Gemini 2.5 Flash to generate unique, personalized content and tracks which verbs and words have already been sent to each user, ensuring they always receive fresh material. When a new version is deployed, existing subscribers automatically receive a changelog message on their next broadcast or `/start`.

---

## Architecture

```
Go application (multi-file, single package main)
├── main.go           — startup, wiring, Telegram types, command router, Store (SQLite), env-var helpers (getEnv, lookupEnv)
├── config.go         — timezone, pool & scheduler tuning knobs, WEB_APP_URL/WEB_APP_PORT
├── providers.go      — Provider interface, GeminiProvider, OpenAICompatProvider, ProviderChain
├── generation.go     — prompt builders, generateContent, generateWordFor, term/meaning parsers
├── pool.go           — content_pool Store methods, serveContent, poolFiller goroutine
├── schedule.go       — quiet hours, broadcast scheduler, daily review scheduler, daily tip scheduler, weekly digest scheduler
├── prefs.go          — user_prefs Store methods, level/interval/pause/tip/feature-toggle helpers, SetFirstName, streak helpers
├── tts.go            — pronunciation TTS generation (Gemini + espeak-ng fallback) and sendVoice helper
├── srs.go            — spaced-repetition SM-2-lite logic, review_schedule Store methods, review scheduler
├── quiz.go           — quiz building (4 types + native poll), quiz_results, poll registry, quiz scheduler
├── stats.go          — /stats computation (streaks, activity days, progressBar, formatStats), admin metrics
├── admin.go          — admin panel: paginated /users list, user detail view, direct messaging to users
├── webapp.go         — optional Telegram Mini App HTTP server; HMAC-SHA256 initData validation; /api/stats JSON endpoint
└── vocab.go          — /mywords (browse learned vocabulary) and /bookmark (save favourite words) features
```

The application runs thirteen concurrent goroutines (plus an optional web server goroutine when `WEB_APP_URL` is set):
1. **Pool filler** (`poolFiller`) — tops up the pre-generated content pool in the background
2. **Broadcast scheduler** (`runBroadcastScheduler`) — fires every half hour, per-user interval-aware delivery sweep
3. **Daily review scheduler** (`runDailyReviewScheduler`) — sends a bedtime word recap at local midnight
4. **Spaced-repetition scheduler** (`runReviewScheduler`) — resurfaces due words for memory-check review
5. **Quiz scheduler** (`runQuizScheduler`) — periodically sends a multiple-choice quiz
6. **Weekly digest scheduler** (`runWeeklyDigestScheduler`) — sends a weekly recap (default Sunday 20:00)
7. **Idiom scheduler** (`runIdiomScheduler`) — sends one daily idiom (default 09:00 local)
8. **Daily tip scheduler** (`runDailyTipScheduler`) — sends one daily grammar tip (default 10:00 local)
9. **Collocation scheduler** (`runCollocationScheduler`) — sends one daily collocation card (default 13:00 local) (v1.23.0)
10. **Mini story scheduler** (`runStoryScheduler`) — sends one daily reading-practice story (default 17:00 local) (v1.23.0)
11. **Nightly backup scheduler** (`runDBBackupScheduler`) — sends a SQLite snapshot to the maintainer (default 02:00 local)
12. **Telegram poller** (`pollTelegramUpdates`) — long-polls Telegram for incoming messages and callback queries
13. **Main goroutine** — blocks on `os.Signal` for graceful shutdown

---

## Configuration

All configuration is read from environment variables at startup. There are no config files.

| Variable | Default | Description |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | `YOUR_TELEGRAM_BOT_TOKEN` | Telegram Bot API token from @BotFather |
| `GEMINI_API_KEY` | `YOUR_GEMINI_API_KEY` | Google Gemini API key |
| `MAINTAINER_CHAT_ID` | `YOUR_PERSONAL_CHAT_ID` | Chat ID that receives new-user join notifications; also gates admin commands (`/admin`, `/metrics`, `/poolusage`, `/announce`, `/health`, `/users`, `/backup`) |
| `TIMEZONE` | `Asia/Tehran` | IANA timezone for quiet hours, daily review, and digest scheduling |
| `QUIET_START` | `00:00` | Start of the quiet window (no scheduled sends) |
| `QUIET_END` | `09:00` | End of the quiet window |
| `TTS_ENABLED` | `true` | Global kill-switch for pronunciation audio (`sendVoice`) |
| `TIP_TIME` | `10:00` | Local time to send the daily grammar tip |
| `POOL_TARGET` | `300` | Target number of pre-generated items per (kind, level) in `content_pool` |
| `POOL_MIN` | `100` | Low-water mark that triggers refill |
| `REFILL_INTERVAL` | `20s` | How often the pool filler goroutine wakes |
| `GEN_SPACING` | `3s` | Minimum spacing between AI generation calls |
| `AI_PROVIDER_ORDER` | `gemini,groq,cerebras,openrouter,github,cloudflare,mistral,gemini2,sambanova,cohere` | Comma-separated provider fallback order |
| `REVIEW_CHECK_INTERVAL` | `1h` | How often the SRS scheduler checks for due reviews |
| `REVIEW_BATCH_MAX` | `1` | Max SRS review cards sent per user per sweep (default 1 to avoid batching multiple cards at once) |
| `QUIZ_INTERVAL` | `6h` | Global kill-switch for quiz scheduler (`0` disables entirely). Per-user quiz frequency is now controlled via `/settings` (default 6h). |
| `DIGEST_DAY` | `Sunday` | Weekday the weekly digest is sent |
| `DIGEST_TIME` | `20:00` | Time of day the weekly digest is sent |
| `IDIOM_TIME` | `09:00` | Local time the daily idiom of the day is sent; `off` disables it |
| `COLLOCATION_TIME` | `13:00` | Local time the daily collocation of the day is sent; `off` disables it (v1.23.0) |
| `STORY_TIME` | `17:00` | Local time the daily mini story is sent; `off` disables it (v1.23.0) |
| `BACKUP_TIME` | `02:00` | Local time the nightly SQLite backup is sent to maintainer; `off` disables it |
| `WEB_APP_URL` | *(unset)* | Public HTTPS base URL where the bot's web server is reachable (e.g. `https://bot.example.com`). When set, `/stats` shows a `📊 Full Dashboard` button that opens the Telegram Mini App. Leave unset to disable the web server entirely. (v1.18.0) |
| `WEB_APP_PORT` | `8090` | Local TCP port for the embedded Mini App HTTP server. Only used when `WEB_APP_URL` is set. (v1.18.0) |

Per-provider API keys, base-URL overrides, and model overrides (e.g. `GROQ_MODEL`,
`CEREBRAS_BASE_URL`, `GEMINI2_MODEL`, `SAMBANOVA_API_KEY`, `COHERE_API_KEY`) are
listed in [Change A — Provider configuration](#provider-configuration) and in `.env.example`.

At startup the bot logs whether the Telegram token is loaded (true/false) and its length — it never logs the actual token value. The maintainer chat ID is logged as-is (it is not a secret).

---

## Changelog System

The bot has a built-in mechanism to notify existing subscribers when a new version is deployed.

### How it works

1. The `Changelogs` slice in `main.go` is the append-only release history. Each entry has a `Version` string, an HTML-formatted `Text` message, and an optional `Silent` flag.
2. When a **new user** runs `/start`, all existing changelog versions are immediately marked as seen — they only receive notes for versions released after they joined.
3. **Existing users** receive any unseen changelog entries:
   - At the start of each half-hourly broadcast cycle (before their drill)
   - When they run `/start` again
4. **Silent entries** (`Silent: true`) are marked as seen without sending any message to regular users. The **maintainer** (identified by `MaintainerChatID`) still receives silent entries with a `🔧 [Internal Deploy vX.Y.Z]` prefix so they know exactly when a deploy landed. This is useful for internal fixes and housekeeping releases that don't warrant a user-facing notification.

The `changelog_delivery` table tracks which versions each user has already received, so each entry is delivered exactly once per user.

### Shipping a new version

Append one entry to `Changelogs` in `main.go` and redeploy:

```go
var Changelogs = []ChangelogEntry{
    {
        Version: "1.1.0",
        Text:    "📣 <b>What's New</b>\n\n• ...",
    },
    {
        Version: "1.2.0",
        Text:    "📣 <b>What's New in v1.2.0</b>\n\n• ...",
    },
    {
        Version: "1.3.0",        // ← new entry
        Text:    "📣 <b>What's New in v1.3.0</b>\n\n• ...",
    },
}
```

Every subscriber will receive the new entry on their next broadcast (quiet hours aside).

---

## Database

**Driver:** `modernc.org/sqlite` (pure-Go, no CGO)
**File:** `subscribers.db`

### Schema

#### `subscribers`
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER PRIMARY KEY` | Telegram chat ID |
| `created_at` | `DATETIME` | When the user first ran /start |

#### `sent_words`
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased verb that was already sent |
| `sent_at` | `DATETIME` | When the verb was first sent |
| `last_sent_at` | `DATETIME` | When the verb was most recently served (v1.13.0); drives least-recently-served rotation |

Primary key is `(chat_id, word)` — first insert sets `sent_at`; every subsequent serve
bumps `last_sent_at` via `ON CONFLICT … DO UPDATE` (v1.13.0).
Index: `idx_sent_words_chat` on `chat_id` for fast per-user lookups.

#### `sent_vocab`
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased vocabulary word that was already sent |
| `sent_at` | `DATETIME` | When the word was first sent |
| `last_sent_at` | `DATETIME` | When the word was most recently served (v1.13.0); drives least-recently-served rotation |

Primary key is `(chat_id, word)` — first insert sets `sent_at`; every subsequent serve
bumps `last_sent_at` via `ON CONFLICT … DO UPDATE` (v1.13.0).
Index: `idx_sent_vocab_chat` on `chat_id`. Kept separate from `sent_words` so grammar-drill verbs and vocabulary words have independent exclusion lists.

#### `sent_idioms` (v1.12.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased idiom phrase already sent (column named `word` so the generic `PooledUnseen` query works across kinds) |
| `sent_at` | `DATETIME` | When the idiom was first sent |
| `last_sent_at` | `DATETIME` | When the idiom was most recently served (v1.13.0); drives least-recently-served rotation |

Primary key `(chat_id, word)` — first insert sets `sent_at`; every subsequent serve
bumps `last_sent_at` via `ON CONFLICT … DO UPDATE` (v1.13.0). Index
`idx_sent_idioms_chat` on `chat_id`. The per-user exclusion list for idioms (Change Q).

#### `sent_tips` (v1.15.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased grammar-tip topic already sent to this user |
| `sent_at` | `DATETIME` | When the tip topic was first sent |
| `last_sent_at` | `DATETIME` | When the tip was most recently served; drives least-recently-served rotation |

Primary key is `(chat_id, word)` — each user sees each tip topic at most once.
Index: `idx_sent_tips_chat` on `chat_id`.

#### `sent_collocations` (v1.23.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased collocation phrase already sent (column named `word` so the generic `PooledUnseen` query works across kinds) |
| `sent_at` | `DATETIME` | When the collocation was first sent |
| `last_sent_at` | `DATETIME` | When the collocation was most recently served; drives least-recently-served rotation |

Primary key `(chat_id, word)`. Index `idx_sent_collocations_chat` on `chat_id`.
The per-user exclusion list for collocations.

#### `sent_stories` (v1.23.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased mini-story title already sent (column named `word` so the generic `PooledUnseen` query works across kinds) |
| `sent_at` | `DATETIME` | When the story was first sent |
| `last_sent_at` | `DATETIME` | When the story was most recently served; drives least-recently-served rotation |

Primary key `(chat_id, word)`. Index `idx_sent_stories_chat` on `chat_id`.
The per-user exclusion list for mini stories.

#### `changelog_delivery`
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `version` | `TEXT` | Changelog version string |
| `sent_at` | `DATETIME` | When this version was delivered |

Primary key is `(chat_id, version)` — each version is delivered at most once per user.

#### `content_pool` (v1.3.0)
| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PRIMARY KEY AUTOINCREMENT` | Row ID |
| `kind` | `TEXT` | `drill` or `word` |
| `term` | `TEXT` | Lowercased verb/word the item teaches |
| `meaning` | `TEXT` | One-line meaning (words only; empty for drills) |
| `text` | `TEXT` | Ready-to-send HTML message |
| `level` | `TEXT` | Difficulty: `beginner` / `intermediate` / `upper-intermediate` / `advanced` (added v1.4.0, upper-intermediate v1.16.0, default `intermediate`) |
| `created_at` | `DATETIME` | When the item was generated |

Unique constraint on `(kind, level, term)` (v1.13.0; previously `(kind, term)`) — the
pool deduplicates per kind *and* level, so the same term can be pooled at multiple
difficulty levels independently. Index: `idx_content_pool_kind` on `(kind, level, created_at)`.
The `poolFiller` goroutine keeps each `(kind, level)` for every *active* level topped
up (`POOL_TARGET` for the default level, `POOL_MIN` for others); `serveContent` reads
from here first, filtered by the user's level, and falls back to inline generation
(commands) or a recycled/oldest pooled item at that level (broadcasts).

> **Migration:** `Store.migrate` adds the `level` column via `ALTER TABLE` on older
> databases that predate v1.4.0 (guarded by a `PRAGMA table_info` check). From v1.13.0,
> it also rebuilds the table from `UNIQUE(kind, term)` to `UNIQUE(kind, level, term)`
> (guarded by `poolDedupIsLevelAware`) and adds `last_sent_at` to the sent tables.

#### `daily_review_delivery` (v1.3.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `review_date` | `TEXT` | Local `YYYY-MM-DD` of the reviewed day |
| `sent_at` | `DATETIME` | When the review was delivered |

Primary key is `(chat_id, review_date)` — the midnight word review is sent at most
once per user per day.

#### `idiom_delivery` (v1.12.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `idiom_date` | `TEXT` | Local `YYYY-MM-DD` the idiom of the day was sent for |
| `sent_at` | `DATETIME` | When the idiom was delivered |

Primary key is `(chat_id, idiom_date)` — the idiom of the day is sent at most once
per user per day (Change Q).

#### `daily_tip_delivery` (v1.15.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `tip_date` | `TEXT` | Local `YYYY-MM-DD` of the sent tip sweep |
| `sent_at` | `DATETIME` | When the tip was delivered |

Primary key is `(chat_id, tip_date)` — one scheduled tip per user per day.

#### `collocation_delivery` (v1.23.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `collocation_date` | `TEXT` | Local `YYYY-MM-DD` the collocation of the day was sent for |
| `sent_at` | `DATETIME` | When the collocation was delivered |

Primary key is `(chat_id, collocation_date)` — one scheduled collocation per user per day.

#### `story_delivery` (v1.23.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `story_date` | `TEXT` | Local `YYYY-MM-DD` the mini story was sent for |
| `sent_at` | `DATETIME` | When the story was delivered |

Primary key is `(chat_id, story_date)` — one scheduled mini story per user per day.

#### `pool_exhaustion_notice` (v1.23.4)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `kind` | `TEXT` | Content kind that was exhausted |
| `level` | `TEXT` | Difficulty level that was exhausted |
| `pool_count` | `INTEGER` | Pool size at the time the maintainer was last alerted |
| `notified_at` | `DATETIME` | When the alert was last sent |

Primary key is `(chat_id, kind, level)`. Dedupes the maintainer pool-exhaustion
alert: `maybeNotifyPoolExhausted` (pool.go) fires from `serveContent`'s recycle
branch (the user has seen everything and is getting repeats) and sends at most one
alert per (chat, kind, level) until the pool grows beyond `pool_count`, at which
point a re-exhaustion re-alerts. Best-effort and nil-notifier-safe.

#### `user_prefs` (v1.4.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER PRIMARY KEY` | FK → subscriber chat_id |
| `level` | `TEXT` | Difficulty, default `intermediate` (Change F) |
| `paused` | `INTEGER` | `1` = scheduled sends paused, default `0` (Change H) |
| `interval_minutes` | `INTEGER` | Minutes between scheduled broadcast sends, default `60` (Change L; v1.17.0 changed default from 30→60) |
| `tts_enabled` | `INTEGER` | `1` = pronunciation audio enabled, default `1` (Change I) |
| `tips_enabled` | `INTEGER` | `1` = daily scheduled grammar tips enabled, default `1` (v1.15.0) |
| `quiz_enabled` | `INTEGER` | `1` = scheduled quizzes enabled, default `1` (v1.17.0) |
| `idiom_enabled` | `INTEGER` | `1` = daily idiom of the day enabled, default `1` (v1.17.0) |
| `collocation_enabled` | `INTEGER` | `1` = daily collocation of the day enabled, default `1` (v1.23.0) |
| `story_enabled` | `INTEGER` | `1` = daily mini story enabled, default `1` (v1.23.0) |
| `review_enabled` | `INTEGER` | `1` = SRS word memory-check reviews enabled, default `1` (v1.17.0) |
| `digest_enabled` | `INTEGER` | `1` = weekly digest enabled, default `1` (v1.17.0) |
| `daily_review_enabled` | `INTEGER` | `1` = midnight vocabulary recap enabled, default `1` (v1.17.0) |
| `quiz_interval_hours` | `INTEGER` | Hours between scheduled quizzes, default `6` (v1.17.0) |
| `first_name` | `TEXT` | User's Telegram first name, updated on every inbound message; used in streak celebrations and personalised greetings (v1.18.0) |
| `streak_celebrated` | `INTEGER` | Highest streak milestone (3/7/14/30/60) already celebrated; prevents repeat celebrations, default `0` (v1.18.0) |
| `updated_at` | `DATETIME` | Last change time |

Rows are created lazily via upsert (`INSERT … ON CONFLICT`) the first time a user sets
a level or pauses; `GetPrefs` returns defaults when no row exists. Managed by the
methods in `prefs.go`.

#### `review_schedule` (v1.8.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | The term being scheduled for review |
| `interval_days` | `INTEGER` | Current SM-2 interval in days, default `1` |
| `ease` | `REAL` | SM-2 ease factor, default `2.5` |
| `reps` | `INTEGER` | Number of successful repetitions so far, default `0` |
| `due_at` | `DATETIME` | When this word is next due for review |

Primary key is `(chat_id, word)`; index `idx_review_due on (chat_id, due_at)`
drives the "what's due now" query. Backs the spaced-repetition review (Change D);
managed by `srs.go`.

#### `quiz_results` (v1.9.0)
| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PRIMARY KEY AUTOINCREMENT` | Surrogate key |
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | The term that was quizzed |
| `correct` | `INTEGER` | `1` if answered correctly, `0` otherwise |
| `answered_at` | `DATETIME` | When the quiz was answered |

Index `idx_quiz_chat on chat_id`. Records each quiz answer for stats and SRS
scheduling (Change E); managed by `quiz.go`.

#### `weekly_digest_delivery` (v1.10.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `week_start` | `TEXT` | ISO `YYYY-MM-DD` of the Monday starting the covered week |
| `sent_at` | `DATETIME` | When the digest was delivered |

Primary key is `(chat_id, week_start)` — the weekly digest is sent at most
once per user per week.

#### `audio_cache` (v1.14.0)
| Column | Type | Description |
|---|---|---|
| `word` | `TEXT PRIMARY KEY` | Lowercased word or idiom phrase |
| `file_id` | `TEXT` | Telegram file_id from a previous `sendVoice` upload |
| `created_at` | `DATETIME` | When the cache entry was created |

Stores the Telegram `file_id` returned by `sendVoice` so subsequent pronunciation
sends for the same word reuse the cached file (zero-cost re-send, no TTS regeneration).
Inserts are idempotent (`INSERT OR IGNORE`).

#### `bookmarks` (v1.21.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased vocabulary word |
| `added_at` | `DATETIME` | When the bookmark was created |

Primary key is `(chat_id, word)` — each user can bookmark each word at most once.
Managed by `AddBookmark`, `RemoveBookmark`, `IsBookmarked`, `BookmarkCount` in `vocab.go`.

#### `bot_config` (v1.22.0)
| Column | Type | Description |
|---|---|---|
| `key` | `TEXT` | Config key (primary key) |
| `value` | `TEXT` | Config value |
| `updated_at` | `DATETIME` | Last update timestamp |

Simple key-value store for runtime config overrides set via `/config`. Loaded on startup
by `LoadBotConfig()` which overwrites the corresponding global variables (env vars act as
defaults). Supported keys: `pool_target`, `pool_min`, `quiet_start`, `quiet_end`,
`tts_enabled`, `gen_spacing`, `review_batch_max`, plus per-(kind,level)
(`pool_kl_<kind>_<level>`, v1.23.6), per-kind (`pool_kind_<kind>`) and
per-level (`pool_level_<level>`) pool-size overrides (v1.23.2).

**Pool-size override resolution (v1.23.2, extended v1.23.6):** `poolTargetFor(kind, level)`
resolves the effective target via `resolvePoolTarget` with precedence **per-(kind,level) →
per-kind → per-level → global rule** (the global rule being `pool_target` at the default
level, `pool_min` elsewhere). A per-(kind,level) override (e.g. `pool_kl_word_upper-intermediate
= 400`) is the most specific and wins outright; a per-kind override (e.g. `pool_kind_story = 80`)
wins for that kind at every level lacking a per-(kind,level) entry; a per-level override (e.g.
`pool_level_advanced = 50`) applies to all kinds at that level unless a more specific override
exists. Setting a value of `0` via `/config` clears the override (key deleted). The overrides
live in the `poolKindLevelTargets` / `poolKindTargets` / `poolLevelTargets` maps, guarded by
`poolOverrideMu` because the pool-filler goroutine reads them while the admin callback writes
them.

### Legacy Migration

On first run, if a `subscribers.json` file exists (the old flat-file format), `migrateLegacyJSON` imports all active subscribers into SQLite and renames the file to `subscribers.json.migrated` so the migration only runs once.

---

## Components

### `ChangelogEntry`

```go
type ChangelogEntry struct {
    Version string
    Text    string   // Telegram HTML-formatted message
    Silent  bool     // when true, mark as seen without sending (v1.21.1)
}
```

### `Store` (SQLite wrapper)

```go
type Store struct { db *sql.DB }
```

| Method | Signature | Description |
|---|---|---|
| `openStore` | `(path string) (*Store, error)` | Opens/creates DB, applies schema, runs legacy migration |
| `Close` | `() error` | Closes the DB connection |
| `migrate` | `() error` | Applies additive column migrations to pre-existing databases |
| `columnExists` | `(table, column string) bool` | Reports whether a table has a column with the given name |
| `poolDedupIsLevelAware` | `() bool` | Reports whether content_pool's UNIQUE constraint includes the level column (v1.13.0) |
| `indexColumns` | `(index string) []string` | Returns column names in a given index (v1.13.0) |
| `migrateLegacyJSON` | `()` | Imports subscribers from the old JSON file if present |
| `AddSubscriber` | `(chatID int64) (bool, error)` | Inserts subscriber; returns `true` if newly added |
| `Subscribers` | `() ([]int64, error)` | Returns all subscribed chat IDs |
| `SentWords` | `(chatID int64) ([]string, error)` | Returns verbs already sent to a user, ordered by `sent_at` |
| `RecordSentWord` | `(chatID int64, word string) error` | Records a verb as sent; first call sets `sent_at`, every call bumps `last_sent_at` |
| `ResetSentWords` | `(chatID int64) (int64, error)` | Deletes all verb history for a user; returns count removed |
| `SentVocab` | `(chatID int64) ([]string, error)` | Returns vocabulary words already sent to a user, ordered by `sent_at` |
| `RecordSentVocab` | `(chatID int64, word string) error` | Records a vocabulary word as sent; first call sets `sent_at`, every call bumps `last_sent_at` |
| `ResetSentVocab` | `(chatID int64) (int64, error)` | Deletes all vocabulary history for a user; returns count removed |
| `MarkChangelogSeen` | `(chatID int64, version string) error` | Records that a changelog version was delivered to a user |
| `UnseenChangelogs` | `(chatID int64) ([]ChangelogEntry, error)` | Returns changelog entries not yet delivered to this user |
| `PoolCount` | `(kind, level string) (int, error)` | How many items are pooled for a kind at a level |
| `PoolTerms` | `(kind, level string) ([]string, error)` | All pooled terms at a given level (exclusion list for filler) |
| `AddToPool` | `(kind, level, term, meaning, text string) error` | Insert a generated item at a level (idempotent) |
| `PooledUnseen` | `(kind, level string, chatID int64) (term, meaning, text string, ok bool, err error)` | Random pooled item for kind+level this user hasn't seen |
| `PooledRecycled` | `(kind, level string, chatID int64) (term, meaning, text string, ok bool, err error)` | Random pooled item excluding the user's most recently served (rotation fallback, v1.13.0) |
| `PooledOldest` | `(kind, level string) (term, meaning, text string, ok bool, err error)` | Oldest pooled item for kind+level regardless of user (final safety net) |
| `recordSentFor` | `(kind string, chatID int64, term string) error` | Records a sent term in the appropriate history table; seeds SRS for words |
| `WordsSentBetween` | `(chatID int64, startUTC, endUTC string) ([]reviewItem, error)` | Words sent to a user within a UTC time range (daily review) |
| `ReviewDelivered` | `(chatID int64, reviewDate string) (bool, error)` | Whether a daily review has been delivered for a given date |
| `MarkReviewDelivered` | `(chatID int64, reviewDate string) error` | Records a daily review as delivered |
| `GetPrefs` | `(chatID int64) (UserPrefs, error)` | Returns user preferences (level, paused, interval); defaults if no row |
| `GetLevel` | `(chatID int64) string` | Returns the user's difficulty level (default intermediate) |
| `SetLevel` | `(chatID int64, level string) error` | Sets the user's difficulty level |
| `SetPaused` | `(chatID int64, paused bool) error` | Sets or clears the paused flag |
| `IsPaused` | `(chatID int64) bool` | Reports whether the user has paused scheduled sends |
| `GetInterval` | `(chatID int64) int` | Returns the user's send interval in minutes (default 30) |
| `SetInterval` | `(chatID int64, minutes int) error` | Sets the user's send interval |
| `ActiveLevels` | `() ([]string, error)` | Returns all levels in use (default + any user-selected) |
| `SeedReview` | `(chatID int64, word string, now time.Time) error` | Enrolls a word in spaced-repetition review with interval=1 |
| `DueReviews` | `(chatID int64, now time.Time, limit int) ([]dueReview, error)` | Returns words whose review is due |
| `ApplyReviewKnown` | `(chatID int64, word string, now time.Time) (bool, error)` | Promotes a word in the SRS schedule (interval grows) |
| `ApplyReviewForgot` | `(chatID int64, word string, now time.Time) (bool, error)` | Resets a word in the SRS schedule (interval back to 1) |
| `SnoozeReview` | `(chatID int64, word string, intervalDays int, now time.Time) error` | Snoozes a word so it isn't re-sent before the user answers |
| `MasteredCount` | `(chatID int64) (int, error)` | Count of words with interval >= mastered threshold (21 days) |
| `SeenWordsWithMeaning` | `(chatID int64) ([]reviewItem, error)` | User's learned words joined with pooled meanings (quiz subjects) |
| `PoolWordMeanings` | `(limit int) ([]reviewItem, error)` | Random pooled word/meaning pairs (quiz distractors) |
| `MeaningForWord` | `(word string) string` | Looks up the pooled meaning for a word |
| `RecordQuizResult` | `(chatID int64, word string, correct bool) error` | Records a quiz answer |
| `QuizStats` | `(chatID int64) (answered, correct int, err error)` | Total quiz answers and correct count for a user |
| `UserStats` | `(chatID int64) (UserStats, error)` | Computes the full stats summary for `/stats` |
| `activityDays` | `(chatID int64) (map[string]bool, error)` | Distinct active days from sent_words + sent_vocab timestamps |
| `PooledCardText` | `(word string) string` | Looks up the full card HTML text for a word from content_pool |
| `WeeklyDigestDelivered` | `(chatID int64, weekStart string) (bool, error)` | Whether a weekly digest has been delivered for a given week |
| `MarkWeeklyDigestDelivered` | `(chatID int64, weekStart string) error` | Records a weekly digest as delivered |
| `WeeklyQuizStats` | `(chatID int64, startUTC, endUTC string) (answered, correct int, err error)` | Quiz stats within a UTC time range (weekly digest) |
| `SubscriberStats` | `() (total, active, paused int)` | Aggregate subscriber counts for admin `/metrics` |
| `TotalQuizStats` | `() (answered, correct int)` | Global quiz answer tallies for admin `/metrics` |
| `TotalMasteredCount` | `() int` | Total mastered words across all users for admin `/metrics` |
| `PoolUsageLeader` | `(kind, level string) (chatID int64, seen int, ok bool, err error)` | Busiest user for a pool and how many of its current items they've seen, for admin `/poolusage` |
| `GetTTSEnabled` | `(chatID int64) bool` | Whether pronunciation audio is enabled for the user (default true) |
| `SetTTSEnabled` | `(chatID int64, enabled bool) error` | Upserts the user's TTS preference |
| `CachedAudioFileID` | `(word string) string` | Returns cached Telegram file_id for a word's pronunciation, or "" |
| `CacheAudioFileID` | `(word, fileID string) error` | Stores a Telegram file_id for a word's pronunciation (idempotent) |
| `AdminListUsers` | `(page, perPage int) ([]AdminUserRow, int, error)` | Paginated subscriber list with prefs joined (admin panel, v1.20.0) |
| `AdminUserDetail` | `(chatID int64) (AdminUserDetail, error)` | Full user detail snapshot: prefs, counts, quiz, SRS, streaks (admin panel, v1.20.0) |
| `LearnedWords` | `(chatID int64, offset, limit int) ([]LearnedWord, error)` | Paginated learned vocabulary with mastery status and bookmark flag (v1.21.0) |
| `LearnedWordsCount` | `(chatID int64) (int, error)` | Total count of learned words for a user (v1.21.0) |
| `AddBookmark` | `(chatID int64, word string) error` | Adds a bookmark for a word (idempotent, v1.21.0) |
| `RemoveBookmark` | `(chatID int64, word string) error` | Removes a bookmark for a word (v1.21.0) |
| `IsBookmarked` | `(chatID int64, word string) (bool, error)` | Reports whether a word is bookmarked by the user (v1.21.0) |
| `BookmarkCount` | `(chatID int64) (int, error)` | Total bookmarks for a user (v1.21.0) |
| `UpdatePoolText` | `(kind, level, term, meaning, text string) error` | Overwrites the text and meaning of an existing pool entry (lazy card refresh, v1.21.1) |
| `SetBotConfig` | `(key, value string) error` | Upserts a key-value pair in `bot_config` (v1.22.0) |
| `GetBotConfig` | `(key string) (string, bool)` | Reads a single config value; returns `("", false)` if not set (v1.22.0) |
| `LoadBotConfig` | `()` | Reads all `bot_config` rows and overwrites corresponding global variables (v1.22.0) |

---

### Telegram Types

```go
TelegramUpdate        — update_id, *Message, *CallbackQuery
TelegramMessage       — message_id, Chat, Text, *From
TelegramCallbackQuery — ID (string), *From, *Message, Data (string)
TelegramChat          — ID (int64)
TelegramUser          — ID (int64), Username (string)
inlineButton          — Text (string), CallbackData (string)
```

---

### Telegram Long Polling — `pollTelegramUpdates`

- Uses `getUpdates` with `timeout=30` (long poll)
- HTTP client timeout: **35 seconds** (5 s safety margin above the long-poll window)
- Tracks an `offset` to acknowledge processed updates
- On network error: sleeps 5 s then retries
- On JSON parse error: sleeps 2 s then retries
- On Telegram API error (`ok: false`): sleeps 5 s then retries
- Each received message is dispatched to `handleMessage`

---

### Command Handler — `handleMessage`

All messages use `parse_mode: HTML`.

> **Important:** When adding a new user-facing command, you must also add it to the
> `registerBotCommands()` function in `main.go`. This function calls
> `setMyCommands` at startup to register the command menu that Telegram shows
> users when they tap `/`. Forgetting this step means users won't discover the
> new command through the Telegram UI.

| Command | Behaviour |
|---|---|
| `/start` | Subscribes user (idempotent). If new: baselines all changelogs as seen, notifies maintainer, sends welcome. If returning: delivers any unseen changelogs first. Always sends welcome message. |
| `/help` | Sends usage instructions covering grammar drills, vocabulary words, grammar tips, level/interval, TTS toggle, and pause/resume. |
| `/drill` | Fires `sendChatAction(typing)`, sends a "Generating…" ack, calls `serveContent(kind=drill)`, delivers the drill. After delivery, asynchronously checks streak milestones. (v1.18.0: typing indicator + streak check) |
| `/word` | Fires `sendChatAction(typing)`, sends a "Finding…" ack, calls `serveContent(kind=word)`, delivers word card + TTS. Checks streak milestones afterward. (v1.18.0: typing indicator + streak check) |
| `/idiom` | Fires `sendChatAction(typing)`, sends a "Finding…" ack, calls `serveContent(kind=idiom)`, delivers idiom card + TTS. (v1.12.0; v1.18.0: typing indicator) |
| `/collocation` | Fires `sendChatAction(typing)`, sends a "Finding…" ack, calls `serveContent(kind=collocation)`, delivers collocation card + TTS. (v1.23.0) |
| `/story` | Fires `sendChatAction(typing)`, sends a "Writing…" ack, calls `serveContent(kind=story)`, delivers a level-matched mini story (text only, no TTS). (v1.23.0) |
| `/tip [on\|off]` | Fires `sendChatAction(typing)`. No arg: on-demand grammar tip. `on`/`off`: toggles `user_prefs.tips_enabled`. (v1.15.0; v1.18.0: typing indicator) |
| `/level [lvl]` | With an argument, sets difficulty directly; without, shows the current level + an inline keyboard (`handleLevel`). Button taps arrive as `callback_query`. (v1.4.0) |
| `/stats` | Sends a progress summary with Unicode progress bars for streak and quiz accuracy; personalised greeting from `user_prefs.first_name`. When `WEB_APP_URL` is configured, includes a `📊 Full Dashboard` inline button that opens the Telegram Mini App. (`UserStats` / `formatStats(st, firstName)`) (v1.5.0; v1.18.0: bars, greeting, mini-app button) |
| `/quiz` | Sends a native Telegram quiz poll (`sendPoll type:quiz`); Telegram highlights the correct answer after the user taps. The `poll_answer` update is routed to `handleQuizPollAnswer` which grades the result and feeds SRS. Falls back to inline keyboard if `sendPoll` fails. (v1.9.0; v1.18.0: native polls) |
| `/pause` | Sets `user_prefs.paused = 1`; scheduled sends are skipped, on-demand still works. (v1.4.0) |
| `/resume` | Clears the paused flag. (v1.4.0) |
| `/interval [min]` | With a numeric argument, sets the send interval directly; without, shows the current interval + an inline keyboard of options (`handleInterval`). Allowed: 30/60/120/180/240/360/480/720 min. (v1.6.0) |
| `/setup` | Daily time-budget quick-setup. Shows five presets (5 / 15 / 30 / 60 / 120 min per day); tapping one sets interval, quiz frequency, and which daily features are on/off in one tap. Button taps use the `setup:` callback prefix. Replaces the selection message with the full settings hub so users can see what changed. (v1.17.0) |
| `/settings` | Shows all per-user settings in one inline-keyboard hub. Tap any button to toggle a feature or open a level/interval/quiz-interval sub-keyboard. All individual setting commands still work as shortcuts. Button taps use the `settings:` callback prefix. (v1.17.0) |
| `/tts [on/off]` | With `on`/`off`, toggles pronunciation audio in `user_prefs.tts_enabled`; without an argument, shows current TTS status. (v1.14.0) |
| `/metrics` | *(Admin only)* Subscriber stats (total/active/paused), pool depth per kind+level, quiz volume, mastered count. Gated by `MAINTAINER_CHAT_ID`. (v1.10.0) |
| `/poolusage` | *(Admin only)* Per (kind, level), finds the single most active user — the one who has seen the most of the items currently pooled — and reports that user's consumption as a percentage of the pool size (`seen/count`). Surfaces pools nearing exhaustion so the target can be raised via `/config` before users see repeats. Gated by `MAINTAINER_CHAT_ID`. (v1.23.5) |
| `/announce <text>` | *(Admin only)* Push a one-off HTML message to all non-paused subscribers. Gated by `MAINTAINER_CHAT_ID`. (v1.10.0) |
| `/health` | *(Admin only)* Quick check: enabled AI providers and their count. Gated by `MAINTAINER_CHAT_ID`. (v1.10.0) |
| `/users` | *(Admin only)* Paginated user list with inline keyboard navigation; tap any user for full detail (settings, toggles, progress, quiz, SRS, streaks); send a direct message to any user. Gated by `MAINTAINER_CHAT_ID`. (v1.20.0) |
| `/backup` | *(Admin only)* Creates a point-in-time SQLite snapshot (`VACUUM INTO`) and sends the `.sqlite` file to maintainer chat. Also runs nightly via `BACKUP_TIME`. Gated by `MAINTAINER_CHAT_ID`. |
| `/config` | *(Admin only)* Interactive inline-keyboard panel to tweak runtime bot settings: pool target/min, **per-(kind, level)** (v1.23.6), **per-kind and per-level pool-size overrides** (v1.23.2), quiet hours, global TTS, gen spacing, review batch max. Changes are persisted in `bot_config` table and survive restarts. Callbacks use the `cfg:` prefix (`cfg:kl:<kind>:<level>:<n>`, `cfg:pk:<kind>:<n>`, `cfg:pl:<level>:<n>`; `n=0` clears). Gated by `MAINTAINER_CHAT_ID`. (v1.22.0) |
| `/admin` | *(Admin only)* Lists every maintainer command (they're hidden from the public `/help` and the Telegram command menu). Gated by `MAINTAINER_CHAT_ID`. (v1.23.6) |
| `/mywords` | Browse all learned vocabulary with mastery status (🟢 mastered / 🔵 learning / ⚪ new) and bookmark indicator (⭐). Paginated with inline buttons (`mywords:<page>`). `/mywords bookmarks` shows bookmarked words only (paginated via `mybm:<page>`). (v1.21.0) |
| `/bookmark [word]` | Toggle a word's bookmark status. With a word argument, adds or removes the bookmark; without an argument, shows the bookmarks page (aliases to `/mywords bookmarks`). Bookmark buttons (`bookmark:add:<word>` / `bookmark:rm:<word>`) also appear on vocabulary word cards. (v1.21.0) |
| `/reset` | Clears both verb (`ResetSentWords`) and vocabulary (`ResetSentVocab`) history; reports how many of each were cleared. |
| *(unknown command)* | Replies with a prompt to use /drill, /word, /tip, /quiz, /tts, /stats or /help — or to send any word to look it up. |
| *(empty text)* | Silently ignored. |

---

### Per-User Hourly Rate Limiter — `globalHourlyLimiter` (v1.17.0)

A shared in-memory rate limiter (`hourlyLimiter` in `schedule.go`) prevents
multiple schedulers from stacking messages on the same user in the same time
window. The window size equals the user's chosen broadcast interval (default 60 min)
so a 30-min user still receives two broadcast slots per hour while a 60-min user
receives one.

**How it works:** `claimSlot(chatID, now, intervalMinutes)` divides
`minutesSinceMidnight` by the user's interval to get a slot number, then records
`(chatID, date+slot)` in a map. The first scheduler to call `claimSlot` for a
given user-slot wins and sends; all others skip silently.

**Priority rules:**
- **Broadcasts, quiz, idiom, tip, collocation, mini story, daily review, weekly digest** — all subject to
  the rate limiter; they compete for the user's current interval slot.
- **SRS word reviews** — **exempt** from the rate limiter. They send independently
  (max 1 card per sweep, controlled by `REVIEW_BATCH_MAX`) so they never block or
  get blocked by broadcasts. A user can receive 1 SRS card plus 1 primary message
  in the same interval window.

State is in-memory; a process restart resets it (at most one extra message at the
first slot after restart — acceptable).

---

### Native Quiz Polls — `sendQuizAsPoll` / `handleQuizPollAnswer` (v1.18.0)

Quizzes are delivered via Telegram's `sendPoll` API with `type: quiz`. Telegram
natively highlights the correct answer after the user taps, shows an explanation
(the word + its meaning), and animates the result — no custom UI needed.

**Poll registry:** because `poll_answer` updates arrive asynchronously (separate
from the message flow), the bot keeps an in-memory map `pendingQuizPolls`
(`map[string]pendingQuizPoll`) keyed by Telegram's `poll_id`. Each entry stores
`{chatID, word, correctIdx}`. `storePendingPoll` writes to the map;
`popPendingPoll` reads and deletes atomically under a mutex.

**Grading:** when Telegram sends a `poll_answer` update,
`handleQuizPollAnswer` pops the entry, compares `option_ids[0]` against the
stored `correctIdx`, records the result via `RecordQuizResult`, and calls
`ApplyReviewKnown` or `ApplyReviewForgot` to advance the SRS schedule.

**Fallback:** if `sendPoll` returns an error (e.g. the bot isn't configured for
polls), `sendQuizAsPoll` falls back transparently to `SendKeyboard` with the
inline button quiz.

---

### Streak Milestone Celebrations — `checkStreakCelebration` (v1.18.0)

After any `/drill` or `/word` delivery, `checkStreakCelebration` is called
asynchronously (`go checkStreakCelebration(...)`). It:

1. Loads `UserStats` to get `CurrentStreak`.
2. Finds the highest milestone in `{3, 7, 14, 30, 60}` that the streak has
   reached.
3. Compares against `user_prefs.streak_celebrated` (the last celebrated
   milestone). If the current milestone is higher, sends a congratulatory
   message and updates the column.

The column prevents the same celebration from firing twice. Milestone messages
use the user's `first_name` (from `user_prefs`) or fall back to "friend".

---

### Persistent Reply Keyboard (v1.18.0)

On `/start`, `SendWithReplyKeyboard` sends the welcome message with a
`ReplyKeyboardMarkup` (`resize_keyboard: true`, `is_persistent: true`) containing
four buttons:

```
[ 📘 Word ]  [ 🎯 Drill ]
[ 🧩 Quiz ]  [ 📊 Stats ]
```

At the top of `handleMessage`, button texts are mapped to their slash command
equivalents before the switch statement, so the rest of the router sees
`/word`, `/drill`, `/quiz`, `/stats` regardless of how the user triggered the
command.

---

### Telegram Mini App — `webapp.go` (v1.18.0, optional)

When `WEB_APP_URL` is set, `startWebServer` launches an HTTP server on
`WEB_APP_PORT` (default `8090`) with two routes:

- `GET /stats` — serves the single-page HTML app (`statsPageHTML`)
- `GET /api/stats?initData=...` — validates Telegram `initData` (HMAC-SHA256),
  returns a JSON stats object

**initData validation** (`validateInitData`): parses the URL-encoded string,
sorts all fields except `hash` into a `key=value\n` check string, computes
`HMAC-SHA256(HMAC-SHA256("WebAppData", bot_token), check_string)`, and compares
to the `hash` field. Rejects the request on mismatch.

**Stats JSON** includes `current_streak`, `longest_streak`, `words`, `mastered`,
`verbs`, `quiz_answered`, `quiz_correct`, `quiz_pct`, `active_days`,
`activity_days` (sorted `"YYYY-MM-DD"` strings), and `level`.

**HTML page** (`statsPageHTML`) uses the Telegram WebApp JS SDK, calls
`/api/stats`, and renders stat cards with Unicode progress bars and a Chart.js
30-day activity bar chart. Colours adapt to Telegram's theme CSS variables
(`--tg-theme-bg-color`, `--tg-theme-button-color`, etc.).

---

### Broadcast Scheduler — `broadcastSweep`

The broadcast scheduler (`runBroadcastScheduler`) sleeps until the next half-hour
boundary (`:00` or `:30`) then runs a per-user delivery sweep. Each subscriber
receives content only on slots aligned to their chosen interval (default 60 min),
alternating drill/word via `dueAndKind` (wall-clock aligned so restarts and
quiet-hour skips never desync):

```
slot fires (every 30 min, the base tick)
  └─ broadcastSweep(now)
       └─ skip if isQuietHours(now)
       └─ store.Subscribers()              → list of chat IDs
       └─ for each chatID:
            ├─ skip if prefs.Paused
            ├─ skip if not dueAndKind(now, prefs.Interval)
            ├─ skip if globalHourlyLimiter.claimSlot fails (rate-limited)
            ├─ sendPendingChangelogs(chatID)
            └─ serveContent(kind, prefs.Level, allowGenerate=false)
                 └─ pool-first lookup (no inline AI call)
                 └─ sendToTelegram(chatID, text)
```

The content kind (drill vs word) is per-user, derived from the wall-clock slot
index parity (`minutesSinceMidnight / interval`), so every user keeps alternating
regardless of their chosen interval. Default interval is 60 min (1 broadcast/hour).

---

### Changelog Delivery — `sendPendingChangelogs`

Queries `UnseenChangelogs` for the user, then for each unseen entry:
1. Sends the message via `sendToTelegram`
2. Only calls `MarkChangelogSeen` after a successful send — so a delivery failure does not suppress future retry attempts

---

### AI Drill Generation — `generateContent(kind=drill)`

**Model:** whichever provider wins the fallback chain (default Gemini `gemini-2.5-flash`).

**Retry policy:** 2 attempts per provider with exponential backoff (2 s → 4 s); rate-limit (429) causes immediate failover to the next provider. Respects context cancellation between retries.

**Output format:** Telegram HTML (`parse_mode: HTML`). The prompt instructs the model to wrap label lines in `<b>` tags and bold the target verb form inside each example sentence. The 21 forms are emitted in a fixed order so the drill can be paginated (see **Paged delivery** below).

**Forms covered (21 total):**

Ordered so page 1 leads with the forms learners use most (v1.12.0):

| # | Form | Use | Page |
|---|---|---|---|
| 1 | Simple Present | Routine / Habit | Everyday Essentials |
| 2 | Present Continuous | Right Now / Temporary | Everyday Essentials |
| 3 | Simple Past | Finished Action | Everyday Essentials |
| 4 | Future: will | Prediction / Spontaneous Decision | Everyday Essentials |
| 5 | Present Perfect | Experience / Recent Result | Perfect Tenses |
| 6 | Present Perfect Continuous | Ongoing Until Now | Perfect Tenses |
| 7 | Past Perfect | Before Another Past Event | Perfect Tenses |
| 8 | Past Perfect Continuous | Duration Before a Past Event | Perfect Tenses |
| 9 | Past Continuous | Was in Progress | More Past and Future |
| 10 | Future: be going to | Plan / Intention | More Past and Future |
| 11 | Future Continuous | In Progress at a Future Moment | More Past and Future |
| 12 | Future Perfect | Completed by a Future Point | More Past and Future |
| 13 | Future Perfect Continuous | Duration up to a Future Point | More Past and Future |
| 14 | Zero Conditional | General Truth / Always Result | Conditionals |
| 15 | First Conditional | Real Future Possibility | Conditionals |
| 16 | Second Conditional | Unreal / Hypothetical Present | Conditionals |
| 17 | Third Conditional | Unreal Past / Regret | Conditionals |
| 18 | Modal Verb | Advice / Obligation (should / must) | Modals and More |
| 19 | Passive Voice | Focus on the Receiver | Modals and More |
| 20 | Imperative | Command / Instruction | Modals and More |
| 21 | Used to | Past Habit (no longer true) | Modals and More |

**Paged delivery (v1.11.0):** A drill is too long for one comfortable message, so it is sent as **five themed pages** with `◀️ Back` / `Next ▶️` inline buttons. The full drill text is stored once in `content_pool`; the bot sends page 1 via `SendKeyboard`, and a button tap (callback data `drill:<page>:<verb>`) reloads the drill by verb with `Store.DrillText`, re-renders the requested page, and updates the message in place with `editMessageText`. Paging is therefore stateless — no per-message storage. The page split is driven by `drillPageGroups` (`generation.go`); `renderDrillPage` and `drillNavKeyboard` build each page and its buttons. Drills that can't be parsed into numbered forms (e.g. legacy pool rows) degrade to a single plain message.

**Exclusion clause:** If the user has prior verb history, the prompt appends an explicit list of already-practiced verbs with an instruction to choose something different.

**Return values:** `(text, term, meaning, provider string, err error)` (meaning is empty for drills).

---

### AI Vocabulary Generation — `generateContent(kind=word)`

**Model:** whichever provider wins the fallback chain (default Gemini `gemini-2.5-flash`).

**Retry policy:** 2 attempts per provider with exponential backoff (2 s → 4 s); rate-limit (429) causes immediate failover. Respects context cancellation between retries.

**Difficulty target:** determined by the user's level (beginner / intermediate / upper-intermediate / advanced).

**Output format:** Telegram HTML (`parse_mode: HTML`, `<b>`, `<i>`, and `<tg-spoiler>` tags). The card structure is:

```
📘 Word of the Session: {WORD}
————————————————————
💬 Meaning            — one simple sentence
🔊 Pronunciation      — syllable spelling · IPA
✅ Synonyms           — 3–5, comma-separated
⛔ Opposites          — 2–4, comma-separated
📝 Examples           — two example sentences (target word bolded)
🇮🇷 Persian            — short Farsi translation (hidden behind spoiler, tap to reveal)
💡 (read-it-aloud nudge)
```

**Exclusion clause:** If the user has prior vocabulary history, the prompt appends an explicit list of already-sent words with an instruction to choose something different.

**Return values:** `(text, term, meaning, provider string, err error)`.

---

### AI Idiom Generation — `generateContent(kind=idiom)` (v1.12.0)

**Model / retry / difficulty:** same as the other kinds (provider fallback chain, 2 attempts with backoff, level-targeted).

**Output format:** Telegram HTML (`<b>`/`<i>` only). The card structure is:

```
🗣️ Idiom of the Day: {IDIOM}
————————————————————
💬 Meaning        — one simple sentence
📝 Examples       — two example sentences (idiom bolded)
🌍 When to use    — tone/context note
💡 (try-it nudge)
```

**Term parsing:** `parseIdiom` keeps the **whole phrase** after `Idiom of the Day:` (idioms are multi-word, unlike `parseVerb`/`parseWord`).

**Exclusion clause:** appends already-sent idioms (per user) with an instruction to pick a different one.

**Delivery:** on demand via `/idiom`, and once daily to all active subscribers via the idiom scheduler (below). Pooled like words/drills; sent history lives in `sent_idioms`. Idioms are not enrolled in spaced repetition.

---

### AI Collocation Generation — `generateContent(kind=collocation)` (v1.23.0)

**Model / retry / difficulty:** same as the other kinds (provider fallback chain, level-targeted via `levelInstruction`).

**Output format:** Telegram HTML (`<b>`/`<i>` only). The card structure is:

```
🔗 Collocation of the Day: {COLLOCATION}
————————————————————
💬 Meaning        — one simple sentence
📝 Examples       — two example sentences (collocation bolded)
⚠️ Watch out      — ❌ common wrong combination → ✅ correct collocation
💡 (say-it-aloud nudge)
```

**Term parsing:** `parseCollocation` keeps the **whole phrase** after `Collocation of the Day:` via the shared `parseLabeledPhrase` helper (which `parseIdiom` also uses).

**Exclusion clause:** appends already-pooled collocations with an instruction to pick a different one.

**Delivery:** on demand via `/collocation`, and once daily via the collocation scheduler (`COLLOCATION_TIME`, default 13:00). Pooled per level; sent history lives in `sent_collocations`. Collocation cards get a best-effort pronunciation voice note like idioms (`extractTTSTerm` falls back to `parseCollocation`).

---

### AI Mini Story Generation — `generateContent(kind=story)` (v1.23.0)

**Model / retry / difficulty:** provider fallback chain with a dedicated `storyLevelInstruction` that scales story length and complexity per CEFR band (≈60–80 words for beginner up to ≈180–220 words for advanced).

**Output format:** Telegram HTML (`<b>`/`<i>` only). The card structure is:

```
📖 Mini Story: {TITLE}
————————————————————
{2–4 short paragraphs; 3 key vocabulary items bolded in the text}
🔑 Key Vocabulary — 3 bolded terms from the story with short meanings
🤔 Think about it — one comprehension question
💡 (read-aloud / retell nudge)
```

**Term parsing:** `parseStoryTitle` keeps the whole title after `Mini Story:` via `parseLabeledPhrase`; the title is the pool/history key.

**Exclusion clause:** appends already-sent story titles with an instruction to write a different story.

**Delivery:** on demand via `/story`, and once daily via the story scheduler (`STORY_TIME`, default 17:00). Pooled per level; sent history lives in `sent_stories`. Stories are sent as plain text (no TTS — they exceed the TTS length cap by design).

---

### Content Delivery — `serveContent`

`serveContent` is the single read-path for both on-demand commands and broadcasts.
It returns ready-to-send text and the resolved term for a user, recording the chosen term:

1. Tries an unseen pooled item at the user's level (`PooledUnseen`, randomized).
2. On a miss with `allowGenerate=true` (on-demand `/drill`, `/word`): generates
   inline via `generateContent` → `ProviderChain.Generate`, adds the result to the
   pool, and serves it.
3. On a miss with `allowGenerate=false` (broadcasts): the user has seen the whole
   pool, so `maybeNotifyPoolExhausted` alerts the maintainer (deduped via
   `pool_exhaustion_notice`, v1.23.4), then it rotates through the user's history
   via `PooledRecycled` (random item excluding the most recently served), so no item
   is ever repeated back-to-back. Falls back to `PooledOldest` only as a final safety
   net (e.g. single-item pool). `serveContent` takes a `Notifier` for this alert;
   on-demand callers never reach this branch (they generate instead).
4. For word cards (kind=word): after serving a pooled item, `maybeRefreshCard` checks
   if the card is missing sections that current prompts produce (e.g. Persian
   definition). If stale, a background goroutine regenerates the card via
   `generateWordFor` and updates the pool entry via `UpdatePoolText`. The old card is
   served immediately; the refreshed version is used on the next request. (v1.21.1)

`generateContent` builds the appropriate prompt (`buildDrillPrompt` / `buildWordPrompt`),
calls the provider chain, and parses the term (and meaning, for words) from the output.

---

### Term Extraction — `parseVerb` / `parseWord`

Both delegate to a shared helper `parseLabeledTerm(text, label)`:
- `parseVerb` scans for the `Verb of the Session:` line
- `parseWord` scans for the `Word of the Session:` line

The helper strips any trailing HTML tags (e.g. `</b>`), strips Markdown punctuation, extracts the first token, and lowercases it. The HTML-aware stripping truncates everything from the first `<` character so terms ending in `b` (e.g. `grab`) are not corrupted by the closing tag trim.

---

### Telegram Sender — `sendToTelegram`

Posts to `sendMessage` with `parse_mode: HTML`. Returns an error if the HTTP response is not 200 OK. Most callers discard the return error with `_` (best-effort delivery); failures are logged. Additional helpers: `sendToTelegramWithKeyboard` (inline keyboard), `editMessageText`, `answerCallbackQuery`.

---

## Data Flow: Scheduled Broadcast

```
half-hour slot fires (runBroadcastScheduler)
  └─ skip if isQuietHours(now)
  └─ broadcastSweep(now)
       └─ store.Subscribers()              → list of chat IDs
       └─ for each chatID:
            ├─ skip if prefs.Paused
            ├─ skip if not dueAndKind(now, prefs.Interval)
            ├─ sendPendingChangelogs(chatID)
            │    └─ store.UnseenChangelogs(chatID)    → unseen entries
            │    └─ for each entry:
            │         └─ sendToTelegram(chatID, entry.Text)
            │         └─ store.MarkChangelogSeen(chatID, entry.Version)
            │
             └─ serveContent(kind, prefs.Level, allowGenerate=false)
                  └─ store.PooledUnseen(kind, level, chatID) → pooled item (random)
                  └─ (fallback) store.PooledRecycled(kind, level, chatID) → rotation
                  └─ (safety net) store.PooledOldest(kind, level)
                  └─ store.recordSentFor(kind, chatID, term)
                  └─ sendToTelegram(chatID, text)
```

---

## Data Flow: /drill & /word Commands

```
user sends /drill                      user sends /word
  └─ sendToTelegram("Generating…")        └─ sendToTelegram("Finding a word…")
  └─ serveContent(drill, level, true)     └─ serveContent(word, level, true)
       └─ pool-first, inline-gen on miss       └─ pool-first, inline-gen on miss
  └─ sendDrill(chatID, text)              └─ sendToTelegram(chatID, text)
       └─ renderDrillPage(text, 1)
       └─ SendKeyboard(page 1 + ◀️/▶️)

later: user taps ◀️/▶️  →  callback "drill:<page>:<verb>"
  └─ handleDrillCallback
       └─ store.DrillText(verb) → full drill text
       └─ renderDrillPage(text, page)
       └─ editMessageText(page + updated buttons)
```

---

## Startup Sequence

```
1. Load env vars (token, API key, maintainer ID)
2. Load timezone (Asia/Tehran by default)
3. Build AI provider chain (Gemini + any configured OpenAI-compatible backends)
4. Open SQLite store (apply schema, run migrations, run legacy JSON migration if needed)
5. Load persisted admin config overrides from `bot_config` table (v1.22.0)
6. Start pool filler goroutine (background content generation)
7. Start broadcast scheduler goroutine (half-hourly, per-user interval + quiet-hour aware)
8. Start daily review scheduler goroutine (fires at local midnight)
9. Start spaced-repetition review scheduler goroutine (hourly)
10. Start quiz scheduler goroutine (every QUIZ_INTERVAL)
11. Start weekly digest scheduler goroutine (fires weekly, default Sunday 20:00)
12. Start idiom-of-the-day scheduler goroutine (fires daily at IDIOM_TIME, default 09:00)
13. Start nightly backup scheduler goroutine (fires daily at BACKUP_TIME, default 02:00)
14. Start collocation-of-the-day scheduler goroutine (fires daily at COLLOCATION_TIME, default 13:00)
15. Start mini story scheduler goroutine (fires daily at STORY_TIME, default 17:00)
16. Start Telegram long-poll goroutine
17. Block on OS signal (SIGINT / SIGTERM)
18. On signal: cancel context → goroutines exit, deferred store.Close() runs
```

---

## Dependencies

| Package | Purpose |
|---|---|
| `google.golang.org/genai` | Google Gemini AI client |
| `modernc.org/sqlite` | Pure-Go SQLite driver |
| `database/sql` | Standard Go SQL interface |
| `net/http` | Telegram Bot API HTTP calls |
| `encoding/json` | Telegram API payload marshaling |

---

# Roadmap — Planned Architecture (v2)

> **Status: IMPLEMENTED in v1.3.0.** Changes A, B and C below all shipped. The
> design spec is retained for reference; the implementation lives in `config.go`,
> `providers.go`, `generation.go`, `pool.go` and `schedule.go`, wired together from
> `main.go`. The three changes work together but remain independently configurable.

## Motivation

The single Gemini free tier rate-limits us. Two problems:

1. **No redundancy** — when Gemini returns a 429 / 5xx / timeout, generation fails outright.
2. **Generation is on the user's critical path** — every `/drill`, `/word` and broadcast
   triggers a live API call, so request volume scales with user count and bursts at
   each broadcast tick.

The v2 plan addresses both:

- **Change A — Multi-provider fallback:** try several free AI providers in order; the
  first that responds wins. Removes the single point of failure and multiplies the
  effective free quota.
- **Change B — Decoupled generation pool:** move all AI calls off the request path into
  a background worker that keeps a pre-generated pool of drills and words topped up.
  User requests and broadcasts only ever read from the pool.
- **Change C — Quiet hours + daily review:** go silent overnight in Tehran time
  (00:00–09:00) and, at midnight, send each user a compact recap of the day's words
  (word + meaning only).

---

## Change A — Multi-Provider Fallback

### Provider abstraction

Introduce a single interface that all providers implement:

```go
// Provider generates drill/word text from a prompt. Implementations wrap a
// specific AI backend. Generate returns the raw model text (HTML drill/card).
type Provider interface {
    Name() string
    Enabled() bool                                   // true only if its API key/config is set
    Generate(ctx context.Context, prompt string) (string, error)
}
```

Two concrete implementations cover all six backends:

1. **`GeminiProvider`** — wraps the existing `google.golang.org/genai` SDK.
2. **`OpenAICompatProvider`** — a generic client for any backend exposing the
   OpenAI `POST /chat/completions` schema. Configured per-provider with a base URL,
   model name, and bearer token. Groq, OpenRouter, Cerebras, GitHub Models,
   Cloudflare Workers AI and Mistral all reuse this one implementation.

### Provider registry & ordering

A `ProviderChain` holds an ordered slice of providers and is built once at startup.
Only providers whose key/config env vars are present are included (`Enabled()` gate);
the rest are skipped and logged. Recommended default order (fastest/most-generous
free tiers first):

```
Gemini -> Groq -> Cerebras -> OpenRouter -> GitHub Models -> Cloudflare -> Mistral -> Gemini2 -> SambaNova -> Cohere
```

### Fallback algorithm

```
generateWithFallback(ctx, prompt):
  for each provider in chain (in order):
      if not provider.Enabled(): continue
      for attempt in 1..N (per-provider retry w/ exponential backoff):
          text, err = provider.Generate(ctx, prompt)
          if err == nil and text non-empty: return text, provider.Name()
          if ctx cancelled: return ctx.Err()
          backoff
      log "provider X exhausted, falling through to next"
  return error "all providers failed"
```

- Per-provider retry budget is small (e.g. 2 attempts) so we **fail over fast** rather
  than burning time on one dead provider.
- A `429`/quota error should immediately fall through to the next provider (no retry).
- The winning provider name is logged and (optionally) stored on the pooled item for
  observability.

### Provider configuration

Each provider is enabled purely by setting its env var(s). All are optional except
that **at least one** must be configured.

| Provider | Env var(s) | Base URL | Default model |
|---|---|---|---|
| Gemini | `GEMINI_API_KEY` | (native SDK) | `gemini-2.5-flash` |
| Groq | `GROQ_API_KEY` | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile` |
| Cerebras | `CEREBRAS_API_KEY` | `https://api.cerebras.ai/v1` | `llama-3.3-70b` |
| OpenRouter | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | `meta-llama/llama-3.3-70b-instruct:free` |
| GitHub Models | `GITHUB_MODELS_TOKEN` | `https://models.github.ai/inference` | `openai/gpt-4o-mini` |
| Cloudflare Workers AI | `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` | `https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/v1` | `@cf/meta/llama-3.1-8b-instruct` |
| Mistral | `MISTRAL_API_KEY` | `https://api.mistral.ai/v1` | `mistral-small-latest` |
| Gemini2 | `GEMINI_API_KEY` (reused) | `https://generativelanguage.googleapis.com/v1beta/openai` | `gemini-2.0-flash` |
| SambaNova | `SAMBANOVA_API_KEY` | `https://api.sambanova.ai/v1` | `Meta-Llama-3.3-70B-Instruct` |
| Cohere | `COHERE_API_KEY` | `https://api.cohere.ai/compatibility/v1` | `command-r` |

Optional overrides (per provider): `<PROVIDER>_MODEL` to swap the model without code
changes, and `AI_PROVIDER_ORDER` (comma-separated names) to reorder/disable the chain.

### Prompt compatibility note

The current prompts already produce Telegram-HTML output and work with any
instruction-following chat model, so they are reused unchanged. The only adaptation is
mapping our single prompt string onto the OpenAI `messages` array
(`[{role:"user", content: prompt}]`) for the compatible providers.

---

## Change B — Decoupled Generation Pool

Before v2, inline generation called the AI on the user's critical path. v2 moved
all generation into a background worker (`poolFiller` in `pool.go`).

### New component — Pool Filler goroutine

A long-running goroutine started in `main()`:

```
poolFiller(ctx, chain, store):
  ticker every REFILL_INTERVAL (e.g. 20s)
  on each tick:
    levels = store.ActiveLevels()             // default + any user-selected
    for kind in {drill, word}:
      for level in levels:
          n = store.PoolCount(kind, level)
          target = poolTargetFor(level)       // full target for default, POOL_MIN for others
          if n >= target: continue
          exclude = store.PoolTerms(kind)     // all terms across levels (global dedup)
          text, term, meaning, provider = generateContent(ctx, chain, kind, level, exclude)
          store.AddToPool(kind, level, term, meaning, text)
          sleep GEN_SPACING                   // throttle to respect rate limits
```

- Generation is **paced** (`GEN_SPACING`, e.g. 1 call / few seconds) so we never burst
  the providers — the opposite of today's per-subscriber burst at broadcast time.
- The pool is shared across all users; per-user "seen" history still personalises which
  pooled item each user receives.

### `content_pool` table (new)

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK AUTOINCREMENT` | Pool item id |
| `kind` | `TEXT` | `drill` or `word` |
| `term` | `TEXT` | The verb/word, lowercased |
| `text` | `TEXT` | Full Telegram-HTML message |
| `meaning` | `TEXT` | One-line meaning, parsed from the card's Meaning line (used by the daily review; empty for drills) |
| `level` | `TEXT` | Difficulty: `beginner` / `intermediate` / `upper-intermediate` / `advanced` (default `intermediate`) |
| `created_at` | `DATETIME` | Generation time |

`UNIQUE(kind, level, term)` keeps the pool free of duplicates per kind and level;
index `idx_content_pool_kind` on `(kind, level, created_at)`.

### New Store methods

| Method | Description |
|---|---|
| `PoolCount(kind, level) (int, error)` | How many items are pooled for a kind at a level |
| `PoolTerms(kind, level) ([]string, error)` | All pooled terms at a given level (exclusion list for the filler) |
| `AddToPool(kind, level, term, meaning, text) error` | Insert a generated item at a level (idempotent) |
| `PooledUnseen(kind, level, chatID) (term, meaning, text, ok, err)` | Random pooled item for kind+level this user hasn't seen |
| `PooledOldest(kind, level) (term, meaning, text, ok, err)` | Oldest pooled item for kind+level regardless of user (broadcast fallback) |
| `recordSentFor(kind, chatID, term) error` | Records a sent term in the appropriate history table; seeds SRS for words |

### Read path (request / broadcast)

```
serveContent(ctx, chain, store, chatID, kind, level, allowGenerate):
  term, _, text, found = store.PooledUnseen(kind, level, chatID)
  if found:
      store.recordSentFor(kind, chatID, term)
      return text                              // ZERO AI calls

  if allowGenerate:                            // on-demand /drill, /word
      text, term, meaning, _ = generateContent(ctx, chain, kind, level, exclude)
      store.AddToPool(kind, level, term, meaning, text)
      store.recordSentFor(kind, chatID, term)
      return text                              // inline generation fallback

  // broadcast fallback: no unseen item, rotate through history (random,
-  // excluding the most recently served item to avoid back-to-back repeats)
-  term, _, text, found = store.PooledRecycled(kind, level, chatID)
-  if found:
-      store.recordSentFor(kind, chatID, term)
-      return text
-  // final safety net: reuse the oldest pooled item at this level
   term, _, text, found = store.PooledOldest(kind, level)
   if found:
       store.recordSentFor(kind, chatID, term)
       return text
   return error "pool empty"
```

- Broadcasts call `serveContent` with `allowGenerate=false` — **no broadcast ever
  blocks on an AI call**.
- On-demand commands call with `allowGenerate=true` — they can generate inline if
  the pool has no unseen items for that user.

### Tuning knobs (env vars)

| Variable | Default | Description |
|---|---|---|
| `POOL_TARGET` | `300` | Desired pooled items per kind |
| `POOL_MIN` | `100` | Pool target for non-default levels (default level uses `POOL_TARGET`) |
| `REFILL_INTERVAL` | `20s` | How often the filler checks the pool |
| `GEN_SPACING` | `3s` | Minimum gap between successive AI calls |

---

## Change C — Quiet Hours + Daily Review (Tehran time)

Users are asleep overnight, so the bot must go silent and instead send a single
end-of-day recap.

### Timezone

All scheduling decisions use **Tehran local time** (`Asia/Tehran`, currently UTC+3:30,
no DST). The zone is loaded once at startup via `time.LoadLocation` and every
"current time" check converts `time.Now()` into that location. Configurable via
`TIMEZONE` (default `Asia/Tehran`).

### Quiet window — 00:00 to 09:00

No content of any kind (drills, words, changelogs, new-user welcomes are still allowed)
is broadcast while the Tehran local hour is in `[00:00, 09:00)`.

- A helper `isQuietHours(now)` returns true when `00:00 <= localTime < 09:00`.
- The 30-minute broadcast ticker checks `isQuietHours` at the top of each tick and
  **skips the send** if quiet, logging that it was suppressed.
- On-demand commands (`/drill`, `/word`) are **not** suppressed — a user who explicitly
  asks during the night still gets a reply (it's pulled from the pool, so it's cheap).
- Window bounds are configurable via `QUIET_START` (default `00:00`) and `QUIET_END`
  (default `09:00`).

> **Interaction with the alternating drill/word ticker:** because some ticks are
> skipped overnight, the drill/word choice must NOT rely on a naive in-memory toggle
> (it would desync). v2 derives the content type from the tick's wall-clock slot
> (e.g. even/odd 30-min slot of the day in Tehran time) so the alternation stays
> deterministic across skipped ticks and restarts.

### Daily review — fired at 00:00 Tehran

Exactly at midnight Tehran (the moment quiet hours begin), every subscriber receives a
recap of all **vocabulary words** delivered to them during the day that just ended —
**word + one-line meaning only**, with no IPA, synonyms, opposites or examples.

Example review message:

```
🌙 <b>Today's Words — Review before bed</b>

• <b>vigorously</b> — done with great energy or force
• <b>reluctant</b> — unwilling and hesitant to do something
• <b>tedious</b> — too long, slow, or dull; boring

😴 Sleep well — say each one aloud once more before you do!
```

(The review covers vocabulary only, per requirement — grammar drills are not recapped.)

### How the review is assembled

1. A dedicated `dailyReviewScheduler` goroutine computes the duration until the next
   00:00 Tehran, sleeps until then, fires the review for all subscribers, then
   reschedules for the following midnight (robust to restarts — it always targets the
   next midnight).
2. Per subscriber, query the words sent to them **since the previous midnight**:
   join `sent_vocab` (rows with `sent_at` in the just-finished Tehran day) against
   `content_pool` to fetch each word's stored `meaning`.
3. If a user received no words that day, skip them (no empty review).
4. Format the compact list and send one message via `sendToTelegram`.

### Idempotency

A `daily_review_delivery(chat_id, review_date, sent_at)` table (PK `(chat_id, review_date)`)
records that a given day's review was delivered, so a restart near midnight cannot
double-send. `review_date` is the Tehran calendar date being recapped.

### New config (env vars)

| Variable | Default | Description |
|---|---|---|
| `TIMEZONE` | `Asia/Tehran` | IANA zone for all scheduling |
| `QUIET_START` | `00:00` | Start of the no-broadcast window (local) |
| `QUIET_END` | `09:00` | End of the no-broadcast window (local) |

---

## Rollout / Migration Plan

1. **Schema:** add `content_pool` (with `meaning` column) and `daily_review_delivery`
   in `openStore` (additive `CREATE TABLE IF NOT EXISTS` — no migration of existing
   tables needed; existing `subscribers`, `sent_words`, `sent_vocab`,
   `changelog_delivery` are untouched).
2. **Providers (Change A):** introduce the `Provider` interface, `GeminiProvider`,
   `OpenAICompatProvider`, and `ProviderChain`; replace direct Gemini SDK calls
   with `chain.Generate` (multi-provider fallback).
   This alone restores reliability and can ship first.
3. **Pool filler (Change B):** add the `content_pool` Store methods and the `poolFiller`
   goroutine; switch the read path to `serveContent`. Ship after Change A is verified.
4. **Quiet hours + review (Change C):** load the timezone at startup; gate the broadcast
   ticker with `isQuietHours`; derive drill/word alternation from the wall-clock slot;
   add the `dailyReviewScheduler` goroutine and `daily_review_delivery` table. Ship
   independently of A/B (only depends on the `meaning` column if the review is to show
   meanings — otherwise it can show words alone).
5. **Backwards compatibility:** if no extra provider keys are set, the chain is just
   `[Gemini]` and behaviour matches v1. If the pool is empty on first boot, the filler
   warms it within a few `REFILL_INTERVAL`s; the read path's graceful-degradation branch
   covers the cold-start window. If `TIMEZONE` is unset it defaults to `Asia/Tehran`.
6. **Observability:** log which provider served each generation, pool depth per kind on
   every refill tick, suppressed broadcasts during quiet hours, and daily-review sends.

### Dependencies (added in v2)

| Package | Purpose |
|---|---|
| *(none required)* | OpenAI-compatible providers use the stdlib `net/http` + `encoding/json` already in use; only the native Gemini SDK dependency remains. |

---

# Roadmap — Future Versions (v3+)

> **Status: mostly implemented.** Changes **F (`/level`)** and **H (`/pause` /
> `/resume`)** shipped in **v1.4.0**; **J (Admin)** and **K (Weekly digest)** shipped
> in **v1.10.0** along with two additional quiz types (synonym + fill-in-the-blank).
> All v2 roadmap changes listed below are now implemented. Each Change is
> independent and can ship on its own; suggested order is given at the end. Schema
> additions are all additive (`CREATE TABLE IF NOT EXISTS`).

---

## Change D — Spaced Repetition Review (top priority) — IMPLEMENTED in v1.8.0

The highest-ROI learning feature: instead of seeing a word once, users re-encounter it
at growing intervals so it moves into long-term memory.

### Model

A lightweight SM-2-style scheduler per `(chat_id, word)`:

| Concept | Meaning |
|---|---|
| `interval` | Days until the next review (1 → 3 → 8 → 21 → 57 …) |
| `ease` | Multiplier that grows on "easy", shrinks on "hard" (default 2.5) |
| `due_at` | When the word should next resurface |
| `reps` | How many successful reviews so far |

### New table — `review_schedule`

| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber |
| `word` | `TEXT` | The vocabulary word |
| `interval_days` | `INTEGER` | Current interval |
| `ease` | `REAL` | Ease factor |
| `reps` | `INTEGER` | Successful repetitions |
| `due_at` | `DATETIME NOT NULL` | Next review time |

PK `(chat_id, word)`. Index `idx_review_due` on `(chat_id, due_at)`. A word enters the schedule the first time it is sent (seeded with
`interval=1`). A separate `reviewScheduler` goroutine (or a slot in the existing ticker)
picks words whose `due_at` has passed and re-sends a compact reminder card, then bumps
the interval. Difficulty feedback comes from Change E's quiz answers (correct = promote,
wrong = reset to interval 1) or from a simple "Knew it / Forgot" inline keyboard.

**Implementation (v1.8.0):** `srs.go` holds the SM-2-lite logic (`srsKnown` grows the
interval 1 → 3 → `round(interval × ease)` and nudges ease up; `srsForgot` resets to a
1-day interval and lowers ease, floored at `srsMinEase = 1.3`) plus the `review_schedule`
store methods (`SeedReview`, `DueReviews`, `ApplyReviewKnown`, `ApplyReviewForgot`,
`SnoozeReview`, `MasteredCount`). Words are seeded inside `recordSentFor(kindWord, …)`
(the single choke point for vocabulary sends), so every word a user receives — scheduled,
on-demand `/word`, or a Change-M lookup — is enrolled, idempotently. `runReviewScheduler`
ticks every `REVIEW_CHECK_INTERVAL` (default 1h), skips quiet hours and paused users, and
sends up to `REVIEW_BATCH_MAX` (default 3) compact "memory check" cards per user with a
`✅ Knew it` / `❌ Forgot` inline keyboard; each reminded word is snoozed so it isn't
re-sent before the user answers. The card **hides the meaning** (`formatReviewReminder`
no longer prints it) so the user must recall it first. The button tap routes through the
`srs:` callback branch (`handleReviewCallback`) which applies the promote/reset, reschedules
from the user's response, and **reveals the answer**: `✅ Knew it` shows a one-line meaning,
`❌ Forgot` shows the full stored vocabulary card (`Store.WordCard`) so the user can relearn
it in detail. (Reveal behaviour added in v1.23.3.) Timestamps are stored as UTC strings (`2006-01-02 15:04:05`) so lexicographic
`due_at <= now` comparison is correct. `/stats` gained a **Words mastered** line
(`interval_days >= srsMasteredIntervalDays = 21`). Tests: `srs_smoke_test.go`.

| Config | Default | Description |
|---|---|---|
| `REVIEW_CHECK_INTERVAL` | `1h` | How often the scheduler scans for due reviews |
| `REVIEW_BATCH_MAX` | `3` | Max review reminders sent per user per scan |

---

## Change E — Quiz / Active Recall — IMPLEMENTED in v1.9.0

Turns passive reading into testing, which is what actually builds recall.

### Behaviour

- Periodically (and via `/quiz`), pick a word the user has already seen (ideally one due
  in the spaced-repetition schedule) and send a question using a Telegram **inline
  keyboard** with answer buttons.
- Question types (rotated):
  - "What does **<word>** mean?" → 4 meaning options *(implemented)*
  - "Which word means *<meaning>*?" → 4 word options *(implemented)*
  - "Pick the synonym of **<word>**" → 4 options *(implemented v1.10.0)*
  - Fill-in-the-blank sentence with 4 word choices *(implemented v1.10.0)*
- Distractors are drawn from other words in `content_pool` (same `kind`), so no extra AI
  call is needed to build a quiz.

### Wiring

- Telegram updates must now also handle **`callback_query`** (button taps), not just
  `message`. *(Already implemented in v1.4.0 for `/level`: `pollTelegramUpdates` has a
  `callback_query` branch routing to `handleCallback`, plus `sendToTelegramWithKeyboard`,
  `editMessageText` and `answerCallbackQuery` helpers — Change E reuses these.)*
- New table `quiz_results(chat_id, word, correct, answered_at)` feeds both stats
  (Change G) and the spaced-repetition ease/interval updates (Change D).
- A correct answer promotes the word in `review_schedule`; a wrong answer resets it.

### Command

| Command | Behaviour |
|---|---|
| `/quiz` | Send one quiz question on demand (pulled from the user's seen words). |

**Implementation (v1.9.0, expanded v1.10.0):** `quiz.go` builds a multiple-choice question entirely
from pooled data — no AI call. Four question types are rotated: **word → meaning**
("What does WORD mean?"), **meaning → word** ("Which word means …?"),
**synonym** ("Pick the synonym of WORD" — parses the ✅ Synonyms section from the
card text via `parseSynonyms`), and **fill-in-the-blank** (blanks a bolded word in an
example sentence via `parseExampleForBlank` / `blankBoldedWords`). The synonym and
fill-in-the-blank types fall back gracefully to word→meaning / meaning→word when the
card text lacks parseable synonyms or example sentences. Card text is retrieved via
`PooledCardText(word)`.
`makeQuiz` picks a subject from the user's learned words (`SeenWordsWithMeaning`,
i.e. `sent_vocab` joined with pooled meanings), **biased toward words currently due**
for spaced-repetition review (`DueReviews`); `buildQuiz` fills three distractors from
`PoolWordMeanings` (random pooled words), shuffles, and records the correct index.
The inline keyboard tags the correct option `quiz:c:<word>` and wrong ones
`quiz:x:<word>` (so a tap always grades the subject word, and option text — which can
be a long meaning — never bloats the callback payload). `handleQuizCallback` records
the answer in `quiz_results`, feeds the spaced-repetition schedule
(`ApplyReviewKnown`/`ApplyReviewForgot`), reveals the answer + meaning, and removes the
keyboard. `runQuizScheduler` sends one quiz per eligible (non-paused, quiet-hour-clear)
subscriber every `QUIZ_INTERVAL` (default `6h`; set `0` to disable — `/quiz` still
works). `/stats` gained a **Quiz accuracy** line (`QuizStats`). Words with too little
material are skipped gracefully. Tests: `quiz_smoke_test.go`.

#### `quiz_results` table

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK AUTOINCREMENT` | Row id |
| `chat_id` | `INTEGER` | FK → subscriber |
| `word` | `TEXT` | Subject word graded |
| `correct` | `INTEGER` | `1` correct, `0` wrong |
| `answered_at` | `DATETIME` | When answered |

Index `idx_quiz_chat` on `chat_id`. Append-only history (no uniqueness) feeding
`/stats` accuracy and the spaced-repetition ease/interval updates.

| Config | Default | Description |
|---|---|---|
| `QUIZ_INTERVAL` | `6h` | How often scheduled quizzes fire (`0` disables; `/quiz` still works) |

---

## Change F — `/level` (per-user difficulty) — IMPLEMENTED in v1.4.0

Lets users tune content difficulty instead of the hard-coded "intermediate".

- Levels: `beginner`, `intermediate`, `upper-intermediate` (v1.16.0), `advanced`.
- Stored on the `user_prefs` table (see below), defaulting to `intermediate`.
- The chosen level is injected into the drill/word prompt via `levelInstruction`
  (`generation.go`) as a CEFR-band directive (A1–A2 / B1–B2 / B2–C1 / C1–C2).
- **Pool interaction (as implemented):** `content_pool` gained a `level` column.
  The `UNIQUE(kind, level, term)` constraint is per-level (v1.13.0; previously global),
  so the same term can be pooled at different difficulty levels. `PoolTerms` de-dupes
  within a level when building the filler's exclusion list. The
  filler (`runRefillCycle`) tops up each `(kind, level)` for every *active* level —
  the default level plus any level a user has selected (`Store.ActiveLevels`).
  `poolTargetFor` keeps the full `POOL_TARGET` for the default level and the smaller
  `POOL_MIN` for non-default levels. `serveContent`, `PooledUnseen` and
  `PooledOldest` all filter by level; a command miss generates inline at the user's
  level.

**Implementation:** `prefs.go` (level constants, validation, `user_prefs` methods),
`handleLevel` + `levelKeyboard` + callback handling in `main.go`.

| Command | Behaviour |
|---|---|
| `/level` | Show current level + inline buttons to change it. |
| `/level <beginner\|intermediate\|upper-intermediate\|advanced>` | Set the level directly by argument. |

---

## Change G — `/stats` — IMPLEMENTED in v1.5.0

Engagement driver: show the user their progress.

- Reads existing tables only: `sent_words`, `sent_vocab`, `subscribers`. Since
  v1.8.0/v1.9.0 it also surfaces **words mastered** (from `review_schedule`,
  Change D) and **quiz accuracy** (from `quiz_results`, Change E).
- Reports: total verbs practised, total words learned, distinct active days,
  current **streak** and longest streak (consecutive active days), level, and
  member-since date.
- The streak signal is derived from `sent_at` dates (no extra `activity` table):
  `activityDays` unions `sent_words`/`sent_vocab`, converts each `sent_at` to the
  app timezone, and `computeStreaks` walks the date set. The current streak anchors
  on today, or yesterday if there's no activity yet today (so it isn't lost until a
  full day is missed).

**Implementation:** `stats.go` (`UserStats` struct, `Store.UserStats`,
`activityDays`, `computeStreaks`, `formatStats`); `/stats` handler in `main.go`.

| Command | Behaviour |
|---|---|
| `/stats` | Send the user's personal progress summary. |

---

## Change H — `/pause` & `/resume` — IMPLEMENTED in v1.4.0

Soft opt-out of broadcasts without deleting history (you opted against full unsubscribe).

- A `paused` boolean lives on `user_prefs`.
- `broadcastContent` and the daily review skip paused users (`Store.IsPaused` /
  `GetPrefs`); spaced-repetition (Change D, future) should do the same.
- On-demand commands still work while paused (so `/drill` and `/word` ignore the flag).
- `/resume` clears the flag and confirms.

**Implementation:** `Store.SetPaused` / `IsPaused` in `prefs.go`; `/pause` and
`/resume` handlers in `main.go`; skip checks in `schedule.go`.

| Command | Behaviour |
|---|---|
| `/pause` | Stop scheduled sends; keep all history. |
| `/resume` | Re-enable scheduled sends. |

---

## Change I — Audio Pronunciation — IMPLEMENTED in v1.14.0

Let learners *hear* a word, not just read its IPA.

- After each delivered vocabulary card (`/word`, lookup, scheduled word slot), the bot
  best-effort sends a Telegram **voice note** (`sendVoice`) pronouncing the target word.
- TTS generation is provider-first with fallback:
  - Primary: Gemini (`gemini-2.5-flash:generateContent` with audio modality) using the
    existing `GEMINI_API_KEY`
  - Fallback: local `espeak-ng` (`espeak-ng -w ...`) for zero-cost offline synthesis
- Audio is best-effort only: any TTS/send failure is logged and never blocks card delivery.
- TTS is skipped during quiet hours and for paused users.
- Global config: `TTS_ENABLED` (default `true`); per-user opt-out: `user_prefs.tts_enabled`.
- Audio is cached per word in the `audio_cache` table: the Telegram `file_id` from the
  first `sendVoice` is stored and reused on subsequent sends, avoiding redundant TTS
  generation.

| Command | Behaviour |
|---|---|
| `/tts` | Show current pronunciation-audio status and usage. |
| `/tts on` | Enable pronunciation audio for this user. |
| `/tts off` | Disable pronunciation audio for this user. |

---

## Change J — Admin Metrics & Broadcast — IMPLEMENTED in v1.10.0

Maintainer-only operational tooling (gated by `chat_id == MAINTAINER_CHAT_ID`).

| Command | Behaviour |
|---|---|
| `/metrics` | Subscriber count (total/active/paused), pool depth per (kind, level), total quiz volume (answered/correct), total mastered words. |
| `/poolusage` | Per (kind, level), the most active user's consumption of the current pool as a percentage. Highlights pools nearing exhaustion. (v1.23.5) |
| `/announce <text>` | Push a one-off HTML message to all (non-paused) subscribers. |
| `/health` | Quick check: which providers are enabled and their count. |
| `/users` | Paginated user list (8 per page) with inline navigation and per-user detail view. (v1.20.0) |
| `/backup` | Trigger an immediate SQLite backup snapshot and send it as a document to maintainer chat. |
| `/admin` | List every maintainer command (hidden from public `/help` and the Telegram command menu). (v1.23.6) |

- All admin commands reply "not authorized" for non-maintainer chat IDs
  (`isMaintainer` parses `MAINTAINER_CHAT_ID` to `int64` and compares).
- `/announce` sends the raw text after the command as-is (HTML preserved) to all
  non-paused subscribers; reports the send count back to the maintainer.

### Admin Panel — `/users` (v1.20.0)

The `/users` command opens an interactive admin panel built entirely with inline
keyboards.

**User list** (`admin.go:sendAdminUserListPage`): queries `subscribers` joined with
`user_prefs`, ordered by `created_at DESC`, paginated at 8 users per page. Each
entry shows a status icon (active/paused), first name, difficulty level, and chat ID.
User name buttons (2 per row) link to the detail view; `◀️ Back` / `Next ▶️` buttons
navigate pages. The page indicator (`1/3`) is a no-op button (`admin:noop`).

**User detail** (`admin.go:sendAdminUserDetail`): shows a comprehensive snapshot of
one user including:
- Identity: first name, chat ID, join date
- Settings: level, status (active/paused), broadcast interval, quiz interval
- Toggles: TTS, tips, quiz, idiom, SRS review, weekly digest, daily review
- Progress: drill count, word count (+ mastered), idiom count, tip count
- Quiz: accuracy percentage and answer count
- SRS: total words tracked and currently due count
- Streaks: current streak (with fire at 3+), best streak, active days

**Direct messaging** (`admin.go:handleAdminMsgSend`): the detail view includes a
"Send Message" button. Tapping it enters message mode — the admin's next plain text
message is forwarded to the target user. An `atomic.Int64` (`adminMsgTarget`)
tracks the pending target; a "Cancel" button (`admin:msgcancel`) clears it. The
admin receives delivery confirmation or an error report.

**Callback data patterns:**

| Pattern | Purpose |
|---|---|
| `admin:users:<page>` | Navigate the paginated user list |
| `admin:user:<chatID>` | View a specific user's detail |
| `admin:msg:<chatID>` | Enter message mode for a user |
| `admin:msgcancel` | Cancel message mode |
| `admin:noop` | Page indicator button (no action) |

#### `cfg:` — Admin config panel (v1.22.0)

| Data | Action |
|---|---|
| `cfg:show` | Refresh the main config panel |
| `cfg:goto:<key>` | Show sub-keyboard for a config key |
| `cfg:pool_target:<n>` | Set pool target |
| `cfg:pool_min:<n>` | Set pool min |
| `cfg:quiet_start:<v>` | Set quiet hours start |
| `cfg:quiet_end:<v>` | Set quiet hours end |
| `cfg:gen_spacing:<v>` | Set AI generation spacing |
| `cfg:review_batch:<n>` | Set review batch max |
| `cfg:toggle:tts` | Toggle global TTS on/off |
| `cfg:goto:pool_kinds` / `cfg:goto:pool_levels` | Show the per-kind / per-level pool-size submenu (v1.23.2) |
| `cfg:goto:pk:<kind>` / `cfg:goto:pl:<level>` | Show the value picker for one kind / level (v1.23.2) |
| `cfg:pk:<kind>:<n>` / `cfg:pl:<level>:<n>` | Set (`n>0`) or clear (`n=0`) a per-kind / per-level pool override (v1.23.2) |
| `cfg:goto:pool_kl` | Show the per-(kind,level) flow: pick a kind (v1.23.6) |
| `cfg:goto:klk:<kind>` | Show the per-level picker for a chosen kind (v1.23.6) |
| `cfg:goto:klv:<kind>:<level>` | Show the value picker for one exact (kind, level) pool (v1.23.6) |
| `cfg:kl:<kind>:<level>:<n>` | Set (`n>0`) or clear (`n=0`) a per-(kind,level) pool override (v1.23.6) |

**New Store methods** (`admin.go`):

| Method | Signature | Description |
|---|---|---|
| `AdminListUsers` | `(page, perPage int) ([]AdminUserRow, int, error)` | Paginated user list with total count |
| `AdminUserDetail` | `(chatID int64) (AdminUserDetail, error)` | Full detail snapshot for one user |

**Implementation:** `admin.go` (`AdminUserRow`, `AdminUserDetail` types,
`AdminListUsers`/`AdminUserDetail` Store methods, `handleAdminUsers`,
`sendAdminUserListPage`, `sendAdminUserDetail`, `handleAdminCallback`,
`handleAdminMsgSend`, `toggleIcon`); `/users` route + `admin:` callback branch
wired in `main.go`; admin message intercept before the word-lookup fallback in
the default handler branch.

---

## Change K — Weekly Digest — IMPLEMENTED in v1.10.0

A Sunday-evening recap, natural extension of the daily review.

- A `runWeeklyDigestScheduler` goroutine fires once a week (configurable day/time,
  Tehran local time). Uses `nextWeekdayTime` to compute the next fire time.
- Per user: the week's new words (word + meaning), quiz accuracy for the week,
  current streak, mastered count, and a "word of the week" highlight (the first
  word learned that week).
- Respects quiet hours and the `paused` flag; idempotent via a
  `weekly_digest_delivery(chat_id, week_start)` table.
- If a user had no activity in the week, no digest is sent.
- `DIGEST_DAY` supports weekday names, abbreviations, or `off`/`none`/`disabled`
  to disable the digest entirely.

**Implementation (v1.10.0):** `config.go` adds `digestDay` (default `time.Sunday`)
and `digestTime` (default `"20:00"`) via `getEnvWeekday` helper; `schedule.go` adds
`runWeeklyDigestScheduler` (sleeps until `nextWeekdayTime`, then sends digests to all
eligible subscribers), `sendWeeklyDigest` (per-user delivery with idempotency check),
`formatWeeklyDigest` (formats the recap HTML), and `nextWeekdayTime` (computes the
next occurrence of a given weekday+time). Store methods in `pool.go`:
`WeeklyDigestDelivered`, `MarkWeeklyDigestDelivered`, `WeeklyQuizStats`.
Table `weekly_digest_delivery` created in `main.go` schema.

| Config | Default | Description |
|---|---|---|
| `DIGEST_DAY` | `Sunday` | Day of week to send the digest (`off` to disable) |
| `DIGEST_TIME` | `20:00` | Local time to send it |

---

## Change L — `/interval` (per-user send frequency) — IMPLEMENTED in v1.6.0

Let each learner choose how often scheduled drills/words arrive, instead of the
fixed 30-minute cadence.

- A new `interval_minutes` column on `user_prefs` (default `30`), added via the
  same additive `migrate()` pattern used for `content_pool.level`.
- Constrain choices to an aligned menu — `30, 60, 120, 180, 240, 360, 480, 720`
  minutes — so the alignment math stays exact and dividers of the day.
- The broadcast scheduler keeps ticking at the **30-minute base granularity**, but
  delivery becomes per-user inside the sweep:
  - A user is **due** this slot when `minutesSinceMidnight % interval == 0`
    (wall-clock alignment, so restarts/quiet-hours never desync — same principle
    as `slotKind`). Intervals must be multiples of the 30-min base tick.
  - The drill/word **alternation is per-user**, derived from that user's own slot
    index `minutesSinceMidnight / interval` parity, so every user keeps
    alternating regardless of their chosen interval.
- This changes `broadcastContent` from "one global kind to everyone" to iterating
  subscribers and computing each user's due-ness + kind from their prefs. Quiet
  hours and the `paused` flag still apply per user.
- `/interval` shows the current value + an inline keyboard (reusing the
  `callback_query` infra from v1.4.0); `/interval <minutes>` sets it directly.

**Planned implementation:** `interval_minutes` on `user_prefs` + migration in
`prefs.go`/`main.go`; due-check + per-user kind in `schedule.go`; `/interval`
handler, keyboard and callback branch in `main.go`.

**Implementation (v1.6.0):** `interval_minutes` column on `user_prefs` (default
30) + additive `migrate()` ALTER; `defaultInterval`, `allIntervals`,
`normalizeInterval`, `intervalLabel`, `Store.GetInterval`/`SetInterval` and
`UserPrefs.Interval` in `prefs.go`; `minutesSinceMidnight` + `dueAndKind`
(alignment-based due-check & per-user drill/word alternation) feeding the new
`broadcastSweep` in `schedule.go`; `handleInterval`, `intervalKeyboard` and the
`interval:` callback branch in `main.go`.

| Command | Behaviour |
|---|---|
| `/interval` | Show current send interval + inline buttons to change it. |
| `/interval <minutes>` | Set the interval directly (must be one of the allowed values). |

---

## Change M — Word Lookup (send any word) — IMPLEMENTED in v1.7.0

Let a user look up a specific word on demand by simply **typing it** (no command),
and get back a vocabulary card formatted exactly like `/word`.

- **Trigger:** any non-command message (text without a leading `/`). Today this
  falls through to the `default` branch ("I only understand commands"); that branch
  becomes the lookup entry point.
- **Behaviour:** generate a vocabulary card for the *user-supplied term* at the
  user's level, using the same card layout/parser as `/word`
  (`📘 Word of the Session …` → meaning, pronunciation, synonyms, opposites, examples).
- **Translation:** the input may be English **or the user's native language
  (Farsi)**. The prompt instructs the model to resolve the input to its English
  headword (translating if needed) and build the card around that English word, so
  "سیب" → an **apple** card. *(Open decision: also append a one-line native-language
  translation of the meaning. Default: yes, a short Farsi gloss under the meaning.)*
- **Generation:** add a term-targeted prompt builder + a generation entry point
  that accepts an explicit term, e.g. `buildWordLookupPrompt(level, term)` and a
  `generateWordFor(ctx, chain, level, term)` (parallel to `generateContent`).
- **Storage:** treat a successful lookup like an on-demand `/word`: add the card to
  `content_pool` at the user's level and record it in `sent_vocab` (so it counts
  toward `/stats`, the daily review meaning-join works, and repeats are avoided).
- **Guards:** reject empty input and over-long messages (e.g. > 4 words / > 40
  chars) with a gentle hint, so sentences/paragraphs aren't treated as a word.
  Send a "🔄 Looking that up…" ack like `/drill` and `/word` do.

**Planned implementation:** `buildWordLookupPrompt` + `generateWordFor` in
`generation.go`; lookup handler replacing the `default` branch in `main.go`
(length guard, ack, generate, pool + record, send); welcome/help mention.

| Input | Behaviour |
|---|---|
| `serendipity` (plain text) | Returns a full vocabulary card for *serendipity*. |
| `سیب` (plain text, Farsi) | Translates to English and returns an *apple* card. |
| (a long sentence) | Gentle hint to send a single word instead. |

---

## Consolidated new tables (v3+)

| Table | Purpose | Introduced by |
|---|---|---|
| `user_prefs` | level, paused flag, interval_minutes, (future per-user settings) | F, H, L |
| `review_schedule` | spaced-repetition state per word | D |
| `quiz_results` | quiz answer history | E |
| `audio_cache` | word → Telegram voice `file_id` | I *(planned)* |
| `activity` *(optional)* | per-day activity for streaks | G |
| `weekly_digest_delivery` | digest idempotency | K |

## Telegram API additions (v3+)

| Need | API |
|---|---|
| Quiz answer buttons | inline keyboards + `callback_query` handling in the poller |
| Audio pronunciation | `sendVoice` (Gemini TTS with `espeak-ng` fallback, per-user `/tts` toggle) |
| "Hear it" / "Knew it / Forgot" buttons | inline keyboards on cards |

## Suggested implementation order (v3+)

1. ~~**Change F (`/level`)** & **Change H (`/pause`/`/resume`)**~~ — ✅ **DONE in v1.4.0**
   (both built on the new `user_prefs` table; `callback_query` handling landed here too).
2. ~~**Change G (`/stats`)**~~ — ✅ **DONE in v1.5.0** (read-only over `sent_words`/
   `sent_vocab`/`subscribers`; streak derived from `sent_at` dates).
3. ~~**Change L (`/interval`)**~~ — ✅ **DONE in v1.6.0** (per-user send frequency;
   `user_prefs.interval_minutes` + per-user due-check sweep; reused `callback_query` infra).
4. ~~**Change M (Word lookup)**~~ — ✅ **DONE in v1.7.0** (`generateWordFor` +
   `handleWordLookup` replacing the `default` branch; pools & records like `/word`).
5. ~~**Change D (Spaced Repetition)**~~ — ✅ **DONE in v1.8.0** (`review_schedule` +
   SM-2-lite `srs.go`; seeded on every word send; hourly reminder sweep with
   Knew-it/Forgot buttons; `/stats` mastered count).
6. ~~**Change E (Quiz / Active Recall)**~~ — ✅ **DONE in v1.9.0** (`quiz_results` +
   `quiz.go`; multiple-choice from pooled words, biased to due reviews; `/quiz` +
   6h scheduler; feeds D and /stats accuracy).
7. ~~**Change J (Admin metrics)**~~ — ✅ **DONE in v1.10.0** (`/metrics`, `/announce`,
   `/health` gated by `MAINTAINER_CHAT_ID`; `SubscriberStats`, `TotalQuizStats`,
   `TotalMasteredCount` in `stats.go`).
8. ~~**Change I (Audio)**~~ — ✅ **DONE in v1.14.0** (best-effort post-word/idiom `sendVoice`, Gemini-first with `espeak-ng` fallback, `/tts on|off`, `user_prefs.tts_enabled`, `audio_cache` table).
9. ~~**Change K (Weekly digest)**~~ — ✅ **DONE in v1.10.0** (`runWeeklyDigestScheduler`
   in `schedule.go`; `weekly_digest_delivery` table; `DIGEST_DAY`/`DIGEST_TIME` config;
   two new quiz types — synonym + fill-in-the-blank — also shipped in v1.10.0).

---

# Competitive Insights & Future Roadmap

> Research date: June 2026. Compared against real Telegram English-learning bots
> and mainstream language platforms.

## Telegram Bot Landscape

| Bot | Focus | Key differentiator |
|---|---|---|
| **@AndyRobot** | Conversational AI | Free-form English chat, grammar explanations on demand, topic-based discussions. Most popular English bot on Telegram (millions of users). |
| **@LingualeoBot** | Vocabulary platform | Personal dictionary, spaced repetition flashcards, word training. Extension of the Lingualeo web/mobile platform. |
| **@wordly_bot** | Vocabulary drills | Word-of-the-day, quizzes, translations. Purely vocabulary-focused. |
| **@vocab_bot** | Custom flashcards | Users build personal word lists and drill them. Anki-like in Telegram. |
| **@Multitran_bot** | Dictionary | Inline translations from the Multitran crowdsourced dictionary. Lookup-only, no learning loop. |
| **@daily_english_bot** | Daily lessons | Scheduled English content for Farsi speakers. Push-based daily delivery. |
| **@englishclub_bot** | Community content | Regular grammar tips and exercises. Community-oriented. |

### Where our bot stands

Our bot already covers the core features of most Telegram competitors:
- Spaced repetition (SM-2) -- matches @LingualeoBot, @vocab_bot
- Scheduled content push -- matches @daily_english_bot
- Multiple quiz types -- matches or exceeds @wordly_bot
- Word lookup -- matches @Multitran_bot (but with full AI-generated cards)
- Difficulty levels + personalization -- exceeds most Telegram bots
- Weekly/daily review -- not common in Telegram bots

### What top competitors do that we don't

1. **Conversational practice** (@AndyRobot's core feature -- and the #1 reason it dominates)
2. **Topic/theme selection** (Lingualeo, Drops -- users pick what to learn)
3. **Custom word lists** (@vocab_bot, Anki -- user-curated study decks)
4. **Inline mode** (@Multitran_bot -- translate without opening the bot)
5. **Pronunciation/audio** (ELSA Speak, Change I in our roadmap)
6. **Grammar correction** (no major Telegram bot does this well -- opportunity)
7. **Points/XP gamification** (Duolingo's retention engine)
8. **Placement test** (auto-detect level instead of user guessing)

---

## Proposed Changes (v1.11.0+)

### Change N — AI Conversation Practice (`/chat`)

**Priority: HIGH** | **Effort: Medium** | **Inspired by: @AndyRobot**

The single biggest feature gap. Let users have free-form English conversations
with the bot, with real-time grammar and vocabulary corrections.

- `/chat` toggles conversation mode. User writes in English, bot responds
  naturally and highlights mistakes inline.
- `/chat topic` starts a themed conversation (e.g. `/chat restaurant`,
  `/chat job interview`, `/chat travel`).
- `/endchat` exits conversation mode and shows a summary: mistakes made,
  words used, suggestions for improvement.
- Uses existing AI provider chain -- no new infrastructure needed.
- New table: `chat_sessions(chat_id, started_at, topic, message_count,
  mistakes_found, ended_at)` for tracking.
- Words discovered during conversation are auto-seeded into the SRS schedule.

**Why this matters:** @AndyRobot is the most popular English bot on Telegram
primarily because of conversational practice. This is the highest-impact
feature we can add.

---

### Change O — Topic/Theme Selection (`/topic`)

**Priority: HIGH** | **Effort: Low** | **Inspired by: Drops, Lingualeo**

Let users choose what vocabulary domain to study instead of receiving
random words.

- `/topic` shows available topics via inline keyboard: General, Travel,
  Business, Technology, Food & Cooking, Medical, Academic, Idioms & Slang.
- Topic stored in `user_prefs.topic` (default: `general`).
- Prompt builders append the topic constraint: "Generate a vocabulary word
  related to [topic]..."
- `general` behaves like today (no constraint).
- Pool filler generates content per-level AND per-topic.

| New column | Table | Type | Default |
|---|---|---|---|
| `topic` | `user_prefs` | `TEXT` | `'general'` |

---

### Change P — Grammar Correction (`/correct`)

**Priority: HIGH** | **Effort: Low** | **Inspired by: Grammarly, no major Telegram bot does this**

User sends a sentence, bot corrects grammar and explains each mistake.

- `/correct I go to store yesterday` returns a corrected version with
  inline explanations.
- Also works without the command: if conversation mode (Change N) is
  active, corrections happen automatically within the chat flow.
- Uses existing AI providers with a grammar-correction prompt.
- Tracks corrections in `quiz_results` (new `correction` question type)
  so repeated mistakes surface more often in quizzes.

**Opportunity:** No major Telegram bot does grammar correction well.
This could be a differentiator.

---

### Change Q — Idiom & Phrase of the Day

**Priority: MEDIUM** | **Effort: Low**

Add idioms/phrases as a third content kind alongside drills and words.

- New content kind: `idiom` (pool key `kind='idiom'`).
- Broadcast rotation: drill -> word -> idiom -> drill -> word -> idiom...
- Idiom card format: the phrase, meaning, literal translation (if
  applicable), 2-3 example sentences, origin/context note.
- `/idiom` command for on-demand idiom delivery.
- Idioms are seeded into SRS like words.

---

### Change R — Placement Test (`/test`)

**Priority: MEDIUM** | **Effort: Medium** | **Inspired by: ELSA, Busuu**

Auto-detect user level instead of requiring them to guess.

- `/test` presents 10-15 adaptive questions (vocabulary, grammar,
  sentence completion) starting at intermediate.
- Each correct answer increases difficulty; each wrong answer decreases it.
- After the test, the bot sets `/level` automatically and reports the
  result: "Your level: Intermediate (B1). I've adjusted your content."
- Can be re-taken anytime.
- New table: `placement_results(chat_id, taken_at, score, assigned_level)`.

---

### Change S — Sentence Rearrangement Quiz

**Priority: MEDIUM** | **Effort: Medium** | **Inspired by: Duolingo**

New quiz type: scrambled words, user taps buttons in order to form a
correct sentence.

- Bot presents a shuffled sentence as numbered inline buttons.
- User taps buttons in sequence; bot confirms or shows the correct order.
- Good for testing grammar intuition (word order, prepositions, articles).
- Added as a 5th quiz type in the existing `makeQuiz` rotation.

---

### Change T — Custom Word Lists (`/add`, `/list`)

**Priority: MEDIUM** | **Effort: Low** | **Inspired by: Anki, @vocab_bot**

Let users curate their own study list.

- `/add ephemeral` adds a word to the user's custom list (generates a
  full card via AI and pools it).
- `/list` shows all custom words with mastery status.
- Custom words get priority in quiz rotation and SRS scheduling.
- New table: `custom_words(chat_id, word, added_at)`.

---

### Change U — Points & Streaks Gamification

**Priority: LOW** | **Effort: Medium** | **Inspired by: Duolingo**

XP system to drive daily engagement.

- Earn XP for: drills (+5), words (+5), quizzes (+10 correct / +3
  wrong), reviews (+5 known / +2 forgot), conversations (+2/message),
  daily streak bonus (streak * 5).
- `/stats` shows total XP alongside existing stats.
- Weekly digest includes XP earned that week.
- New column: `user_prefs.xp INTEGER DEFAULT 0`.
- Future: leaderboard among all users (`/leaderboard`).

---

### Change I — Audio Pronunciation (implemented in v1.14.0)

**Priority: LOW** | **Effort: Medium**

Shipped as documented above: post-word `sendVoice` pronunciation using Gemini TTS
with `espeak-ng` fallback, plus per-user `/tts` opt-out.

---

## Suggested implementation order (v1.11.0+)

| Order | Change | Version | Rationale |
|---|---|---|---|
| 1 | **O (Topics)** | v1.11.0 | Lowest effort, immediate personalization value |
| 2 | **P (Grammar correction)** | v1.11.0 | Low effort, no Telegram competitor does this |
| 3 | **Q (Idioms)** | v1.11.0 | New content kind, tiny code change |
| 4 | **N (Conversation)** | v1.12.0 | Highest impact, needs conversation state management |
| 5 | **R (Placement test)** | v1.12.0 | Pairs well with conversation -- both need AI prompts |
| 6 | **T (Custom words)** | v1.13.0 | User-driven content, extends existing pool infra |
| 7 | **S (Sentence rearrangement)** | v1.13.0 | New quiz type, needs button-sequence UX |
| 8 | **U (XP/Gamification)** | v1.14.0 | Cross-cutting -- add after core features stabilize |
| 9 | **I (Audio)** | v1.14.0 | ✅ Implemented (Gemini + `espeak-ng` fallback, `/tts`, `audio_cache`) |
