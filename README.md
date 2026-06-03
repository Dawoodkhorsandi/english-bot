# English Muscle Memory Bot

[![CI](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-62.8%25-yellow)](https://github.com/Dawoodkhorsandi/english-bot/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A Telegram bot that sends subscribers AI-generated English practice every 30 minutes, alternating between grammar drills and vocabulary cards. Built with Go and SQLite.

## Features

- **Grammar Drills** -- one verb conjugated across 14 English tenses with example sentences
- **Vocabulary Cards** -- meaning, pronunciation, synonyms, opposites, and examples
- **Spaced Repetition** -- SM-2-style review scheduler resurfaces words at growing intervals
- **Quizzes** -- four question types (word-to-meaning, meaning-to-word, synonym, fill-in-the-blank)
- **Weekly Digest** -- Sunday evening recap of the week's words, quiz accuracy, and streaks
- **Per-User Settings** -- difficulty level (beginner/intermediate/advanced), send interval, pause/resume
- **Multi-Provider AI** -- Gemini, Groq, Cerebras, OpenRouter, GitHub Models, Cloudflare, Mistral with automatic fallback
- **Pre-Generated Pool** -- background worker keeps a content pool topped up; broadcasts never block on AI calls
- **Quiet Hours** -- no broadcasts during configurable overnight window (default 00:00--09:00 Tehran)
- **Daily Review** -- compact bedtime recap of the day's vocabulary at midnight
- **Admin Tools** -- `/metrics`, `/health`, `/announce` gated by maintainer chat ID

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
| `/start` | Subscribe and receive a welcome message |
| `/drill` | Get a grammar drill on demand |
| `/word` | Get a vocabulary card on demand |
| `/quiz` | Take a multiple-choice quiz |
| `/stats` | View your progress (words learned, streak, quiz accuracy, mastered) |
| `/level [beginner\|intermediate\|advanced]` | Set difficulty level |
| `/interval [minutes]` | Set send frequency (30/60/120/180/240/360/480/720) |
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
| `/announce <text>` | Broadcast an HTML message to all active subscribers |
| `/health` | List enabled AI providers |

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
| `POOL_TARGET` | `30` | Desired pooled items per kind for the default level |
| `POOL_MIN` | `10` | Pool target for non-default levels |
| `REFILL_INTERVAL` | `20s` | How often the filler checks the pool |
| `GEN_SPACING` | `3s` | Minimum gap between AI calls |
| `REVIEW_CHECK_INTERVAL` | `1h` | How often the SRS scheduler scans for due reviews |
| `REVIEW_BATCH_MAX` | `3` | Max review reminders per user per scan |
| `QUIZ_INTERVAL` | `6h` | Scheduled quiz frequency (`0` to disable) |
| `DIGEST_DAY` | `Sunday` | Day of week for weekly digest (`off` to disable) |
| `DIGEST_TIME` | `20:00` | Local time to send the weekly digest |
| `AI_PROVIDER_ORDER` | `gemini,groq,...` | Comma-separated provider priority |

## Architecture

```
Go application (single package main, 10 source files)
+-- main.go           -- startup, schema, Telegram types, command router, Store
+-- config.go         -- timezone, pool & scheduler tuning knobs
+-- providers.go      -- Provider interface, GeminiProvider, OpenAICompatProvider, ProviderChain
+-- generation.go     -- prompt builders, AI generation, term/meaning parsers
+-- pool.go           -- content_pool Store methods, serveContent, poolFiller
+-- schedule.go       -- quiet hours, broadcast/daily-review/weekly-digest schedulers
+-- prefs.go          -- user_prefs Store methods, level/interval/pause helpers
+-- srs.go            -- spaced-repetition SM-2-lite logic, review scheduler
+-- quiz.go           -- quiz building (4 types), quiz_results, quiz scheduler
+-- stats.go          -- /stats computation, admin metrics
```

### Goroutines (8 concurrent)

1. **Pool filler** -- background content generation
2. **Broadcast scheduler** -- half-hourly, per-user interval-aware delivery
3. **Daily review scheduler** -- bedtime word recap at midnight
4. **Spaced-repetition scheduler** -- hourly scan for due reviews
5. **Quiz scheduler** -- periodic multiple-choice quizzes
6. **Weekly digest scheduler** -- weekly recap (default Sunday 20:00)
7. **Telegram poller** -- long-polls for messages and callbacks
8. **Main goroutine** -- blocks on OS signal for graceful shutdown

### Database

SQLite via `modernc.org/sqlite` (pure Go, no CGO). Tables: `subscribers`, `sent_words`, `sent_vocab`, `changelog_delivery`, `content_pool`, `daily_review_delivery`, `user_prefs`, `review_schedule`, `quiz_results`, `weekly_digest_delivery`.

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
