# Contributing to English Muscle Memory Bot

Thanks for your interest in contributing! This guide covers the development workflow and conventions used in this project.

## Prerequisites

- **Go 1.24+** (see `go.mod` for the exact version)
- A Telegram bot token (create one via [@BotFather](https://t.me/BotFather))
- At least one AI provider API key (Gemini is the easiest to get started)

## Getting Started

```bash
# Clone the repo
git clone https://github.com/your-org/english-bot.git
cd english-bot

# Copy and fill in your env vars
cp .env.example .env

# Build
go build -o english-bot .

# Run tests
go test ./... -v
```

## Project Structure

The codebase is a single Go package (`package main`) with 10 source files. Each file has a clear responsibility:

| File | Responsibility |
|---|---|
| `main.go` | Startup, schema, Telegram types, command router, core Store methods |
| `config.go` | Environment variable parsing, timezone, tuning knobs |
| `providers.go` | AI provider interface, implementations, fallback chain |
| `generation.go` | Prompt builders, content generation, term/meaning parsers |
| `pool.go` | Content pool Store methods, `serveContent`, pool filler goroutine |
| `schedule.go` | Broadcast, daily review, and weekly digest schedulers |
| `prefs.go` | User preferences Store methods (level, pause, interval) |
| `srs.go` | Spaced-repetition logic and review scheduler |
| `quiz.go` | Quiz building (4 types), quiz results, quiz scheduler |
| `stats.go` | Stats computation and admin metrics |

## Code Style

- **Formatting**: All code must pass `gofmt`. Run `gofmt -w *.go` before committing.
- **Vetting**: Run `go vet ./...` to catch common issues.
- **Single package**: Everything is in `package main`. This is intentional -- the app is small enough that package boundaries would add complexity without benefit.
- **Naming**: Follow standard Go conventions. Exported symbols get doc comments.
- **Error handling**: Use `errors.Is(err, sql.ErrNoRows)` for SQL no-rows checks (not string comparison).
- **Logging**: Use structured log prefixes like `[POOL]`, `[SRS]`, `[QUIZ]`, etc.

## Testing

Tests live alongside source files with `_test.go` or `_smoke_test.go` suffixes.

```bash
# Run all tests
go test ./... -v

# Run a specific test
go test -run TestParseVerb -v

# Run with race detector
go test -race ./...
```

### Writing Tests

- Use `openStore(t.TempDir() + "/test.db")` for tests that need a database
- Use table-driven tests for pure functions
- Call `t.Cleanup(func() { store.Close() })` instead of `defer` in test helpers
- When testing Store methods, call `store.AddSubscriber(chatID)` first to satisfy foreign key expectations

## Adding a New Feature

1. **Plan**: Create an issue or describe the feature in DOCS.md roadmap first
2. **Implement**: Add the code to the appropriate file (or create a new one if the responsibility is clearly distinct)
3. **Test**: Add tests in a `*_test.go` file
4. **Schema**: If adding a new table, add `CREATE TABLE IF NOT EXISTS` in `main.go`'s schema block. If adding a column to an existing table, add a migration in `Store.migrate()`
5. **Config**: If adding env vars, add them to `config.go` and `.env.example`
6. **Docs**: Update `DOCS.md` with technical details and `README.md` if user-facing
7. **Changelog**: Add a `ChangelogEntry` to the `Changelogs` slice in `main.go`

## Adding a New AI Provider

1. If OpenAI-compatible: add an entry to `buildProviders()` in `providers.go` -- just env var name, base URL, and default model
2. If non-standard: implement the `Provider` interface (`Name()`, `Enabled()`, `Generate()`)
3. Add the env vars to `.env.example`
4. Document in `README.md` provider table

## Deployment

### Docker (recommended)

```bash
cp .env.example .env
# Fill in your keys
docker compose up -d --build
```

The SQLite database is persisted in a Docker volume. To backup:

```bash
docker compose exec english-bot cp /data/subscribers.db /data/backup.db
docker cp english-bot:/data/backup.db ./backup.db
```

### Bare Metal

```bash
go build -o english-bot .
# Set env vars (via .env, export, or systemd EnvironmentFile)
./english-bot
```

## Commit Messages

- Use clear, concise commit messages
- Reference issue numbers when applicable
- Prefix with the area of change when helpful (e.g., `quiz: add fill-in-the-blank type`)

## Questions?

Open an issue or reach out to the maintainer.
