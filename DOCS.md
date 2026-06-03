# English Muscle Memory Bot — Technical Documentation

## Overview

A Telegram bot written in Go that sends subscribers AI-generated English practice every 30 minutes, alternating between two formats:

- 🎯 a **grammar drill** — one verb conjugated across 14 tenses
- 📘 a **vocabulary word** — meaning, pronunciation, synonyms, opposites and example sentences

The bot uses Google Gemini 2.5 Flash to generate unique, personalized content and tracks which verbs and words have already been sent to each user, ensuring they always receive fresh material. When a new version is deployed, existing subscribers automatically receive a changelog message on their next broadcast or `/start`.

---

## Architecture

```
main.go  (single-file Go application)
├── main()                    — startup, wires all components
├── Telegram long-poll loop   — receives commands
├── 30-min broadcast ticker   — alternates drill ↔ word (+ changelogs) to all subscribers
├── Gemini AI client          — generates drill & vocabulary content
└── SQLite store              — persists subscribers, per-user verb & word history, changelog delivery
```

The application runs three concurrent goroutines:
1. **Broadcast goroutine** — fires every 30 minutes via `time.Ticker`, alternating between a tense drill and a vocabulary word
2. **Telegram poller goroutine** — long-polls Telegram for incoming messages
3. **Main goroutine** — blocks on `os.Signal` for graceful shutdown

---

## Configuration

All configuration is read from environment variables at startup. There are no config files.

| Variable | Default | Description |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | `YOUR_TELEGRAM_BOT_TOKEN` | Telegram Bot API token from @BotFather |
| `GEMINI_API_KEY` | `YOUR_GEMINI_API_KEY` | Google Gemini API key |
| `MAINTAINER_CHAT_ID` | `YOUR_PERSONAL_CHAT_ID` | Chat ID that receives new-user join notifications |

At startup the bot logs whether each token is loaded (true/false) and the token length — it never logs the actual token value.

---

## Changelog System

The bot has a built-in mechanism to notify existing subscribers when a new version is deployed.

### How it works

1. The `Changelogs` slice in `main.go` is the append-only release history. Each entry has a `Version` string and an HTML-formatted `Text` message.
2. When a **new user** runs `/start`, all existing changelog versions are immediately marked as seen — they only receive notes for versions released after they joined.
3. **Existing users** receive any unseen changelog entries:
   - At the start of each hourly broadcast cycle (before their drill)
   - When they run `/start` again

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
| `sent_at` | `DATETIME` | When the verb was sent |

Primary key is `(chat_id, word)` — inserts are idempotent (`INSERT OR IGNORE`).
Index: `idx_sent_words_chat` on `chat_id` for fast per-user lookups.

#### `sent_vocab`
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `word` | `TEXT` | Lowercased vocabulary word that was already sent |
| `sent_at` | `DATETIME` | When the word was sent |

Primary key is `(chat_id, word)` — inserts are idempotent (`INSERT OR IGNORE`).
Index: `idx_sent_vocab_chat` on `chat_id`. Kept separate from `sent_words` so grammar-drill verbs and vocabulary words have independent exclusion lists.

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
| `level` | `TEXT` | Difficulty: `beginner` / `intermediate` / `advanced` (added v1.4.0, default `intermediate`) |
| `created_at` | `DATETIME` | When the item was generated |

Unique constraint on `(kind, term)` — the pool never holds duplicate terms per kind
(global across levels). Index: `idx_content_pool_kind` on `(kind, level, created_at)`.
The `poolFiller` goroutine keeps each `(kind, level)` for every *active* level topped
up (`POOL_TARGET` for the default level, `POOL_MIN` for others); `serveContent` reads
from here first, filtered by the user's level, and falls back to inline generation
(commands) or the oldest pooled item at that level (broadcasts).

> **Migration:** `Store.migrate` adds the `level` column via `ALTER TABLE` on older
> databases that predate v1.4.0 (guarded by a `PRAGMA table_info` check).

#### `daily_review_delivery` (v1.3.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER` | FK → subscriber chat_id |
| `review_date` | `TEXT` | Local `YYYY-MM-DD` of the reviewed day |
| `sent_at` | `DATETIME` | When the review was delivered |

