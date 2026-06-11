# Mini App roadmap

Phased feature plan for the Telegram Mini App hub, ordered by
impact-per-effort. Companion to [DESIGN.md](DESIGN.md) — each phase closes
specific rows of its gap checklist (§9).

**Rules**

- One phase = one PR = one `Changelogs` entry. Any `webapp/` change needs a
  new version to redeploy (the SPA is `go:embed`-ed).
- **Phases 1–4 use `Silent: true` entries** — they ship quietly. The final
  phase ships a **non-silent** changelog announcing the whole arc (polish,
  sessions, Library, gamification, in-app practice) to users at once.
- Each PR updates DESIGN.md: tick the gaps it closes, spec anything new.
- New endpoints: wrap in `withUser`, document in DOCS.md, test via the
  `signInitData` + `apiCall` pattern in `webapp_api_test.go`.

**Sizing:** S ≈ <80 LOC · M ≈ 80–250 · L ≈ 250+.

---

## Phase 1 — Native-feel polish (frontend-only)

Highest impact-per-effort; zero Go changes; `Silent` changelog acceptable.

| Feature | Files | Size |
|---|---|---|
| Complete theme tokens (`--section`, `--accent-fg`, `--danger`, `--subtitle`, `--bottom-bar`, `--success`, `--border`), migrate hardcoded colors, `setBottomBarColor('secondary_bg_color')` on boot | `webapp/styles.css`, `webapp/app.js` | S |
| Haptics taxonomy: `selectionChanged()` on tabs/chips, `notificationOccurred('success'/'error')` on answers | `webapp/app.js` | S |
| `disableVerticalSwipes()` entering swipe views (re-enable on exit); `enableClosingConfirmation()` while a session has ≥1 answer | `webapp/app.js` | S |
| Skeleton loaders (`.skeleton` shimmer) replacing `Loading…` on Stats / Words / Ranks first paint | `webapp/styles.css`, `webapp/app.js`, `webapp/index.html` | M |
| Failure re-sync: swipe-answer retry queue (don't drop on failed POST), settings-toggle rollback + brief error hint | `webapp/app.js` | M |

## Phase 2 — Session & dashboard experience

| Feature | Files | Size |
|---|---|---|
| `MainButton` ("Knew it") + `SecondaryButton` ("Forgot", left) wired into `createSwipeSession`; hide on exit | `webapp/app.js` | M |
| Session completion screen: per-session known/forgot counts, progress ring, "come back tomorrow" framing | `webapp/app.js`, `webapp/styles.css` | M |
| GitHub-style activity heatmap (CSS grid, ~120 days) replacing Chart.js bars — extend `/api/stats` `activity_days` window; **drop the Chart.js CDN dependency** | `webapp.go` (S), `webapp/app.js`, `webapp/styles.css`, `webapp/index.html` | M |
| `CloudStorage`: restore last-active tab on boot, persist last leaderboard metric (`lastTab`, `ui.*`) | `webapp/app.js` | S |

## Phase 3 — Library tab: content history

First new tab + endpoint. Exposes five tables that currently have no UI:
`sent_idioms`, `sent_collocations`, `sent_stories`, `sent_tips`, and
per-attempt `quiz_results`.

| Feature | Files | Size |
|---|---|---|
| `GET /api/content?kind=idiom\|collocation\|story\|tip&offset&limit` reusing the `/api/vocab` pagination pattern; no schema changes | `webapp.go` | M |
| `GET /api/quizzes?offset&limit` — past attempts (word, correct, date) | `webapp.go` | S |
| Library view: kind chips, reuse `.list`/`.word` rows, story/long-text detail via `BackButton` drill-down | `webapp/index.html`, `webapp/app.js`, `webapp/styles.css` | M/L |
| API tests for both endpoints | `webapp_api_test.go` | S |

**Decision (v1.27.0):** no 6th tab and no Review demotion — the Words tab
became the **Library**: its filter chips grew to Words / Bookmarks / Idioms /
Collocations / Stories / Tips / Quizzes, reusing the list UI. DESIGN.md §5
updated.

## Phase 4 — Gamification & growth

| Feature | Files | Size |
|---|---|---|
| Leaderboard avatars: pass `photo_url` from validated initData through `withUser` → leaderboard rows, fallback to initials | `webapp.go`, `leaderboard.go`, `webapp/app.js`, `webapp/styles.css` | M |
| Weekly metric chip: leaderboard query windowed to the current week (keeps newcomers motivated vs all-time board) | `leaderboard.go`, `webapp.go`, `webapp/app.js` | S/M |
| `shareToStory` streak card at milestones (7/30/100 days) | `webapp/app.js` | M |
| `addToHomeScreen()` prompt at a 7-day streak (`checkHomeScreenStatus` first, once per user via `CloudStorage`) | `webapp/app.js` | S |

## Phase 5 — On-demand practice in-app

Biggest backend lift; brings chat-only features (`/quiz`, `/word`, `/idiom`,
`/collocation`) into the app.

| Feature | Files | Size |
|---|---|---|
| In-app quiz: `GET /api/quiz/next` + `POST /api/quiz/answer` reusing `quiz.go` question generation, writing `quiz_results` | `webapp.go`, `quiz.go`, `webapp/app.js`, `webapp/styles.css` | L |
| On-demand content: `GET /api/practice?kind=word\|idiom\|collocation` drawing from `content_pool` at the user's level, respecting `pool.go` accounting and recording to the `sent_*` tables | `webapp.go`, `pool.go`, `webapp/app.js` | L |
| Rate limiting on practice endpoints (align with the bot's hourly-limiter approach) | `webapp.go` | S |
| **Non-silent changelog** announcing all phases: native polish, smarter review sessions, the Library tab, leaderboard avatars & weekly ranks, and in-app practice | `main.go` (`Changelogs`) | S |

## Backlog / non-goals

- `DeviceStorage` as a localStorage replacement — revisit if CloudStorage
  limits bite.
- Offline audio cache via `downloadFile` — TTS stays chat-side for now.
- User-created decks — bundled `deckRegistry` decks only.
- Fullscreen mode — safe-area cost outweighs benefit for this app.

Reference implementation worth studying: **MemoCard**
(github.com/kubk/memo-card) — Telegram-contest-winning SRS flashcard Mini App
(decks, streak heatmap, reminder notifications).
