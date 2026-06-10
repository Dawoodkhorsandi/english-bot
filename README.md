# English Muscle Memory Bot

[![CI](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/Dawoodkhorsandi/bf1536b78d62b052a98f2d090b29b011/raw/coverage-badge.json)](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A Telegram bot that sends subscribers AI-generated English practice on a configurable schedule, alternating between grammar drills and vocabulary cards. Built with Go and SQLite.

## Features

- **Grammar Drills** -- one verb across 21 forms (12 tenses, all 4 conditionals, modals, passive, imperative, *used to*), delivered as five navigable pages with ◀️/▶️ buttons
- **Vocabulary Cards** -- meaning, pronunciation, synonyms, opposites, examples, and a hidden Persian/Farsi definition (tap to reveal)
- **Pronunciation Audio** -- best-effort voice note after each vocabulary card and idiom (Gemini TTS with espeak-ng fallback, per-user `/tts` toggle)
- **Idiom of the Day** -- a common English idiom with meaning and examples, daily and on demand via `/idiom`
- **Collocation of the Day** -- a natural word partnership (e.g. *make a decision*) with examples and common-mistake warnings, daily and on demand via `/collocation`
- **Mini Stories** -- a short reading-practice story at the user's level with key vocabulary and a comprehension question, daily and on demand via `/story`
- **Spaced Repetition (SRS)** -- SM-2-style review scheduler resurfaces words at growing intervals so you remember what you've learned
- **Native Quiz Polls** -- Telegram's built-in quiz poll format; tap an answer and the correct one is highlighted instantly; four question types (word-to-meaning, meaning-to-word, synonym, fill-in-the-blank)
- **Weekly Digest** -- Sunday evening recap of the week's words, quiz accuracy, and streaks
- **Per-User Settings** -- `/setup` quick-setup wizard (5 time-budget presets) + `/settings` hub to toggle every feature individually; difficulty level, send interval, quiz frequency, and all daily features are per-user
- **Streak Celebrations** -- personalised congratulations at 3, 7, 14, 30, and 60-day streaks
- **Reply Keyboard** -- four persistent shortcut buttons (Word / Drill / Quiz / Stats) always visible at the bottom of the chat
- **Progress Dashboard** -- `/stats` shows Unicode progress bars for streak and quiz accuracy; optional Telegram Mini App with a 30-day activity chart (set `WEB_APP_URL`)
- **Typing Indicator** -- "typing…" chat action fires before every AI generation call
- **Multi-Provider AI** -- Gemini, Groq, Cerebras, OpenRouter, GitHub Models, Cloudflare, Mistral, Gemini2, SambaNova, Cohere with automatic fallback
- **Pre-Generated Pool** -- background worker keeps a content pool topped up; broadcasts never block on AI calls
- **Quiet Hours** -- no broadcasts during configurable overnight window (default 00:00--09:00 Tehran)
- **Daily Review** -- compact bedtime recap of the day's vocabulary at midnight
- **Admin Tools** -- `/metrics`, `/poolusage` (per-pool consumption %), `/health`, `/announce`, `/users` (paginated user list with detail view and direct messaging), `/config` (runtime settings panel) gated by maintainer chat ID

## Quick Start

### Prerequisites

- Go 1.24+ (or Docker)
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- At least one AI provider API key (Gemini recommended)

### Run Locally

```bash
# Clone the repo
git clone https://github.com/Dawoodkhorsandi/english-bot.git
cd english-bot

# Configure
cp .env.example .env
# Edit .env -- set TELEGRAM_BOT_TOKEN, GEMINI_API_KEY, MAINTAINER_CHAT_ID

# Build and run
go build -o english-bot .
./english-bot
```

The bot creates a `subscribers.db` SQLite file in the working directory.

### Run with Docker

```bash
cp .env.example .env
# Edit .env with your keys

docker compose up -d --build
```

The SQLite database is persisted in a named Docker volume (`bot-data`).

### Run Tests

```bash
go test ./... -v
```

## Bot Commands

### User Commands

| Command | Description |
|---|---|
| `/start` | Subscribe and receive a welcome message + quick-access keyboard |
| `/setup` | Quick-setup wizard — pick your daily time budget (5 min / 15 min / 30 min / 1 hr / 2 hr); bot auto-configures all settings |
| `/settings` | View and toggle all settings in one place |
| `/drill` | Get a grammar drill on demand |
| `/word` | Get a vocabulary card on demand |
| `/idiom` | Get an idiom with meaning and examples |
| `/collocation` | Get a collocation (natural word partnership) with examples |
| `/story` | Get a mini story to read at your level |
| `/tip` | Get a grammar tip now; `/tip on` or `/tip off` to control daily tips |
| `/quiz` | Take a multiple-choice quiz (native Telegram poll) |
| `/stats` | View progress: streak, words, quiz accuracy with progress bars |
| `/mywords` | Browse all learned vocabulary with mastery status; `/mywords bookmarks` for bookmarked only |
| `/bookmark [word]` | Toggle bookmark on a word, or view bookmarks (no argument) |
| `/level [beginner\|intermediate\|upper-intermediate\|advanced]` | Set difficulty level |
| `/interval [minutes]` | Set send frequency (30/60/120/180/240/360/480/720) |
| `/tts [on\|off]` | Toggle pronunciation audio on or off |
| `/pause` | Pause scheduled sends (on-demand still works) |
| `/resume` | Resume scheduled sends |
| `/reset` | Clear all word/drill history |
| `/help` | Show usage instructions |
| *(any word)* | Look up a specific word (English or Farsi) |

### Admin Commands

Gated by `MAINTAINER_CHAT_ID` -- other users see "not authorized".

| Command | Description |
|---|---|
| `/metrics` | Subscriber stats, pool depth, quiz volume, mastered count |
| `/poolusage` | Per kind/level, the most active user's pool consumption as a percentage (spot pools nearing exhaustion) |
| `/announce <text>` | Broadcast an HTML message to all active subscribers |
| `/health` | List enabled AI providers |
| `/users` | Paginated user list with inline buttons; tap any user to see full detail (settings, toggles, progress, quiz accuracy, SRS state, streaks); send a direct message to any user from the detail view |
| `/backup` | Send a point-in-time SQLite backup file to the maintainer chat immediately |
| `/config` | Interactive panel to tweak runtime bot settings (global pool target/min, per-kind and per-level pool-size overrides, quiet hours, TTS, gen spacing, review batch); persisted across restarts |

## Configuration

All configuration is via environment variables. See [`.env.example`](.env.example) for the full list.

### Required

| Variable | Description |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Telegram Bot API token from @BotFather |
| `MAINTAINER_CHAT_ID` | Your Telegram chat ID (receives join notifications + admin commands) |
| At least one AI key | `GEMINI_API_KEY`, `GROQ_API_KEY`, etc. |

### Optional

| Variable | Default | Description |
|---|---|---|
| `TIMEZONE` | `Asia/Tehran` | IANA timezone for scheduling |
| `QUIET_START` | `00:00` | Start of no-broadcast window (local time) |
| `QUIET_END` | `09:00` | End of no-broadcast window (local time) |
| `TTS_ENABLED` | `true` | Global kill-switch for pronunciation audio |
| `POOL_TARGET` | `300` | Desired pooled items per kind for the default level |
| `POOL_MIN` | `100` | Pool target for non-default levels |
| `REFILL_INTERVAL` | `20s` | How often the filler checks the pool |
| `GEN_SPACING` | `3s` | Minimum gap between AI calls |
| `REVIEW_CHECK_INTERVAL` | `1h` | How often the SRS scheduler scans for due reviews |
| `REVIEW_BATCH_MAX` | `3` | Max review reminders per user per scan |
| `QUIZ_INTERVAL` | `6h` | Scheduled quiz frequency (`0` to disable) |
| `DIGEST_DAY` | `Sunday` | Day of week for weekly digest (`off` to disable) |
| `DIGEST_TIME` | `20:00` | Local time to send the weekly digest |
| `IDIOM_TIME` | `09:00` | Local time to send the daily idiom (`off` to disable) |
| `COLLOCATION_TIME` | `13:00` | Local time to send the daily collocation (`off` to disable) |
| `STORY_TIME` | `17:00` | Local time to send the daily mini story (`off` to disable) |
| `BACKUP_TIME` | `02:00` | Local time to send nightly SQLite backup to the maintainer (`off` to disable) |
| `AI_PROVIDER_ORDER` | `gemini,groq,...` | Comma-separated provider priority |
| `WEB_APP_URL` | *(unset)* | Public HTTPS URL of the bot server (e.g. `https://bot.example.com`). When set, `/stats` includes a "📊 Full Dashboard" button opening the Telegram Mini App stats page. Leave unset to disable. |
| `WEB_APP_PORT` | `8090` | TCP port for the embedded Mini App HTTP server (only used when `WEB_APP_URL` is set). |

## Architecture

```
Go application (single package main, 14 source files)
+-- main.go           -- startup, schema, Telegram types, command router, Store
+-- config.go         -- timezone, pool & scheduler tuning knobs, WEB_APP_URL/PORT
+-- providers.go      -- Provider interface, GeminiProvider, OpenAICompatProvider, ProviderChain
+-- generation.go     -- prompt builders, AI generation, term/meaning parsers
+-- pool.go           -- content_pool Store methods, serveContent, poolFiller
+-- schedule.go       -- quiet hours, broadcast/daily-review/weekly-digest schedulers
+-- prefs.go          -- user_prefs Store methods, level/interval/pause/toggle helpers, first_name/streak_celebrated
+-- tts.go            -- pronunciation TTS (Gemini + espeak-ng fallback), audio cache
+-- srs.go            -- spaced-repetition SM-2-lite logic, review scheduler
+-- quiz.go           -- quiz building (4 types + native polls), poll registry, quiz scheduler
+-- stats.go          -- /stats computation (progress bars), admin metrics
+-- admin.go          -- admin panel: paginated /users, user detail, direct messaging
+-- webapp.go         -- optional Mini App HTTP server, HMAC initData validation, /api/stats
+-- vocab.go          -- /mywords (browse learned words) and /bookmark (favourites) features
```

### Goroutines (13 concurrent + optional web server)

1. **Pool filler** -- background content generation
2. **Broadcast scheduler** -- half-hourly, per-user interval-aware delivery
3. **Daily review scheduler** -- bedtime word recap at midnight
4. **Spaced-repetition scheduler** -- hourly scan for due reviews
5. **Quiz scheduler** -- periodic multiple-choice quizzes (native Telegram polls)
6. **Weekly digest scheduler** -- weekly recap (default Sunday 20:00)
7. **Idiom-of-the-day scheduler** -- daily idiom at `IDIOM_TIME` (default 09:00)
8. **Daily tip scheduler** -- daily grammar tip at `TIP_TIME` (default 10:00)
9. **Collocation scheduler** -- daily collocation at `COLLOCATION_TIME` (default 13:00)
10. **Mini story scheduler** -- daily reading-practice story at `STORY_TIME` (default 17:00)
11. **Nightly backup scheduler** -- SQLite snapshot to the maintainer at `BACKUP_TIME` (default 02:00)
12. **Telegram poller** -- long-polls for messages, callbacks, and `poll_answer` updates
13. **Main goroutine** -- blocks on OS signal for graceful shutdown
14. **Mini App web server** *(optional)* -- serves the stats dashboard when `WEB_APP_URL` is set

### Database

SQLite via `modernc.org/sqlite` (pure Go, no CGO). Tables: `subscribers`, `sent_words`, `sent_vocab`, `sent_idioms`, `sent_tips`, `sent_collocations`, `sent_stories`, `changelog_delivery`, `content_pool`, `daily_review_delivery`, `idiom_delivery`, `daily_tip_delivery`, `collocation_delivery`, `story_delivery`, `user_prefs`, `review_schedule`, `quiz_results`, `weekly_digest_delivery`, `audio_cache`, `bookmarks`, `bot_config`.

### AI Providers

The bot tries providers in order and uses the first that responds. A `429` causes immediate failover. Per-provider retry: 2 attempts with exponential backoff.

| Provider | Env Var | Default Model |
|---|---|---|
| Gemini | `GEMINI_API_KEY` | `gemini-2.5-flash` |
| Groq | `GROQ_API_KEY` | `llama-3.3-70b-versatile` |
| Cerebras | `CEREBRAS_API_KEY` | `llama-3.3-70b` |
| OpenRouter | `OPENROUTER_API_KEY` | `meta-llama/llama-3.3-70b-instruct:free` |
| GitHub Models | `GITHUB_MODELS_TOKEN` | `openai/gpt-4o-mini` |
| Cloudflare | `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` | `@cf/meta/llama-3.1-8b-instruct` |
| Mistral | `MISTRAL_API_KEY` | `mistral-small-latest` |
| Gemini2 | `GEMINI_API_KEY` | `gemini-2.0-flash` |
| SambaNova | `SAMBANOVA_API_KEY` | `Meta-Llama-3.3-70B-Instruct` |
| Cohere | `COHERE_API_KEY` | `command-r` |

## Project Structure

```
english-bot/
+-- .env.example          # All environment variables with descriptions
+-- .gitignore
+-- .dockerignore
+-- Dockerfile             # Multi-stage Alpine build (no CGO)
+-- docker-compose.yml     # One-command deployment with volume persistence
+-- go.mod
+-- go.sum
+-- README.md              # This file -- setup, usage, architecture overview
+-- DOCS.md                # Deep technical documentation (internals, data flows, roadmap)
+-- CONTRIBUTING.md        # How to contribute
+-- LICENSE                # MIT License
+-- *.go                   # Source files (package main)
+-- *_test.go / *_smoke_test.go  # Test files
```

## Documentation

- **[README.md](README.md)** -- Setup, usage, configuration (you are here)
- **[DOCS.md](DOCS.md)** -- Deep technical docs: data flows, prompt design, scheduler internals, schema details, roadmap
- **[CONTRIBUTING.md](CONTRIBUTING.md)** -- Development workflow and contribution guidelines

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