Primary key is `(chat_id, review_date)` — the midnight word review is sent at most
once per user per day.

#### `user_prefs` (v1.4.0)
| Column | Type | Description |
|---|---|---|
| `chat_id` | `INTEGER PRIMARY KEY` | FK → subscriber chat_id |
| `level` | `TEXT` | Difficulty, default `intermediate` (Change F) |
| `paused` | `INTEGER` | `1` = scheduled sends paused, default `0` (Change H) |
| `updated_at` | `DATETIME` | Last change time |

Rows are created lazily via upsert (`INSERT … ON CONFLICT`) the first time a user sets
a level or pauses; `GetPrefs` returns defaults when no row exists. Managed by the
methods in `prefs.go`.

### Legacy Migration

On first run, if a `subscribers.json` file exists (the old flat-file format), `migrateLegacyJSON` imports all active subscribers into SQLite and renames the file to `subscribers.json.migrated` so the migration only runs once.

---

## Components

### `ChangelogEntry`

```go
type ChangelogEntry struct {
    Version string
    Text    string   // Telegram HTML-formatted message
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
| `AddSubscriber` | `(chatID int64) (bool, error)` | Inserts subscriber; returns `true` if newly added |
| `Subscribers` | `() ([]int64, error)` | Returns all subscribed chat IDs |
| `SentWords` | `(chatID int64) ([]string, error)` | Returns verbs already sent to a user, ordered by `sent_at` |
| `RecordSentWord` | `(chatID int64, word string) error` | Records a verb as sent (idempotent) |
| `ResetSentWords` | `(chatID int64) (int64, error)` | Deletes all verb history for a user; returns count removed |
| `SentVocab` | `(chatID int64) ([]string, error)` | Returns vocabulary words already sent to a user, ordered by `sent_at` |
| `RecordSentVocab` | `(chatID int64, word string) error` | Records a vocabulary word as sent (idempotent) |
| `ResetSentVocab` | `(chatID int64) (int64, error)` | Deletes all vocabulary history for a user; returns count removed |
| `MarkChangelogSeen` | `(chatID int64, version string) error` | Records that a changelog version was delivered to a user |
| `UnseenChangelogs` | `(chatID int64) ([]ChangelogEntry, error)` | Returns changelog entries not yet delivered to this user |

---

### Telegram Types

```go
TelegramUpdate   — update_id, *Message
TelegramMessage  — message_id, Chat, Text, *From
TelegramChat     — ID (int64)
TelegramUser     — ID (int64), Username (string)
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

| Command | Behaviour |
|---|---|
| `/start` | Subscribes user (idempotent). If new: baselines all changelogs as seen, notifies maintainer, sends welcome. If returning: delivers any unseen changelogs first. Always sends welcome message. |
| `/help` | Sends usage instructions covering grammar drills (14 tenses), vocabulary words, level and pause/resume. |
| `/drill` | Sends a "Generating…" ack, calls `serveContent(kind=drill, level, allowGenerate=true)` (pool-first, inline-gen on miss), returns the result. |
| `/word` | Sends a "Finding a fresh word…" ack, calls `serveContent(kind=word, level, allowGenerate=true)`, returns the vocabulary card. |
| `/level [lvl]` | With an argument, sets difficulty directly; without, shows the current level + an inline keyboard (`handleLevel`). Button taps arrive as `callback_query` and are handled by `handleCallback`. (v1.4.0) |
| `/stats` | Sends a read-only progress summary: drills practised, words learned, mastered, active days, current & longest daily streak, quiz accuracy, level (`UserStats` / `formatStats`). (v1.5.0; mastered v1.8.0; quiz accuracy v1.9.0) |
| `/quiz` | Sends one multiple-choice quiz on a learned word (biased to due reviews); the tapped answer is recorded and feeds spaced repetition. (v1.9.0) |
| `/pause` | Sets `user_prefs.paused = 1`; scheduled sends are skipped, on-demand still works. (v1.4.0) |
| `/resume` | Clears the paused flag. (v1.4.0) |
| `/interval [min]` | With a numeric argument, sets the send interval directly; without, shows the current interval + an inline keyboard of options (`handleInterval`). Allowed: 30/60/120/180/240/360/480/720 min. (v1.6.0) |
| `/reset` | Clears both verb (`ResetSentWords`) and vocabulary (`ResetSentVocab`) history; reports how many of each were cleared. |
| *(anything else)* | Replies with a prompt to use /drill, /word, /level or /help. |
| *(empty text)* | Silently ignored. |

