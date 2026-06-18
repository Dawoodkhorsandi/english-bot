package app

// ChangelogEntry holds the version tag and the HTML-formatted message that is
// delivered once to each subscriber who hasn't seen it yet.
// When Silent is true the version is marked as seen without sending a message,
// useful for internal fixes that shouldn't spam users.
type ChangelogEntry struct {
	Version string
	Text    string
	Silent  bool
}

// Changelogs is the append-only release history. Add a new entry on each
// deployment; existing subscribers receive it on their next broadcast or /start.
var Changelogs = []ChangelogEntry{
	{
		Version: "1.1.0",
		Text: "📣 <b>What's New</b>\n\n" +
			"• Drills now cover <b>14 tenses</b> — including Past Perfect Continuous, Future Perfect Continuous, and the First Conditional\n" +
			"• Messages use richer formatting for easier reading\n" +
			"• Your practiced-verb history keeps every drill fresh — use /reset to start over",
	},
	{
		Version: "1.2.0",
		Text: "📣 <b>What's New in v1.2.0</b>\n\n" +
			"• 📘 <b>Vocabulary words!</b> Alongside grammar drills, you'll now get useful words with meaning, pronunciation, synonyms, opposites and examples\n" +
			"• You now receive practice <b>every 30 minutes</b> — one grammar drill and one vocabulary word per hour\n" +
			"• New /word command to get a vocabulary word on demand\n" +
			"• /reset now clears both your verb and word history",
	},
	{
		Version: "1.3.0",
		Text: "📣 <b>What's New in v1.3.0</b>\n\n" +
			"• 🌙 <b>Quiet nights:</b> no messages between midnight and 9 AM (Tehran time) — rest easy\n" +
			"• 🛌 <b>Bedtime review:</b> at midnight you'll get a recap of the day's words with quick meanings\n" +
			"• ⚡ <b>More reliable:</b> drills and words now come from several AI providers, so practice keeps flowing even if one is down\n" +
			"• 🚀 Faster delivery thanks to a pre-generated content pool",
	},
	{
		Version: "1.4.0",
		Text: "📣 <b>What's New in v1.4.0</b>\n\n" +
			"• 🎚️ <b>Choose your difficulty!</b> Use /level to pick beginner, intermediate or advanced — drills and words adapt to you\n" +
			"• ⏸️ <b>/pause</b> and ▶️ <b>/resume</b> let you stop and restart scheduled sends without losing your history (on-demand /drill and /word still work while paused)",
	},
	{
		Version: "1.5.0",
		Text: "📣 <b>What's New in v1.5.0</b>\n\n" +
			"• 📊 <b>/stats</b> shows your progress: grammar drills practised, words learned, active days, and your current & longest <b>daily streak</b> 🔥\n" +
			"• Keep your streak alive by practising a little every day!",
	},
	{
		Version: "1.6.0",
		Text: "📣 <b>What's New in v1.6.0</b>\n\n" +
			"• ⏱️ <b>/interval</b> lets you choose how often you get practice — anywhere from every 30 minutes to every 12 hours\n" +
			"• Tap the buttons or send e.g. <code>/interval 60</code> to set it",
	},
	{
		Version: "1.7.0",
		Text: "📣 <b>What's New in v1.7.0</b>\n\n" +
			"• 💬 <b>Look up any word!</b> Just send me a word — in English or Persian — and I'll reply with a full vocabulary card (meaning, pronunciation, synonyms, examples)\n" +
			"• Persian words are translated to their English equivalent automatically",
	},
	{
		Version: "1.8.0",
		Text: "📣 <b>What's New in v1.8.0</b>\n\n" +
			"• 🧠 <b>Spaced repetition!</b> Words you've learned now come back as quick <b>memory checks</b> at growing intervals — the proven way to move them into long-term memory\n" +
			"• Tap <b>✅ Knew it</b> or <b>❌ Forgot</b> and I'll fine-tune when you see each word next\n" +
			"• 📊 /stats now shows how many words you've <b>mastered</b>",
	},
	{
		Version: "1.9.0",
		Text: "📣 <b>What's New in v1.9.0</b>\n\n" +
			"• 🧩 <b>Quizzes!</b> Test yourself with multiple-choice questions on words you've learned — send /quiz anytime, and I'll also pop one in now and then\n" +
			"• Your answers tune your spaced-repetition schedule and show up as <b>quiz accuracy</b> in /stats",
	},
	{
		Version: "1.10.0",
		Text: "📣 <b>What's New in v1.10.0</b>\n\n" +
			"• 🧩 <b>New quiz types!</b> Synonym matching (\"Pick the synonym of…\") and fill-in-the-blank questions now rotate alongside the existing formats\n" +
			"• 📅 <b>Weekly recap:</b> every Sunday evening you'll get a summary of the week's words, quiz accuracy, streak and a word-of-the-week highlight\n" +
			"• 🔧 <b>Admin tools:</b> /metrics, /health and /announce for the bot maintainer",
	},
	{
		Version: "1.11.0",
		Text: "📣 <b>What's New in v1.11.0</b>\n\n" +
			"• 🎯 <b>Bigger, better drills!</b> Every grammar drill now takes your verb through <b>21 forms</b> — all 12 tenses, <b>all four conditionals</b> (zero, first, second, third), plus modals, passive voice, the imperative and <i>used to</i>\n" +
			"• 📄 <b>Easy paging:</b> drills are split into bite-size pages by theme — tap <b>◀️ Back</b> and <b>Next ▶️</b> to move through present, past, future, conditionals and more\n" +
			"• Say each one out loud as you go — that's how the muscle memory sticks! 💪",
	},
	{
		Version: "1.12.0",
		Text: "📣 <b>What's New in v1.12.0</b>\n\n" +
			"• 🗣️ <b>Idiom of the Day!</b> Each day you'll get a common English idiom with its meaning and real examples — or grab one anytime with /idiom\n" +
			"• 🎯 <b>Drills lead with the essentials:</b> page 1 of every grammar drill now opens with the forms you use most — Simple Present, Present Continuous, Simple Past and Future <i>will</i>\n" +
			"• Say them out loud and make them stick! 💪",
	},
	{
		Version: "1.13.0",
		Text: "📣 <b>What's New in v1.13.0</b>\n\n" +
			"• 🔀 <b>No more repeats!</b> Fixed a bug where very active learners could keep getting the <b>same drill or word</b> over and over. Content is now picked at random and rotates fairly, so you always get fresh practice\n" +
			"• 📚 <b>Much bigger content pool</b> — far more drills, words and idioms ready to go before anything ever comes back around\n" +
			"• Once you've seen everything at your level, reviews now cycle through your history instead of sticking on one item",
	},
	{
		Version: "1.14.0",
		Text: "📣 <b>What's New in v1.14.0</b>\n\n" +
			"• 🔊 <b>Pronunciation audio is here!</b> After each vocabulary card and idiom, I now send a short voice pronunciation clip to reinforce speaking out loud\n" +
			"• ⚙️ <b>New /tts command:</b> use <code>/tts on</code> or <code>/tts off</code> to control pronunciation audio at any time",
	},
	{
		Version: "1.15.0",
		Text: "📣 <b>What's New in v1.15.0</b>\n\n" +
			"• 💡 <b>Grammar Tip of the Day</b> is here! You now get one focused daily grammar tip with clear correct/incorrect examples\n" +
			"• 🛠️ New <b>/tip</b> command: get a tip anytime, or use <code>/tip off</code> and <code>/tip on</code> to control scheduled daily tips",
	},
	{
		Version: "1.15.1",
		Text: "📣 <b>What's New in v1.15.1</b>\n\n" +
			"• 📋 <b>Command menu!</b> Tap <b>/</b> in the chat and you'll now see a list of all available commands — no need to memorise them\n" +
			"• The menu updates automatically on every deploy, so new commands always appear right away",
	},
	{
		Version: "1.16.0",
		Text: "📣 <b>What's New in v1.16.0</b>\n\n" +
			"• 🎚️ <b>New difficulty level: Upper-Intermediate!</b> Bridges the gap between Intermediate (B1–B2) and Advanced (C1–C2)\n" +
			"• Targets CEFR B2–C1 — academic, professional, and media vocabulary with natural sentence complexity\n" +
			"• Use /level to switch to the new level",
	},
	{
		Version: "1.17.0",
		Text: "📣 <b>What's New in v1.17.0</b>\n\n" +
			"• ⚙️ <b>Unified Settings!</b> Use /settings to control everything in one place\n" +
			"• ⏱ <b>Quick Setup!</b> Use /setup to pick how much time you have per day (5 min → 2 hours) — the bot tunes itself to fit your schedule\n" +
			"• Toggle quizzes, idiom of the day, SRS word reviews, weekly digest and midnight vocab recap individually\n" +
			"• Set your own quiz frequency: every 3, 6, 12 or 24 hours\n" +
			"• No more message pileups — a smart rate limiter ensures at most one message per interval window\n" +
			"• Default broadcast interval changed from 30 → 60 min for a calmer default experience",
	},
	{
		Version: "1.18.0",
		Text: "📣 <b>What's New in v1.18.0</b>\n\n" +
			"• 🧩 <b>Native quiz polls!</b> Questions now use Telegram's built-in quiz format — tap your answer and see the correct one highlighted instantly\n" +
			"• ⌨️ <b>Quick-access keyboard</b> — four shortcut buttons (Word / Drill / Quiz / Stats) now live at the bottom of your keyboard\n" +
			"• 📊 <b>Progress bars</b> in /stats show your streak and quiz accuracy at a glance\n" +
			"• 🎉 <b>Streak celebrations</b> — hit 3, 7, 14, 30 or 60 days in a row and I'll cheer you on\n" +
			"• ⌨️ <b>Typing indicator</b> — you'll see 'typing...' while I'm generating content so the wait feels shorter\n" +
			"• 👋 Personalised greetings using your Telegram name",
	},
	{
		Version: "1.18.1",
		Text: "📣 <b>What's New in v1.18.1</b>\n\n" +
			"• 📊 <b>Mini App live!</b> The /stats Full Dashboard button now opens the interactive progress page at bot.mardeen.ir",
	},
	{
		Version: "1.19.0",
		Text: "📣 <b>What's New in v1.19.0</b>\n\n" +
			"• Bug fixes and stability improvements",
	},
	{
		Version: "1.20.0",
		Text: "📣 <b>What's New in v1.20.0</b>\n\n" +
			"• 🇮🇷 <b>Persian definition!</b> Every vocabulary card now includes a Persian/Farsi translation — hidden behind a spoiler, tap to reveal",
	},
	{
		Version: "1.21.0",
		Text: "📣 <b>What's New in v1.21.0</b>\n\n" +
			"• 📘 <b>Browse your words!</b> Use /mywords to see all vocabulary you've learned, with mastery status\n" +
			"• ⭐ <b>Bookmarks!</b> Tap ⭐ on any word card or use /bookmark to save important words\n" +
			"• Use /mywords bookmarks to see only your bookmarked words\n" +
			"• 🎚️ <b>Smoother difficulty levels!</b> Intermediate (B1) and Upper-Intermediate (B2) are better calibrated",
	},
	{
		Version: "1.21.1",
		Silent:  true,
		Text: "• Fixed /bookmark empty state showing misleading pagination\n" +
			"• Bookmark button now appears on all word cards including old pool entries\n" +
			"• Stale word cards (missing Persian) are auto-refreshed in the background\n" +
			"• Added silent changelog support for internal deploys",
	},
	{
		Version: "1.21.2",
		Silent:  true,
		Text: "• Added nightly SQLite backup delivery to maintainer chat\n" +
			"• Added maintainer-only /backup command for on-demand backups\n" +
			"• Added BACKUP_TIME config for daily backup scheduling",
	},
	{
		Version: "1.22.0",
		Silent:  true,
		Text: "• Added /config admin panel for runtime bot settings\n" +
			"• Configurable: pool target, pool min, quiet hours, global TTS, gen spacing, review batch max\n" +
			"• Settings persisted in bot_config table and survive restarts",
	},
	{
		Version: "1.23.0",
		Text: "📣 <b>What's New in v1.23.0</b>\n\n" +
			"• 🔗 <b>Collocations!</b> Learn natural word partnerships (like <i>make a decision</i>, <i>heavy rain</i>) with meaning, examples and common mistakes — one arrives daily, or get one anytime with /collocation\n" +
			"• 📖 <b>Mini stories!</b> A short reading-practice story at your level with key vocabulary and a comprehension question — one arrives daily, or get one anytime with /story\n" +
			"• Both can be turned on/off in /settings",
	},
	{
		Version: "1.23.1",
		Silent:  true,
		Text: "• /metrics pool depth now includes collocations and mini stories (per level) and tips\n" +
			"• Fixes admin pool-depth view omitting the v1.23.0 content kinds",
	},
	{
		Version: "1.23.2",
		Silent:  true,
		Text: "• /config now supports per-kind and per-level pool-size overrides\n" +
			"• Precedence: per-kind → per-level → global target/min; ♻️ Default clears an override\n" +
			"• Overrides persist in bot_config and survive restarts",
	},
	{
		Version: "1.23.3",
		Silent:  true,
		Text: "• Memory-check cards now hide the meaning until you answer (recall first!)\n" +
			"• Tapping ✅ Knew it reveals a short reminder; ❌ Forgot reveals the full word card to relearn it",
	},
	{
		Version: "1.23.4",
		Silent:  true,
		Text: "• Maintainer is now alerted when a user has consumed the entire pool for a kind/level and starts seeing repeats\n" +
			"• Deduped per chat+kind+level; re-alerts only after the pool is grown via /config",
	},
	{
		Version: "1.23.5",
		Silent:  true,
		Text: "• New /poolusage admin report: per kind/level, shows the most active user's pool consumption as a percentage\n" +
			"• Surfaces which pools are closest to exhaustion so you can grow them via /config before users see repeats",
	},
	{
		Version: "1.23.6",
		Silent:  true,
		Text: "• /config now supports per-(kind, level) pool-size overrides (e.g. upper-intermediate words specifically)\n" +
			"• Precedence: per-(kind,level) → per-kind → per-level → global; ♻️ Default clears it\n" +
			"• New /admin command lists every maintainer command so they're easy to find",
	},
	{
		Version: "1.23.7",
		Silent:  true,
		Text: "• /poolusage now shows \"pool <depth>/<target>\" per line so a raised target is visible immediately\n" +
			"• Clarifies that the percentage is consumption of items generated so far, not of the configured target",
	},
	{
		Version: "1.23.8",
		Silent:  true,
		Text: "• Mini App: clearer startup logging when WEB_APP_URL is unset (web server stays off → reverse proxy 502) or non-HTTPS\n" +
			"• Documented WEB_APP_URL / WEB_APP_PORT in .env.example so the dashboard is discoverable",
	},
	{
		Version: "1.24.0",
		Text: "📣 <b>What's New in v1.24.0</b>\n\n" +
			"🚀 <b>Your English hub just leveled up!</b>\n\n" +
			"Tap the <b>menu button</b> next to the message box (or send /app) to open a whole new in-app experience:\n\n" +
			"📊 <b>Dashboard</b> — your streak, words, quiz accuracy and 30-day activity, beautifully laid out\n" +
			"📘 <b>Word list</b> — search every word you've learned, filter your ⭐ bookmarks, all in one scroll\n" +
			"📚 <b>Word decks</b> — swipe through curated decks (the classic <b>504 Essential Words</b> and <b>Barron's GRE</b>) with a smart Leitner system that brings tricky words back more often\n" +
			"🧠 <b>Quick review</b> — swipe ✅/❌ through your due words to lock them into memory\n" +
			"🏆 <b>Leaderboard</b> — see how you rank against other learners (pick a name, or get a fun random one!)\n" +
			"⚙️ <b>Settings</b> — tweak your level, schedule and content right inside the app\n\n" +
			"Everything syncs with the bot you already use. Give it a tap! 🎉",
	},
	{
		Version: "1.24.1",
		Silent:  true,
		Text: "🛠️ <b>Mini App fixes</b>\n\n" +
			"• ✅ Tap a Review/Deck card to reveal its meaning again (the tap-to-flip got stuck after the first touch)\n" +
			"• ⚙️ Difficulty levels now wrap neatly and show friendly names in Settings\n" +
			"• 🏆 You can now change your leaderboard name any time from Settings",
	},
	{
		Version: "1.25.0",
		Silent:  true,
		Text: "Mini App native-feel polish (roadmap phase 1)\n" +
			"• Full Telegram theme-token coverage (section/destructive/bottom-bar colors) + native bottom-bar blending\n" +
			"• Haptics taxonomy: selection feedback on tabs/chips, success/error feedback on review answers\n" +
			"• Swipe views disable the vertical collapse gesture; mid-session close shows a confirmation\n" +
			"• Skeleton loaders on first paint; failed swipe answers re-queue; failed settings toggles roll back",
	},
	{
		Version: "1.26.0",
		Silent:  true,
		Text: "Mini App session & dashboard experience (roadmap phase 2)\n" +
			"• Native Telegram MainButton/SecondaryButton answer review/deck cards on Bot API 7.10+ clients\n" +
			"• Sessions end on a completion screen with a progress ring and known/forgot counts\n" +
			"• GitHub-style 4-month activity heatmap replaces the Chart.js bar chart (CDN dependency dropped)\n" +
			"• CloudStorage remembers your last tab and leaderboard metric across devices",
	},
	{
		Version: "1.27.0",
		Silent:  true,
		Text: "Mini App Library (roadmap phase 3)\n" +
			"• The Words tab became a Library: chips for Words, Bookmarks, Idioms, Collocations, Stories, Tips and Quizzes\n" +
			"• New /api/content (history tables joined with content_pool, HTML stripped) and /api/quizzes endpoints\n" +
			"• Library rows expand in place to re-read the full card; quiz rows show ✅/❌ + date",
	},
	{
		Version: "1.28.0",
		Silent:  true,
		Text: "Mini App gamification & growth (roadmap phase 4)\n" +
			"• Leaderboard avatars: Telegram photos cached from validated initData (user_prefs.photo_url), initial-letter fallback\n" +
			"• New 📅 This-week leaderboard metric (words learned since Monday) keeps newcomers motivated\n" +
			"• 📣 Share-my-streak button (t.me/share deep link) at a 3-day streak\n" +
			"• Add-to-home-screen offer at a 7-day streak (Bot API 8.0+, asked once via CloudStorage)",
	},
	{
		Version: "1.29.0",
		Text: "📣 <b>What's New in v1.29.0</b>\n\n" +
			"🚀 <b>The Mini App got a huge upgrade!</b> Tap the menu button (or send /app) and explore:\n\n" +
			"🧩 <b>Practice anytime</b> — play quizzes and grab fresh words, idioms and collocations right inside the app, no waiting for scheduled sends\n" +
			"📖 <b>Your Library</b> — every idiom, collocation, story, tip and quiz you've ever received, searchable and re-readable with one tap\n" +
			"🟩 <b>Activity heatmap</b> — a GitHub-style calendar of your last 4 months; the more you learn in a day, the deeper the green\n" +
			"🎯 <b>Smarter reviews</b> — sessions end with a progress ring and your known/forgot score, and a stray swipe can't lose your progress anymore\n" +
			"🏆 <b>Fresh leaderboard</b> — real profile photos, plus a new 📅 This-week ranking so newcomers can compete too\n" +
			"📣 <b>Share your streak</b> — show off your learning streak to friends with one tap\n" +
			"📱 <b>One tap away</b> — keep a 7-day streak and we'll offer to put the app on your home screen\n\n" +
			"…plus a faster, smoother feel everywhere: native haptics, skeleton loading, and full dark-mode theming. Happy learning! 🎉",
	},
	{
		Version: "1.29.1",
		Silent:  true,
		Text: "Mini App: Cache-Control no-cache on the SPA shell and assets — embedded files have no modtime, " +
			"so webviews cached them heuristically and users kept a stale frontend after deploys " +
			"(reported as the reverted native answer buttons still hiding the tab bar in Review).",
	},
	{
		Version: "1.29.2",
		Silent:  true,
		Text: "Mini App: review/deck cards now flip both ways on tap (tap reveals the meaning, tap again hides it), " +
			"and the dashboard shows a Library breakdown (idioms / collocations / stories / tips) alongside words and drills.",
	},
	{
		Version: "1.30.0",
		Text: "📣 <b>What's New in v1.30.0</b>\n\n" +
			"📖 <b>Grammar lessons!</b> A brand-new section — bite-size lessons from easy to advanced, each with the pattern, a clear explanation, examples and quick practice. Send /grammar or open the app.\n\n" +
			"🧠 <b>Better reviews</b> — review cards now show pronunciation, a Persian translation and an example, no more repeated words, and swiping works smoothly on iPhone.\n\n" +
			"📚 <b>More & richer decks</b> — the 504 deck now has Persian, Barron's GRE adds memory mnemonics, plus four new decks: Phrasal Verbs, Business English, Academic Word List and IELTS/TOEFL. Each deck now has its own detail page with a progress breakdown.\n\n" +
			"🏆 <b>Leaderboard</b> — a new ☀️ Today ranking alongside This-week.\n\n" +
			"✨ <b>A fresh look</b> — a redesigned quiz, a fancier stats dashboard with a streak ring (tap ⓘ to see how streaks work), and a one-tap Share button to invite friends.\n\n" +
			"Tap the menu button (or send /app) and explore! 🎉",
	},
	{
		Version: "1.31.0",
		Silent:  true,
		Text: "Self-paced learning, smarter level guidance, and a streak fix. " +
			"Self-paced mode (Settings) silences every automatic message so users can learn purely on demand in the app — " +
			"on-demand practice still seeds reviews. The streak now counts in-app reviews, deck study and quiz answers " +
			"(previously only delivered words/drills counted), and scheduled sends now fire milestone celebrations. " +
			"After a sustained run of reviews — not every batch — the Review tab may suggest a harder/easier level with a " +
			"one-tap switch. The Stats activity section gains headline numbers and a per-week bar plot.",
	},
	{
		Version: "1.31.1",
		Silent:  true,
		Text:    "Mini App fix: the Grammar section no longer shows its heading twice (the static wrapper title duplicated the one rendered by the list/lesson view).",
	},
	{
		Version: "1.32.0",
		Silent:  true,
		Text:    "Mini App: leaderboard rows are now tappable and open a user profile — a head-to-head comparison of your stats vs theirs (words, mastered, streak, quiz accuracy, active days) plus their activity heatmap. You can also give 👏 kudos to other learners; recipients are notified unless they're in self-paced mode. Profiles use an opaque public id, never a Telegram identity.",
	},
	{
		Version: "1.32.1",
		Silent:  true,
		Text:    "Fix: profile pages failed to load (\"Could not load this profile\") because each user's opaque public id was never persisted — the leaderboard tried to write it while its own result cursor was still open, which SQLite rejects. Public ids are now stored after the read completes, with a deterministic self-healing fallback for already-affected users.",
	},
	{
		Version: "1.32.2",
		Silent:  true,
		Text:    "Mini App: redesigned the profile page as a head-to-head \"VS\" matchup — each stat is a single tug-of-war bar whose divider shifts toward whoever's ahead, so you read who leads at a glance. Plus an accessibility pass across the app: visible keyboard focus, hover states, reduced-motion support, steady tabular numerals, theme-color/color-scheme, and aria-labels on icon-only controls.",
	},
	{
		Version: "1.32.3",
		Silent:  true,
		Text:    "Fix: the nightly \"🌙 Today's Words\" review could list the same word more than once when that word existed in the content pool at multiple levels (the lookup joined the pool without deduplicating). Words are now grouped so each appears once, preferring a non-empty meaning.",
	},
	{
		Version: "1.32.4",
		Silent:  true,
		Text:    "Internal: restructured the codebase from one flat package into a cmd/english-bot entrypoint plus internal/{config,ai,telegram,content,app} packages, renamed the module to its canonical path, added a golangci-lint config + Makefile, and broadened test coverage. No user-facing changes.",
	},
	{
		Version: "1.33.0",
		Silent:  true,
		Text:    "Dictionary: added an offline English-Persian dictionary powered by kaikki.org (wiktextract) with fa.wiktionary.org live fallback. On first deploy the bot downloads and indexes ~17k English→Persian translation pairs into a local SQLite table. New Dict tab in the Mini App with search-as-you-type. Deck backfill now tries the free dictionary before spending AI calls on Persian/pronunciation/example fields.",
	},
	{
		Version: "1.33.1",
		Silent:  true,
		Text:    "Fix: release workflow built binaries from the repo root (no Go files after the v1.32.4 restructure) instead of ./cmd/english-bot. Cross-platform release binaries now build correctly.",
	},
	{
		Version: "1.34.0",
		Silent:  true,
		Text: "Tap-to-translate: tap any English word in card backs, library details, practice cards, grammar lessons, " +
			"or dictionary results to see an instant Persian translation popup (powered by the offline/online dictionary). " +
			"The popup shows up to 3 senses with a link to open the full Dictionary tab. " +
			"Also fixed heatmap dots not showing activity counts on mobile Telegram (replaced native title with a custom touch tooltip).",
	},
	{
		Version: "1.34.1",
		Silent:  true,
		Text:    "Fix: the tap-to-translate word popup could not be dismissed — the .wp-overlay display:flex rule overrode the hidden attribute. The X button and backdrop now close it on mobile and desktop.",
	},
	{
		Version: "1.34.2",
		Silent:  true,
		Text:    "Fix: the word popup still couldn't be closed on Android/iOS Telegram. Routed close (X / backdrop) and Open-in-Dictionary through the single capture-phase click handler that reliably fires in the mobile webview, and toggle visibility via inline display instead of the hidden attribute.",
	},
	{
		Version: "1.34.3",
		Silent:  true,
		Text:    "Fix: tapping the dimmed backdrop now closes the word popup on Android/iOS. iOS/Telegram WebView only emits click events for elements it considers clickable, so the plain backdrop div never fired a tap — added cursor:pointer to mark it clickable.",
	},
	{
		Version: "1.34.4",
		Silent:  true,
		Text:    "Fix: the word popup still couldn't be closed on mobile (× and backdrop both unresponsive) — click events don't reliably reach the popup controls in the Telegram webview even though opening works. Added a touchend fallback (the same path the swipe cards use) scoped to the open popup so close and Open-in-Dictionary fire on the first tap.",
	},
	{
		Version: "1.35.0",
		Silent:  true,
		Text:    "Achievements and learning analytics. New /api/stats payload includes 26 unlockable badges across 8 categories (Vocabulary, Grammar, Streaks, Quiz, SRS, Library, Social, Dedication, Mastery) — all computed from existing user data with no new DB tables. New /api/analytics endpoint serves vocabulary breakdown by level, quiz accuracy trend (30-day sparkline), activity-by-hour heatmap, weekly learning velocity, and content diversity. Dashboard shows badge grid with progress bars and analytics cards below the activity heatmap.",
	},
	{
		Version: "1.35.1",
		Silent:  true,
		Text:    "Achievements redesign: Stats dashboard split into Overview and Gamification sub-tabs. 39 achievements (up from 26) with expandable category sections. New badges: First Drill, First Quiz, First Idiom, First Story, Vocabulary Legend (1000 words), Century Streak (100 days), Quiz Warrior (100 answers), Quiz Perfectionist (100% at 50+), Idiom Collector (20), Prolific Reader (20 stories), Popular (5 kudos), Century Active (100 days), Speed Learner (10+ in one day), Bookworm Supreme (25 bookmarks), Deck Explorer (50 cards). Leaderboard profile view now shows head-to-head achievement comparison.",
	},
	{
		Version: "1.36.0",
		Silent:  true,
		Text:    "Mobile app authentication: email/password registration and login, JWT HMAC-SHA256 middleware, Telegram login overlay for cross-app linking. No user-facing changes to the Telegram bot.",
	},
	{
		Version: "1.37.0",
		Text: "📣 <b>What's New in v1.37.0</b>\n\n" +
			"• 📱 <b>Mobile app sign-in now works!</b> New /login command gives you a one-time code — enter it in the app, no Mini App or clipboard needed\n" +
			"• 🔗 <b>One account across email & Telegram:</b> sign in with either and link them to share the same progress (Google sign-in coming next)\n" +
			"• 🔒 Security hardening behind the scenes: stronger login-secret checks, a higher login-code limit, and a code-reuse fix",
	},
	{
		Version: "1.37.1",
		Silent:  true,
		Text:    "Fix: Telegram login codes were always rejected as expired on non-UTC servers (production runs Asia/Tehran). claimLoginCode now normalises the stored created_at via parseStoredUTC instead of scanning a DATETIME straight into time.Time.",
	},
	{
		Version: "1.38.0",
		Silent:  true,
		Text:    "Security & hygiene: rate-limit POST /api/auth/telegram/verify (reuse existing authBlocked/recordAuthFailure guard), wire up CleanupAuthCodes goroutine in Run() to purge expired login codes every 10 minutes.",
	},
	{
		Version: "1.39.0",
		Silent:  true,
		Text: "Mobile app account & feedback features. Sign in with Google: POST /api/auth/google " +
			"verifies the Google ID token (via Google's tokeninfo endpoint, checking the aud against " +
			"GOOGLE_CLIENT_ID) and creates or links the account — an existing email identity with the " +
			"same address is unified, never duplicated; POST /api/auth/link/google attaches Google to " +
			"the signed-in account. Connect Telegram from the app reuses /api/auth/link/telegram. " +
			"POST /api/report/bug forwards a user's bug report to MAINTAINER_CHAT_ID via the notifier. " +
			"Silent: Google sign-in stays dormant until GOOGLE_CLIENT_ID is set in prod, so no loud " +
			"announcement yet.",
	},
}
