# CLAUDE.md — guide for AI sessions on english-bot

A Go Telegram bot that teaches English through AI-generated, spaced-repetition
practice (drills, words, idioms, tips, collocations, stories) plus a Telegram
Mini App hub. Single `package main`, SQLite via `modernc.org/sqlite` (pure Go,
no CGO). Read `README.md` (user-facing) and `DOCS.md` (deep reference) — they are
kept in sync with the code and **CI enforces it** (see Testing).

## Build / test / run

- `go build ./...`, `go vet ./...`, `go test ./...` — all must pass before a PR.
- Tests use temp SQLite (`testStoreHelper`) and never hit the network
  (`emptyProviderChain`/`mockProviderChain`, `mockNotifier`). No token needed.
- Pre-existing `gofmt -l` noise: `pool.go`, `handler_test.go`, `prefs.go`,
  `quiz.go` are unformatted on `master` — leave them; keep *new* code gofmt-clean.

## Conventions

- Per-user data is keyed by `chat_id`. Vocabulary lives in `sent_vocab`, drills
  in `sent_words`, SRS in `review_schedule`, prefs in `user_prefs`.
- Content is AI-generated via `ProviderChain` (`providers.go`), pooled in
  `content_pool` (level-partitioned), and topped up by `poolFiller` (`pool.go`).
- New DB tables go in the `CREATE TABLE IF NOT EXISTS` block (runs every start);
  new **columns** also need an additive `ALTER TABLE` in `migrate()` (`main.go`).
- Telegram API calls go through `telegramPost(method, payload)`.
- `Changelogs` (`main.go`) is append-only. `Silent: true` for internal/unreleased
  work; user-facing features get a catchy non-silent note. Each version must be
  unique semver; non-silent entries need non-empty `Text` (CI-checked).
- Maintainer-only commands are gated by `isMaintainer(chatID)` and hidden from
  `/help` + `registerBotCommands`.

## Mini App hub (`webapp.go`, `webapp/`, `leaderboard.go`, `leitner.go`)

- Frontend is a vanilla-JS SPA embedded via `//go:embed webapp` — files in
  `webapp/` (`index.html`, `app.js`, `styles.css`) and decks in
  `webapp/decks/*.json`. No build step, no framework. UI conventions live in
  `UI/DESIGN.md` (design system, update it with UI PRs) and the feature
  roadmap in `UI/ROADMAP.md`.
- Every `/api/*` handler is wrapped by `withUser`, which validates Telegram
  `initData` (HMAC-SHA256) **and** an `auth_date` freshness TTL (`initDataTTL`),
  resolving the `chatID`. The frontend sends initData in the `X-Init-Data`
  header (server also accepts `?initData=`). Reuse `withUser` for any new endpoint.
- API: `/api/stats`, `/api/vocab`, `/api/bookmark`, `/api/leaderboard`(+`/name`),
  `/api/review/next`+`/answer`, `/api/decks`(+`/study`,`/swipe`), `/api/settings`
  (GET reads prefs, POST applies one `{key,value}` via existing setters),
  `/api/content?kind=` (Library history: idiom/collocation/story/tip),
  `/api/quizzes` (quiz attempt history), `/api/vocab/card?term=` (word detail),
  `/api/practice?kind=` (pool-only on-demand card, rate-limited),
  `/api/quiz/next`+`/answer` (in-app quiz, HMAC-token stateless).
- Tabs: Stats · Library · Decks · Review · Ranks; Settings via the native
  Telegram `SettingsButton`. Drill-downs use `BackButton`. The app is the chat's
  persistent menu button (`setChatMenuButton` on startup) and opens via `/app`.
- **Adding a deck:** drop `webapp/decks/<id>.json`
  (`[{term, definition, example, group}]`) and add a `deckRegistry` entry in
  `leitner.go`. `SeedDecks` ingests it idempotently; blank `example`s are filled
  by `runDeckBackfill`. Leitner: 5 boxes, known→+1 (0/1/3/7/21d), miss→box 1.
- Bundled decks: **504 Absolutely Essential Words** (full 504, def+example, from
  github.com/ashkan-jafarzadeh/504-essential-words, pipe-delimited CSV) and
  **Barron's GRE 333** (def only; examples AI-backfilled).

## Testing & docs sync (CI-enforced — `doc_test.go`)

- Every `case "/cmd"` in `main.go` must appear in **both** README.md and DOCS.md.
- Every non-test `*.go` file must be listed in the README **and** DOCS
  architecture blocks.
- Callback prefixes routed in `handleCallback` must be documented in DOCS.md.
- Add tests for new features (see `webapp_api_test.go` for the initData-signing
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
- Maintainer commands: `/metrics /poolusage /health /config /users /announce
  /backup /admin` (see `/admin` for the live list).