> **Note:** the "Components" subsections below describe the original v1 design.
> Behaviour changed in v1.3.0 (provider chain, content pool, quiet hours, daily
> review) and v1.4.0 (`/level`, `/pause`, `/resume`); see the **Roadmap** sections,
> which are marked IMPLEMENTED, for the current architecture.

---

### Hourly Broadcast — `broadcastDrill` / `broadcastWord`

The ticker fires every **30 minutes**. A boolean toggle in `main()` alternates which broadcast runs, so each subscriber receives one grammar drill and one vocabulary word per hour, ~30 minutes apart:

```
tick 1 → broadcastDrill   (drill)
tick 2 → broadcastWord    (word)
tick 3 → broadcastDrill   (drill)
...
```

Both functions:
- Read the full subscriber list
- For each subscriber, in order:
  1. Call `sendPendingChangelogs` — deliver any unseen version notes
  2. Generate and send the personalized content (`generatePersonalizedDrill` or `generatePersonalizedWord`)
- Log errors per-user but continue to the next subscriber on failure

---

### Changelog Delivery — `sendPendingChangelogs`

Queries `UnseenChangelogs` for the user, then for each unseen entry:
1. Sends the message via `sendToTelegram`
2. Only calls `MarkChangelogSeen` after a successful send — so a delivery failure does not suppress future retry attempts

---

### AI Drill Generation — `generateGeminiDrill`

**Model:** `gemini-2.5-flash`

**Retry policy:** 3 attempts with exponential backoff (2 s → 4 s → 8 s). Respects context cancellation between retries.

**Output format:** Telegram HTML (`parse_mode: HTML`). The prompt instructs Gemini to wrap label lines in `<b>` tags and bold the target verb form inside each example sentence.

**Tenses covered (14 total):**

| # | Tense | Use |
|---|---|---|
| 1 | Simple Present | Routine / Habit |
| 2 | Present Continuous | Right Now / Temporary |
| 3 | Present Perfect | Experience / Recent Result |
| 4 | Present Perfect Continuous | Ongoing Until Now |
| 5 | Simple Past | Finished Action |
| 6 | Past Continuous | Was in Progress |
| 7 | Past Perfect | Before Another Past Event |
| 8 | Past Perfect Continuous | Duration Before a Past Event |
| 9 | Future: be going to | Plan / Intention |
| 10 | Future: will | Prediction / Spontaneous Decision |
| 11 | Future Continuous | In Progress at a Future Moment |
| 12 | Future Perfect | Completed by a Future Point |
| 13 | Future Perfect Continuous | Duration up to a Future Point |
| 14 | First Conditional | Real Future Possibility |

**Exclusion clause:** If the user has prior verb history, the prompt appends an explicit list of already-practiced verbs with an instruction to choose something different.

**Return values:** `(drillText string, verb string, error)`

---

### AI Vocabulary Generation — `generateGeminiWord`

**Model:** `gemini-2.5-flash`

**Retry policy:** 3 attempts with exponential backoff (2 s → 4 s → 8 s). Respects context cancellation between retries.

**Difficulty target:** intermediate / upper-intermediate learner words (any part of speech).

**Output format:** Telegram HTML (`parse_mode: HTML`, only `<b>` and `<i>` tags). The card structure is:

```
📘 Word of the Hour: {WORD}
————————————————————
💬 Meaning            — one simple sentence
🔊 Pronunciation      — syllable spelling · IPA
✅ Synonyms           — 3–5, comma-separated
⛔ Opposites          — 2–4, comma-separated
📝 Examples           — two example sentences (target word bolded)
💡 (read-it-aloud nudge)
```

**Exclusion clause:** If the user has prior vocabulary history, the prompt appends an explicit list of already-sent words with an instruction to choose something different.

