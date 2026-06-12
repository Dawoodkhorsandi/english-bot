# Mini App design system

> **This documents the app as it SHOULD be — the target state.** Items marked
> **`GAP`** are not yet implemented; every gap maps to a phase in
> [ROADMAP.md](ROADMAP.md). When a PR changes the UI, update this doc (and tick
> off gaps) in the same PR.

The doc governs the Telegram Mini App frontend only: `webapp/index.html`,
`webapp/app.js`, `webapp/styles.css`, served by `webapp.go` via `go:embed`.
Hard constraint: **vanilla JS, no framework, no build step** — anything that
needs a bundler is out.

---

## 1. Principles

**Do**

- Derive every color from `--tg-theme-*` variables via the semantic tokens in
  §2 — the app must look right in any Telegram theme, light or dark.
- Feel native: Telegram `BackButton` for drill-downs, `SettingsButton` for
  settings, haptics on every meaningful interaction (§6). Native
  `MainButton`/`SecondaryButton` are NOT used for session answers (tried,
  reverted — they render below the tab bar; see §4 swipe session).
- Optimistic UI **with rollback**: update the UI immediately, revert on a
  failed POST. The bookmark star (`wordRow()` in `app.js`) is the canonical
  example — it flips back on error. All mutations follow it: settings
  toggles/level/interval roll back, swipe answers re-queue (✅ v1.25.0).
- Reuse `createSwipeSession(container, opts)` for any card-based flow.
- `esc()` every interpolated string — user names, AI-generated content, all of it.
- Every screen has explicit loading / empty / error states with actionable copy (§4).

**Don't**

- No pull-to-refresh — it conflicts with Telegram's swipe-to-close gesture.
  Tabs reload on every switch (`showView`) instead.
- No horizontal swipe targets hugging the screen edges (iOS edge-swipe
  conflict); the swipe card is inset by body padding.
- No hardcoded hex colors outside the token block in `styles.css`
  (✅ v1.25.0 — see §2).
- No custom in-page back buttons or bottom sheets where native chrome exists.
- Don't drop a swiped card from the queue before its POST is durable —
  `commit()` re-queues the card once on a failed `onAnswer` (✅ v1.25.0).
- No new CDN dependencies without strong cause; prefer removing them
  (Chart.js is slated for removal — see ROADMAP Phase 2).

## 2. Theme tokens

All colors live in the `:root` block of `styles.css` and map Telegram theme
variables to semantic app tokens. Components reference only the app tokens.

| Telegram variable | App token | Fallback | Status |
|---|---|---|---|
| `--tg-theme-bg-color` | `--bg` | `#fff` | ✅ exists |
| `--tg-theme-text-color` | `--text` | `#111` | ✅ exists |
| `--tg-theme-secondary-bg-color` | `--card` | `#f5f5f5` | ✅ exists |
| `--tg-theme-hint-color` | `--hint` | `#888` | ✅ exists |
| `--tg-theme-button-color` | `--accent` | `#2196F3` | ✅ exists |
| `--tg-theme-button-text-color` | `--accent-text` | `#fff` | ✅ exists |
| `--tg-theme-link-color` | `--link` | `#2196F3` | ✅ exists |
| `--tg-theme-section-bg-color` | `--section` | `var(--card)` | ✅ v1.25.0 |
| `--tg-theme-accent-text-color` | `--accent-fg` | `var(--accent)` | ✅ v1.25.0 |
| `--tg-theme-destructive-text-color` | `--danger` | `#f44336` | ✅ v1.25.0 |
| `--tg-theme-subtitle-text-color` | `--subtitle` | `var(--hint)` | ✅ v1.25.0 |
| `--tg-theme-bottom-bar-bg-color` | `--bottom-bar` | `var(--bg)` | ✅ v1.25.0 |
| — (semantic) | `--success` | `#4CAF50` | ✅ v1.25.0 |
| — (semantic) | `--border` | `rgba(128,128,128,.15)` | ✅ v1.25.0 |

