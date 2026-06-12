# CLAUDE.md — guide for AI sessions on english-bot

A Go Telegram bot that teaches English through AI-generated, spaced-repetition
practice (drills, words, idioms, tips, collocations, stories) plus a Telegram
Mini App hub. Laid out as a `cmd/english-bot` entrypoint plus `internal/`
packages — `config` (leaf), `ai`, `telegram`, `content`, and the coupled core
`app` (the old `package main`); dep graph is acyclic and nothing imports `app`.
SQLite via `modernc.org/sqlite` (pure Go, no CGO). Read `README.md` (user-facing)
and `DOCS.md` (deep reference) — they are kept in sync with the code and **CI
enforces it** (see Testing).

## Build / test / run

- `go build ./...`, `go vet ./...`, `go test ./...`, `golangci-lint run ./...`
  — all must pass before a PR. The `Makefile` wraps these (`make build/test/lint/fmt`).
  The binary is built from `./cmd/english-bot`.
- Tests use temp SQLite (`testStoreHelper`) and never hit the network
  (`emptyProviderChain`/`mockProviderChain`, `mockNotifier`). No token needed.
- Keep code gofmt/goimports-clean (the linter enforces it via its formatters).

## Conventions

- Per-user data is keyed by `chat_id`. Vocabulary lives in `sent_vocab`, drills
  in `sent_words`, SRS in `review_schedule`, prefs in `user_prefs`.
- Content is AI-generated via `ai.ProviderChain` (`internal/ai/providers.go`),
  pooled in `content_pool` (level-partitioned), and topped up by `poolFiller`
  (`internal/app/pool.go`). Prompts/parsers live in `internal/content`.
- New DB tables go in the `CREATE TABLE IF NOT EXISTS` block (runs every start);
  new **columns** also need an additive `ALTER TABLE` in `migrate()`
  (`internal/app/app.go`).
- Telegram API calls go through `telegram.Post(method, payload)`; the Telegram
  DTOs and `Notifier` live in `internal/telegram`. Env-sourced config (tokens,
  tuning knobs, kind/level constants) lives in `internal/config`.
- `Changelogs` (`internal/app/changelog.go`) is append-only — **CI greps this
  file** for the version to auto-tag. `Silent: true` for internal/unreleased
  work; user-facing features get a catchy non-silent note. Each version must be
  unique semver; non-silent entries need non-empty `Text` (CI-checked).
- Maintainer-only commands are gated by `isMaintainer(chatID)` and hidden from
  `/help` + `registerBotCommands`.

## Mini App hub (`internal/app/{webapp,leaderboard,leitner}.go`, `internal/app/webapp/`)

- Frontend is a vanilla-JS SPA embedded via `//go:embed webapp` (in
  `internal/app/webapp.go`) — files in `internal/app/webapp/`
  (`index.html`, `app.js`, `styles.css`) and decks in
  `internal/app/webapp/decks/*.json`. No build step, no framework. UI conventions live in
  `UI/DESIGN.md` (design system, update it with UI PRs) and the feature
  roadmap in `UI/ROADMAP.md`.
- Every `/api/*` handler is wrapped by `withUser`, which validates Telegram
  `initData` (HMAC-SHA256) **and** an `auth_date` freshness TTL (`initDataTTL`),
  resolving the `chatID`. The frontend sends initData in the `X-Init-Data`
  header (server also accepts `?initData=`). Reuse `withUser` for any new endpoint.
- API: `/api/config` (public: bot handle + web app URL), `/api/stats`,
  `/api/vocab`, `/api/bookmark`, `/api/leaderboard`(+`/name`; metrics
  words/mastered/weekly/today; **no profile photos** — privacy; rows carry an
  opaque `id`), `/api/profile?id=` (head-to-head stat comparison + their heatmap)
  and `/api/kudos` (toggle 👏; notifies the recipient unless self-paced) — both
  address users by the opaque `public_id`, **never the `chat_id`**,
  `/api/review/next`+`/answer`+`/summary` (cards carry pronunciation/persian/example;
  `/summary` proposes a harder/easier level from the rolling review-perf window —
  throttled, fires after a sustained run, never every batch),
  `/api/decks`(+`/study`,`/swipe`,`/detail`), `/api/settings`
  (GET reads prefs, POST applies one `{key,value}` via existing setters),
  `/api/content?kind=` (Library history: idiom/collocation/story/tip),
  `/api/quizzes` (quiz attempt history), `/api/vocab/card?term=` (word detail),
  `/api/practice?kind=` (pool-only on-demand card, rate-limited),
  `/api/quiz/next`+`/answer` (in-app quiz, HMAC-token stateless),
  `/api/grammar`(+`/lesson?id=`) (static grammar lessons).
- Tabs: Stats · Library · Study (decks + on-demand practice + grammar) · Review
  · Ranks; Settings via the native Telegram `SettingsButton`. Drill-downs use
  `BackButton`. The app is the chat's persistent menu button
  (`setChatMenuButton` on startup) and opens via `/app`.