**Return values:** `(cardText string, word string, error)`

---

### Personalized Generators — `generatePersonalizedDrill` / `generatePersonalizedWord`

Each wraps its respective Gemini call with per-user history:
1. Loads the user's exclusion list (`SentWords` / `SentVocab`)
2. Calls the generator with that list
3. Records the newly used verb/word (`RecordSentWord` / `RecordSentVocab`)

---

### Term Extraction — `parseVerb` / `parseWord`

Both delegate to a shared helper `parseLabeledTerm(text, label)`:
- `parseVerb` scans for the `Verb of the Hour:` line
- `parseWord` scans for the `Word of the Hour:` line

The helper strips any trailing HTML tags (e.g. `</b>`), strips Markdown punctuation, extracts the first token, and lowercases it. The HTML-aware stripping truncates everything from the first `<` character so terms ending in `b` (e.g. `grab`) are not corrupted by the closing tag trim.

---

### Telegram Sender — `sendToTelegram`

Posts to `sendMessage` with `parse_mode: HTML`. Returns an error if the HTTP response is not 200 OK. All callers in `handleMessage`, `broadcastDrill` and `broadcastWord` discard the return error with `_` (best-effort delivery); failures are logged.

---

## Data Flow: Scheduled Broadcast

```
ticker fires (every 30 min) — alternates drill ↔ word
  └─ broadcastDrill  (odd ticks)  /  broadcastWord  (even ticks)
       └─ store.Subscribers()              → list of chat IDs
       └─ for each chatID:
            ├─ sendPendingChangelogs(chatID)
            │    └─ store.UnseenChangelogs(chatID)    → unseen entries
            │    └─ for each entry:
            │         └─ sendToTelegram(chatID, entry.Text)
            │         └─ store.MarkChangelogSeen(chatID, entry.Version)
            │
            ├─ [drill]  generatePersonalizedDrill(chatID)
            │    └─ store.SentWords(chatID)           → exclusion list
            │    └─ generateGeminiDrill(exclusionList)
            │         └─ Gemini API (up to 3 retries, HTML format)
            │         └─ returns (drillText, verb)
            │    └─ store.RecordSentWord(chatID, verb)
            │    └─ sendToTelegram(chatID, drillText)
            │
            └─ [word]   generatePersonalizedWord(chatID)
                 └─ store.SentVocab(chatID)           → exclusion list
                 └─ generateGeminiWord(exclusionList)
                      └─ Gemini API (up to 3 retries, HTML format)
                      └─ returns (cardText, word)
                 └─ store.RecordSentVocab(chatID, word)
                 └─ sendToTelegram(chatID, cardText)
```

---

## Data Flow: /drill & /word Commands

```
user sends /drill                      user sends /word
  └─ sendToTelegram("Generating…")        └─ sendToTelegram("Finding a word…")
  └─ generatePersonalizedDrill(chatID)    └─ generatePersonalizedWord(chatID)
  └─ sendToTelegram(chatID, drillText)    └─ sendToTelegram(chatID, cardText)
```

---

## Startup Sequence

```
1. Load env vars (token, API key, maintainer ID)
2. Create Gemini AI client
3. Open SQLite store (apply schema, run legacy migration if needed)
4. Start 30-minute ticker goroutine (alternates drill ↔ word)
5. Start Telegram long-poll goroutine
6. Block on OS signal (SIGINT / SIGTERM)
7. On signal: cancel context → goroutines exit, deferred store.Close() runs
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
Gemini -> Groq -> Cerebras -> OpenRouter -> GitHub Models -> Cloudflare -> Mistral
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

Optional overrides (per provider): `<PROVIDER>_MODEL` to swap the model without code
changes, and `AI_PROVIDER_ORDER` (comma-separated names) to reorder/disable the chain.

### Prompt compatibility note

The current prompts already produce Telegram-HTML output and work with any
instruction-following chat model, so they are reused unchanged. The only adaptation is
mapping our single prompt string onto the OpenAI `messages` array
(`[{role:"user", content: prompt}]`) for the compatible providers.

---

## Change B — Decoupled Generation Pool

Today `generatePersonalizedDrill` / `generatePersonalizedWord` call the AI **inline**
during a user request or broadcast. v2 moves all generation into a background worker.

### New component — Pool Filler goroutine

A fourth long-running goroutine started in `main()`:

```
poolFiller(ctx, chain, store):
  ticker every REFILL_INTERVAL (e.g. 20s)
  on each tick, for kind in {drill, word}:
      n = store.PoolCount(kind)
      if n < POOL_MIN:                      // low watermark
          for i in 0 .. (POOL_TARGET - n):  // refill toward target
              if ctx cancelled: return
              exclude = store.PoolTerms(kind)            // avoid duplicates
              text, term = generateWithFallback(promptFor(kind, exclude))
              store.AddToPool(kind, term, text)
              sleep GEN_SPACING               // throttle to respect rate limits
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
| `provider` | `TEXT` | Which AI produced it (observability) |
| `created_at` | `DATETIME` | Generation time |