All former hardcoded colors (`#4CAF50`, `#f44336`, the `rgba(128,128,128,…)`
borders) now go through `--success` / `--danger` / `--border`; the `.slider`
track keeps its own `rgba(…,.4)` as a control-specific shade.

Boot rule: `tg.setBottomBarColor(tg.themeParams.bottom_bar_bg_color || 'bg_color')`
so the native bottom-bar area matches the tab bar (✅ v1.25.0).

## 3. Typography, spacing, shape

Codified from the current CSS — reuse these steps, don't invent new ones.

- **Type scale:** 34px/700 display (`.big`) · 30px/700 card front
  (`.swipe-front`) · 20px/700 deck % (`.deck-pct`) · 19px/700 page title (`h1`)
  · 17px swipe back · 15px body (`.word-term`, `.set-row`) · 13px secondary
  (`.sub`, `.chip`, section header `h2` uppercase) · 12px micro (`.swipe-hint`)
  · 11px tab label.
- **Radii:** 14 card · 18 swipe card · 16 modal · 12 list row/buttons ·
  10 inputs · 8 small · 20/999 pill (chips).
- **Spacing steps:** 4 / 8 / 12 / 16. Card padding 16; list gap 8; grid gap 12.
- **Font:** system stack (`-apple-system, … sans-serif`).

## 4. Components

Each component lists its CSS classes (exact names in `styles.css`) and JS
factory in `app.js` where one exists.

| Component | Classes / factory | Notes |
|---|---|---|
| Card | `.card`, `.card h2`, `.big`, `.sub` | Section header `h2` is 13px uppercase hint-colored |
| Progress bar | `.bar-wrap` / `.bar-fill`; `bar(pct, colour)` | Width transition .4s; colour param should be a token |
| Stat grid | `.grid` | 2-column |
| Stat tiles | `.stat-tiles` / `.stat-tile` / `.stat-emoji` / `.stat-n` | 4-up emoji+count tiles; Library breakdown on the dashboard, shown only when any kind > 0 (✅ v1.29.2) |
| Activity section | `activitySection(s)`: 3-up `.stat-tiles.t3` headline numbers (Activities / This week / Best day) + `.wbars`/`.wbar`/`.wbar-fill` per-week bar plot + `.heat-months` labels + heatmap + legend | Richer Stats activity block (✅ v1.31.0) |
| Activity heatmap | `.heatmap` / `.heat-cell` (+`.l1`–`.l4`, `.today`, `.future`), `.heat-legend`; `heatmapHTML(counts)`, `heatLevel(n)` | 17×7 CSS grid; GitHub-style intensity from `activity_counts` (1/3/6/10+ thresholds) with Less→More legend (✅ v1.29.0) |
| Self-paced banner | `.card.self-paced`; shown when `paused` | Replaces the old "paused → /resume in chat" warning with a positive banner + "Get a new word" / "Review now" CTAs (✅ v1.31.0) |
| Profile drill-down | `#board-profile` via `showBoardSub`; **matchup header** `.card.matchup` (`.vs-side`/`.vs-medallion`/`.you-av`) + `.kudos-bar`/`.kudos-btn`(`.on`); **tug-of-war rows** `versusRow` → `.vs-track` with `.vs-me`(accent)/`.vs-them`(muted) meeting at a divider (longer side wins), `.vs-verdict.up/.down/.tie`, empty state `.vs-track.empty`+`.vs-none`; reuses `heatmapHTML` | Tap a leaderboard row → "VS" head-to-head: a single divider bar per metric encodes who leads, + their heatmap + 👏 kudos. Opaque `id`, never `chat_id`. Verified in light + dark (✅ v1.32.0, redesigned v1.32.2) |
| A11y baseline | global `:focus-visible` ring, `@media (hover)` brightness, `prefers-reduced-motion` reset, `tabular-nums` on number displays, `touch-action: manipulation`; `color-scheme` + dual `theme-color`; `aria-label`/`aria-pressed` on icon-only star/kudos/toggles | Web Interface Guidelines pass (✅ v1.32.2) |
| Level suggestion | `.level-suggest` (`.ls-emoji` / `.ls-msg`); `maybeSuggestLevel()` appended in the Review `onFinish` | One-tap harder/easier switch shown only after a sustained review run, throttled server-side (✅ v1.31.0) |
| Chips | `.chips` / `.chip` / `.chip-on` | Filter + action duty; active = accent |
| Search | `.search` | Debounce input 250ms |
| List + row | `.list`, `.word`, `.word-main`, `.word-term`, `.word-meaning`, `.word-mastery`; `wordRow(w)` | Mastery icons: ✅ mastered · 📖 learning · 🆕 new |
| Library row | `.word.expandable`, `.word-date`, `.word-text`; `contentRow(it)`, `quizRow(it)`, `wordRow(w)` | Every row opens its details on tap (✅ v1.29.0): content rows toggle inline; word rows lazy-fetch `/api/vocab/card`; the ⭐ star stops propagation |
| Bookmark star | `.star` | Optimistic toggle **with rollback** — the canonical mutation pattern |
| Load more | `.more` | Hidden when `offset >= total` |
| Rank row | `.rank`, `.avatar`/`.avatar-fallback`, `.you`, `.word.me`; `boardRow(r)`, `medal(rank)` | 🥇🥈🥉 then `#n`; avatar falls back to the name's first letter |
| Deck card | `.card.deck`, `.deck-head`, `.deck-pct`; `deckCardEl(d)` | Whole card is the tap target |
| Settings row | `.set-row`, `.num`, `.switch`, `.slider`; `row()`, `toggleHTML()` | Rows divided by `--border` |
| Modal | `.modal-back`, `.modal`, `.modal-actions`; `askDisplayName()` | Only for text input (no native equivalent) |
| Tab bar | `#tabbar`, `.tab`, `.tab-on` | Fixed bottom, `safe-area-inset-bottom`; inactive icons grayscaled |
| States | `.loading`, `.empty` | ✅ v1.25.0: `.skeleton` shimmer (`.skel-line`/`.skel-big`/`.skel-row`, helpers `skeletonRows()`/`skeletonCards()`) on first paint |