- **Adding a deck:** drop `internal/app/webapp/decks/<id>.json`
  (`[{term, definition, example, group, persian?, pronunciation?, mnemonic?}]`)
  and add a `deckRegistry` entry in `internal/app/leitner.go`. `SeedDecks` ingests it
  idempotently (UPSERT fills blank columns from JSON without clobbering
  backfilled values); blank `example`/`persian`/`pronunciation` are filled by
  `runDeckBackfill` (`backfillOneDeckField`, one field per 30s). Leitner: 5
  boxes, known→+1 (0/1/3/7/21d), miss→box 1.
- Bundled decks: **504 Absolutely Essential Words** (def+example+Persian, from
  github.com/ashkan-jafarzadeh/504-essential-words), **Barron's GRE 333**
  (def+mnemonic; Persian/pronunciation AI-backfilled), plus curated **Phrasal
  Verbs**, **Business English**, **Academic Word List**, **IELTS/TOEFL** decks.
- **Grammar lessons** (`internal/app/grammar.go`, `internal/app/webapp/grammar/lessons.json`): a static,
  pre-authored curriculum (no AI at request time) served to `/grammar` and the
  Study tab. Lessons have `{pattern, explanation, examples, tip, practice}`;
  practice MCQs are scored client-side. `BOT_USERNAME` env (default
  `@mymusclememorybot`) feeds the share link via `/api/config`.

## Testing & docs sync (CI-enforced — `internal/docsync/doc_test.go`)

The doc-sync test locates the repo root via `go.mod` and walks `cmd/`+`internal/`,
so it is layout-independent. It enforces:
- Every `case "/cmd"` routed in `internal/app` must appear in **both** README.md and DOCS.md.
- Every non-test `*.go` file under `cmd/`+`internal/` must be listed (by basename)
  in the README **and** DOCS architecture blocks.
- Callback prefixes routed in `handleCallback` must be documented in DOCS.md.
- Add tests for new features (see `internal/app/webapp_api_test.go` for the initData-signing
  helper `signInitData` + `apiCall` pattern used to test authed endpoints).

## Git / PR workflow

- Feature branch off `master` → PR via the GitHub REST API using `GH_TOKEN`
  (repo: `Dawoodkhorsandi/english-bot`) → poll check-runs → squash-merge →
  `git pull --ff-only` → delete branch local+remote.
- Commit trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Only commit/push when asked.

## Deployment (the gotchas — learned the hard way)

Prod is **live at https://bot.mardeen.ir** (Mini App hub shipped in v1.24.0).
Runs in Docker on a VPS; HTTPS terminates on an external proxy → nginx:80 →
`english-bot:8090`. Merging to `master` auto-deploys: `auto-tag.yml` tags the
newest `Changelogs` version (so a docs-only change with no new entry does **not**
redeploy), and the tag triggers `release.yml` → `deploy.yml`. The deploy can also
be run manually via `workflow_dispatch`. Gotchas:

1. **`WEB_APP_URL` must be set** or the web server never starts → reverse proxy
   returns **502** *and* `/stats` shows no button. It must be `https://`.
2. The prod `.env` is written from a **single GitHub secret named `ENV_FILE`** —
   individual repo secrets (e.g. a separate `WEB_APP_URL`) are **ignored**. Put
   new env vars *inside* `ENV_FILE`.
3. The deploy generates its own `docker-compose.yml` and **must include the nginx
   service publishing `:80`** + `nginx.conf` (added in PR #34) — otherwise nothing
   is reachable on the host.
4. Deploy uses `docker compose up -d --force-recreate` (PR #33) so `.env` changes
   actually take effect (plain `up -d` keeps the old container/env).
5. The nginx container publishes host port **:80** — if another service already
   holds `:80` on the VPS it will fail to start; check `docker logs english-bot-nginx`.

## Useful facts

- Levels: `beginner`, `intermediate` (default), `upper-intermediate`, `advanced`
  (`allLevels`, `normalizeLevel`, `levelLabel` in `prefs.go`).
- Mastered threshold: `srsMasteredIntervalDays = 21`.
- **Streak counts all learning** (`activityDays` in `stats.go`): delivered words/drills
  (`sent_words`/`sent_vocab`) *plus* pull-based learning (in-app reviews, deck study, quiz
  answers) recorded via `RecordActivity` into the `activity_log` table. Add `RecordActivity`
  to any new learning action that has no `sent_*` footprint.
- **Self-paced mode** reuses the `paused` pref (a master kill-switch every scheduler honours);
  the webapp surfaces it as "Self-paced mode — silence all automatic messages". On-demand
  `/api/practice` still seeds reviews, so the pull-based loop works with all pushes off.
- **Level suggestion**: review answers feed a recency-weighted window in `review_perf`
  (`RecordReviewOutcome`); `LevelSuggestion` proposes a level change only past
  `reviewPerfMinSample` answers and a `reviewSuggestCooldown` gap, then resets the window.
- Maintainer commands: `/metrics /poolusage /health /config /users /announce
  /backup /admin` (see `/admin` for the live list).