`UNIQUE(kind, term)` keeps the pool free of duplicates; index on `kind`.

### New Store methods

| Method | Description |
|---|---|
| `PoolCount(kind) (int, error)` | How many items are pooled for a kind |
| `PoolTerms(kind) ([]string, error)` | All pooled terms (exclusion list for the filler) |
| `AddToPool(kind, term, text, provider) error` | Insert a generated item (idempotent) |
| `PooledUnseen(chatID, kind) (term, text string, found bool, err)` | A pooled item this user hasn't received yet |
| `SentTerms(chatID, kind) ([]string, error)` | Dispatches to `SentWords`/`SentVocab` by kind |
| `RecordSent(chatID, kind, term) error` | Dispatches to `RecordSentWord`/`RecordSentVocab` by kind |

### Read path (request / broadcast)

```
serveContent(chatID, kind):
  term, text, found = store.PooledUnseen(chatID, kind)
  if found:
      store.RecordSent(chatID, kind, term)
      return text                              // ZERO AI calls
  else:
      // pool exhausted for this user - graceful degradation, pick one of:
      //  (a) re-send the least-recently-seen pooled item, or
      //  (b) reply "more practice coming soon" and let the filler catch up
      return fallbackText
```

The broadcast loop and `/drill` `/word` handlers call `serveContent` instead of the
inline generators. **No user request ever blocks on an AI call.**

### Tuning knobs (env vars)

| Variable | Default | Description |
|---|---|---|
| `POOL_TARGET` | `30` | Desired pooled items per kind |
| `POOL_MIN` | `10` | Low watermark that triggers a refill |
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
————————————————————
• <b>vigorously</b> — done with great energy or force
• <b>reluctant</b> — unwilling and hesitant to do something
• <b>tedious</b> — too long, slow, or dull; boring

😴 Sleep well — fresh practice resumes at 9 AM.
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
   `OpenAICompatProvider`, and `ProviderChain`; replace direct `client.Models.GenerateContent`
   calls inside `generateGeminiDrill` / `generateGeminiWord` with `chain.generateWithFallback`.
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

> **Status: partially implemented.** Changes **F (`/level`)** and **H (`/pause` /
> `/resume`)** shipped in **v1.4.0** (see their sections, marked IMPLEMENTED). The
> remaining changes below are still planned. Each Change is independent and can ship
> on its own; suggested order is given at the end. Schema additions are all additive
> (`CREATE TABLE IF NOT EXISTS`).

---

## Change D — Spaced Repetition Review (top priority) — IMPLEMENTED in v1.8.0

The highest-ROI learning feature: instead of seeing a word once, users re-encounter it
at growing intervals so it moves into long-term memory.

### Model

A lightweight SM-2-style scheduler per `(chat_id, word)`:

| Concept | Meaning |
|---|---|
| `interval` | Days until the next review (1 → 3 → 7 → 16 → 35 …) |
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
| `due_at` | `DATETIME` | Next review time |