### Swipe session (`createSwipeSession(container, opts)`)

The one card engine — Review (SRS) and Decks (Leitner) both use it; any future
card flow must too. Classes: `.swipe-area`, `.swipe-card` (+`.dragging`,
`.leaving`), `.swipe-front`, `.swipe-back` (+`.ex` example line),
`.swipe-stamp.known/.forgot`, `.swipe-actions`, `.swipe-btn.known/.forgot`,
`.swipe-hint`, `.swipe-progress`.

Contract (`opts`): `load() → Promise<[{front, back, …}]>` ·
`onAnswer(card, known) → Promise` · `doneText` · `emptyText` ·
`onProgress(remaining)`.

Interaction constants: drag >6px distinguishes drag from a tap · a tap flips the
card both ways (front⇄back, ✅ v1.29.2) · commit at |dx| > 90px · 180ms advance
to next card · stamps fade in over 80px.

Answers re-queue once on POST failure (✅ v1.25.0). Sessions end on a
completion screen with a progress ring and known/forgot counts (✅ v1.26.0).
**Answer buttons are in-page, above the fixed tab bar** — native
`MainButton`/`SecondaryButton` were tried in v1.26.0 and reverted in v1.29.0:
Telegram renders them below the webview, i.e. under the navigation bar
(maintainer decision, June 2026). Don't reintroduce them for session answers.

## 5. Screens

Navigation rules: tab switch hides `BackButton` and reloads the view
(`showView`); Settings is an overlay that returns to `settingsReturnView`;
deck study is a `BackButton` drill-down inside the Decks tab.

| Screen | Data | Layout | Target additions (`GAP`) |
|---|---|---|---|
| **Stats** `#view-dashboard` | `GET /api/stats` | Self-paced banner (when paused) → **hero streak card** (progress ring `streakRing` + ⓘ streak explainer) → Vocabulary tiles (Words/Mastered/Drills/Quiz) → Library tiles → Quiz accuracy → **Activity section** (headline numbers + weekly bar plot + month-labelled heatmap) → Level | ✅ richer activity + self-paced banner (v1.31.0) |
| **Library** `#view-vocab` | `GET /api/vocab` (words/bookmarks), `GET /api/content?kind=` (idiom/collocation/story/tip), `GET /api/quizzes` | Search (words only) → kind chips → list → Load more; content rows expand in place (`contentRow`), quiz rows show ✅/❌ + date (`quizRow`) | `GAP`: result count while searching |
| **Study** `#view-decks` | `GET /api/decks`(+`/study`,`/swipe`,`/detail`), `GET /api/practice?kind=`, `GET /api/quiz/next`+`/answer`, `GET /api/grammar`(+`/lesson`) | Practice-now chips + Grammar CTA → deck list → **deck detail** (box distribution + Study CTA, `showDeckSub`) → swipe session; Grammar list → lesson detail | ✅ detail page + grammar (v1.30.0) |
| **Review** `#view-review` | `GET /api/review/next?limit=30`, `POST /api/review/answer`, `POST /api/review/summary` | Swipe session, 30-card cap; card shows pronunciation (front sub) + meaning/example/Persian (`cardBack`); on finish a throttled level suggestion may appear (`maybeSuggestLevel`) | ✅ richer card + iOS touch swipe (v1.30.0); level suggestion (v1.31.0) |
| **Ranks** `#view-board` | `GET /api/leaderboard`, `GET /api/profile?id=`, `POST /api/kudos` | Ranked rows (tap → profile drill-down `#board-profile` with you-vs-them comparison, their heatmap, 👏 kudos) | ✅ profiles + kudos (v1.32.0) |
| **Ranks** `#view-board` | `GET /api/leaderboard?metric`, `POST /api/leaderboard/name` | Metric chips (All-time/Weekly/**Today**/Mastered) → your-rank card → top-50 list (initial-letter badges, **no photos**); first-visit name modal | ✅ Today metric, photos removed (v1.30.0) |
| **Settings** `#view-settings` | `GET/POST /api/settings` | Level chips → leaderboard name → pause + interval → 9 content toggles | Toggle rollback on failed POST |

State copy conventions — every screen ships all four states:

- **Loading:** skeleton (`GAP`) or `Loading…`.
- **Empty:** always actionable, e.g. *"No words learned yet. Send /word in the
  chat!"*, *"No bookmarks yet. Tap ☆ on a word to save it."*
- **Error:** short + retryable, e.g. *"Could not load your stats. Try again later."*
- **Done (sessions):** celebratory, e.g. *"Review complete! 🎉"*.

## 6. Interaction rules

### Haptics taxonomy

All haptics wrapped in try/catch (see `haptic()` — older clients throw).

| Event | Call | Status |
|---|---|---|
| Tab / chip selection | `selectionChanged()` | ✅ v1.25.0 |
| Button tap, card flip, deck open | `impactOccurred('light')` | ✅ |
| Answer: knew it | `notificationOccurred('success')` | ✅ v1.25.0 |
| Answer: forgot | `notificationOccurred('error')` | ✅ v1.25.0 |
| Session complete | `notificationOccurred('success')` | ✅ v1.25.0 |

### Native chrome

- `BackButton`: drill-downs only (deck study, settings overlay). Always
  `offClick` the old handler when leaving.
- `SettingsButton`: opens Settings; remembers and restores the previous view.
- `MainButton` ("✅ Knew it") + `SecondaryButton` ("❌ Forgot", `position:
  'left'`) during swipe sessions, hidden elsewhere — **`GAP`**.

### Gestures & lifecycle

- ✅ v1.25.0 `tg.disableVerticalSwipes()` on entering a swipe view
  (`setSwipeGuard`), re-enabled on leaving — prevents a card drag from
  collapsing the Mini App.
- ✅ v1.25.0 `tg.enableClosingConfirmation()` once a session has ≥1 answered
  card (`setCloseGuard`); disabled on completion/exit.