PK `(chat_id, word)`. A word enters the schedule the first time it is sent (seeded with
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
re-sent before the user answers. The button tap routes through the `srs:` callback branch
(`handleReviewCallback`) which applies the promote/reset and reschedules from the user's
response. Timestamps are stored as UTC strings (`2006-01-02 15:04:05`) so lexicographic
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
  - "What does **<word>** mean?" → 4 meaning options
  - "Which word means *<meaning>*?" → 4 word options
  - "Pick the synonym of **<word>**" → 4 options
  - Fill-in-the-blank sentence with 4 word choices
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

**Implementation (v1.9.0):** `quiz.go` builds a multiple-choice question entirely
from pooled data — no AI call. Two question types are rotated: **word → meaning**
("What does WORD mean?") and **meaning → word** ("Which word means …?").
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

- Levels: `beginner`, `intermediate`, `advanced`.
- Stored on the `user_prefs` table (see below), defaulting to `intermediate`.
- The chosen level is injected into the drill/word prompt via `levelInstruction`
  (`generation.go`) as a CEFR-band directive (A1–A2 / B1–B2 / C1–C2).
- **Pool interaction (as implemented):** `content_pool` gained a `level` column.
  The `UNIQUE(kind, term)` constraint stays global (a term lives at one level), and
  `PoolTerms` de-dupes across all levels so the same word isn't regenerated. The
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
| `/level <beginner\|intermediate\|advanced>` | Set the level directly by argument. |

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

## Change I — `/audio` Pronunciation

Let learners *hear* a word, not just read its IPA.

- Send a Telegram **voice note** (`sendVoice`) or audio of the word (and optionally an
  example sentence).
- Source options (all have free tiers / are free):
  - Google Translate TTS endpoint (unofficial, free, simple GET)
  - A free/open TTS (e.g. Piper) run locally and uploaded as OGG/Opus
  - Provider TTS where available
- Cache generated audio by word (new `audio_cache(word, file_id)` table storing the
  Telegram `file_id` after first upload) so each word's audio is produced **once** and
  re-sent by `file_id` thereafter — zero repeat cost.

| Command | Behaviour |
|---|---|
| `/audio` | Send the pronunciation of the most recent word (or a `/audio <word>`). |

Each vocabulary card can also carry a "🔊 Hear it" inline button that triggers the same flow.

---

## Change J — Admin Metrics & Broadcast

Maintainer-only operational tooling (gated by `chat_id == MAINTAINER_CHAT_ID`).

| Command | Behaviour |
|---|---|
| `/metrics` | Subscriber count, active vs paused, pool depth per (kind, level), provider usage tallies, quiz volume, last refill time. |
| `/announce <text>` | Push a one-off HTML message to all (non-paused) subscribers. |
| `/health` | Quick check: which providers are enabled, DB reachable, last broadcast/refill timestamps. |

- Provider usage tallies come from a counter incremented in `generateWithFallback`
  (which provider served each request) — optionally persisted to a `provider_usage` table.
- All admin commands no-op (or reply "not authorized") for non-maintainer chat IDs.

---

## Change K — Weekly Digest

A Sunday-evening recap, natural extension of the daily review.

- A `weeklyDigestScheduler` goroutine fires once a week (configurable day/time, Tehran).
- Per user: the week's new words (word + meaning), quiz accuracy, streak, and a
  "word of the week" highlight.
- Respects quiet hours and the `paused` flag; idempotent via a
  `weekly_digest_delivery(chat_id, week_start)` table.

| Config | Default | Description |
|---|---|---|
| `DIGEST_DAY` | `Sunday` | Day of week to send the digest |
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
  (`📘 Word of the Hour …` → meaning, pronunciation, synonyms, opposites, examples).
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
| `audio_cache` | word → Telegram voice `file_id` | I |
| `activity` *(optional)* | per-day activity for streaks | G |
| `provider_usage` *(optional)* | per-provider call tallies | J |
| `weekly_digest_delivery` | digest idempotency | K |

## Telegram API additions (v3+)

| Need | API |
|---|---|
| Quiz answer buttons | inline keyboards + `callback_query` handling in the poller |
| Audio pronunciation | `sendVoice` (+ caching by `file_id`) |
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
7. **Change J (Admin metrics)** — operational visibility once the above add moving parts.
8. **Change I (Audio)** & **Change K (Weekly digest)** — polish features, ship last.