- ✅ v1.26.0 native `MainButton`/`SecondaryButton` answer cards in sessions
  (Bot API 7.10+, in-page buttons as fallback); torn down on any view change.
- `.swipe-card` keeps `touch-action: pan-y` so vertical page scroll still works.

### Telegram WebApp API matrix

| Used today | Target (`GAP`) | Explicitly avoided |
|---|---|---|
| `ready`, `expand`, `initData` (+`photo_url`), `HapticFeedback` (impact/notification/selection), `BackButton`, `SettingsButton`, `MainButton`, `SecondaryButton`, `disableVerticalSwipes`, `enableClosingConfirmation`, `setBottomBarColor`, `CloudStorage`, `addToHomeScreen`/`checkHomeScreenStatus`, `openTelegramLink` (t.me/share) | `shareToStory`/`shareMessage` (need a media asset / prepared messages — backlog) | fullscreen mode, `setEmojiStatus`, `downloadFile`, biometrics, sensors, pull-to-refresh |

## 7. Network & state

- `api(path, opts)` is the only fetch wrapper: sends `tg.initData` in the
  `X-Init-Data` header, sets JSON content type on bodies, throws on `!res.ok`.
  Server-side every `/api/*` route is wrapped by `withUser` (`webapp.go`) —
  reuse it for any new endpoint.
- **Mutation policy:** optimistic update + rollback on rejection (bookmark
  star pattern). Settings toggles flip back on failure; swipe answers
  re-queue once on a failed POST (✅ v1.25.0).
- ✅ v1.26.0 `CloudStorage` keys (`cloudGet`/`cloudSet`): `lastTab` (restored
  on boot), `ui.board.metric`. Never store learning data client-side — SQLite
  is the source of truth.
- One screen = one primary fetch; refills inside a session go through the
  session's `load()`.

## 8. Content & accessibility

- `esc()` is mandatory for every interpolated string. AI-generated text and
  user names are untrusted.
- Emoji conventions: exactly one leading emoji per `h1` (📊 📘 📚 🧠 🏆 ⚙️);
  mastery ✅/📖/🆕; medals 🥇🥈🥉; streak flame 🔥 at ≥3 days; celebration 🎉.
- Copy tone: short, encouraging, second person. Empty states always tell the
  user what to do next.
- Keep tap targets ≥ 40px tall (list rows, tabs, switches already comply).

## 9. Gap checklist

| Gap | Roadmap phase |
|---|---|
| ~~Missing theme tokens + `setBottomBarColor` + hardcoded color migration~~ ✅ v1.25.0 | 1 |
| ~~Haptics taxonomy (`selectionChanged`, `notificationOccurred`)~~ ✅ v1.25.0 | 1 |
| ~~`disableVerticalSwipes` + `enableClosingConfirmation` in sessions~~ ✅ v1.25.0 | 1 |
| ~~Skeleton loaders~~ ✅ v1.25.0 | 1 |
| ~~Mutation rollback (settings toggles) + swipe answer retry queue~~ ✅ v1.25.0 | 1 |
| ~~`MainButton`/`SecondaryButton` session answers~~ ✅ v1.26.0 | 2 |
| ~~Session completion screen with per-session stats~~ ✅ v1.26.0 | 2 |
| ~~Activity heatmap (drop Chart.js)~~ ✅ v1.26.0 | 2 |
| ~~`CloudStorage` last-tab restore + `ui.*` state~~ ✅ v1.26.0 | 2 |
| ~~Library (idioms / collocations / stories / tips / quiz history)~~ ✅ v1.27.0 — lives in the Words tab as kind chips, no 6th tab | 3 |
| ~~Leaderboard avatars + weekly metric~~ ✅ v1.28.0 | 4 |
| ~~Share-streak + `addToHomeScreen` prompt~~ ✅ v1.28.0 (share via `t.me/share` deep link — no media asset needed; `shareToStory` stays in the backlog) | 4 |
| ~~In-app quiz + on-demand practice~~ ✅ v1.29.0 — Practice hub on the Decks tab (`openPractice`, `.quiz-opt`) | 5 |
