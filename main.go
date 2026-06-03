package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tz database so Asia/Tehran resolves without OS tzdata

	_ "modernc.org/sqlite"
)

// Configuration - Read from Environment Variables
var (
	TelegramBotToken = getEnv("TELEGRAM_BOT_TOKEN", "YOUR_TELEGRAM_BOT_TOKEN")
	GeminiAPIKey     = getEnv("GEMINI_API_KEY", "YOUR_GEMINI_API_KEY")
	MaintainerChatID = getEnv("MAINTAINER_CHAT_ID", "YOUR_PERSONAL_CHAT_ID")
)

var telegramHTTPClient = &http.Client{Timeout: 30 * time.Second}

const (
	dbFile       = "subscribers.db"
	legacyDBFile = "subscribers.json"
)

// ChangelogEntry holds the version tag and the HTML-formatted message that is
// delivered once to each subscriber who hasn't seen it yet.
type ChangelogEntry struct {
	Version string
	Text    string
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
}

// Store wraps the SQLite connection used to persist subscribers and the
// per-user history of verbs that have already been sent.
type Store struct {
	db *sql.DB
}

type TelegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *TelegramMessage       `json:"message"`
	CallbackQuery *TelegramCallbackQuery `json:"callback_query"`
}

type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	Chat      TelegramChat  `json:"chat"`
	Text      string        `json:"text"`
	From      *TelegramUser `json:"from"`
}

// TelegramCallbackQuery represents a tap on an inline-keyboard button.
type TelegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    *TelegramUser    `json:"from"`
	Message *TelegramMessage `json:"message"`
	Data    string           `json:"data"`
}

type TelegramChat struct {
	ID int64 `json:"id"`
}

type TelegramUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// inlineButton is one button in an inline keyboard.
type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

func main() {
	log.Println("⚙️  [INIT] Initializing Telegram English Muscle Memory Bot...")

	log.Printf("⚙️  [CONFIG] Telegram Token Loaded: %t (Length: %d)", TelegramBotToken != "YOUR_TELEGRAM_BOT_TOKEN" && TelegramBotToken != "", len(TelegramBotToken))
	log.Printf("⚙️  [CONFIG] Maintainer Chat ID Target: %s", MaintainerChatID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loadLocation()

	log.Println("⚙️  [INIT] Building AI provider chain...")
	chain := newProviderChain(ctx)
	if !chain.HasAny() {
		log.Println("⚠️  [INIT] No AI providers enabled. Set at least one provider API key (e.g. GEMINI_API_KEY).")
	}

	store, err := openStore(dbFile)
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	notifier := &telegramNotifier{}

	// On deploy: immediately broadcast unseen changelogs to all subscribers.
	broadcastChangelogsOnStartup(store, notifier)

	// Background workers (v2):
	//   1. poolFiller keeps the pre-generated content pool stocked.
	//   2. broadcast scheduler fans pooled content out every 30 min (quiet-hour aware).
	//   3. daily review scheduler sends a bedtime word recap at local midnight.
	//   4. spaced-repetition scheduler resurfaces due words for review (Change D).
	//   5. quiz scheduler periodically tests learned words (Change E).
	//   6. weekly digest scheduler sends a recap every DIGEST_DAY at DIGEST_TIME (Change K).
	go poolFiller(ctx, chain, store)
	go runBroadcastScheduler(ctx, chain, store, notifier)
	go runDailyReviewScheduler(ctx, store, notifier)
	go runReviewScheduler(ctx, store, notifier)
	go runQuizScheduler(ctx, store, notifier)
	go runWeeklyDigestScheduler(ctx, store, notifier)
	go runIdiomScheduler(ctx, chain, store, notifier)

	log.Println("📡 [SYSTEM] Launching Telegram incoming updates consumer engine...")
	go pollTelegramUpdates(ctx, chain, store, notifier)

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	log.Println("🚀 [SYSTEM] Startup complete. Bot is fully operational and listening.")
	sig := <-stopChan
	log.Printf("🛑 [SYSTEM] Shutdown intercept caught OS Signal: %v. Cleaning tasks...", sig)
}

func pollTelegramUpdates(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	var offset int64 = 0
	client := &http.Client{Timeout: 35 * time.Second}

	log.Println("📡 [POLLER] Thread loop listening. Long-polling via Telegram Gateway started.")
	for {
		select {
		case <-ctx.Done():
			log.Println("📡 [POLLER] Thread context killed. Terminating polling interface safely.")
			return
		default:
			url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", TelegramBotToken, offset)

			log.Printf("📡 [POLLER_REQ] Requesting updates from endpoint gateway. Current Offset pointer: %d", offset)
			resp, err := client.Get(url)
			if err != nil {
				log.Printf("❌ [POLLER_NET_ERR] Network socket error connecting to Telegram API: %v. Retrying in 5 seconds...", err)
				time.Sleep(5 * time.Second)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("❌ [POLLER_IO_ERR] Read failure on incoming content buffer response payload: %v", err)
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			var updateResp struct {
				Ok          bool             `json:"ok"`
				Description string           `json:"description"`
				Result      []TelegramUpdate `json:"result"`
			}

			if err := json.Unmarshal(body, &updateResp); err != nil {
				log.Printf("❌ [POLLER_JSON_ERR] Unmarshal exception. Source raw: %s. Error: %v", string(body), err)
				time.Sleep(2 * time.Second)
				continue
			}

			if !updateResp.Ok {
				log.Printf("❌ [TELEGRAM_API_REJECT] Server returned ok=false. Reason: %s", updateResp.Description)
				time.Sleep(5 * time.Second)
				continue
			}

			updateCount := len(updateResp.Result)
			if updateCount > 0 {
				log.Printf("📥 [POLLER_INBOUND] Received batch of %d update(s).", updateCount)
			}

			for _, update := range updateResp.Result {
				offset = update.UpdateID + 1
				switch {
				case update.Message != nil:
					log.Printf("📩 [MESSAGE_DISPATCH] Update %d | Chat %d | Text: %q", update.UpdateID, update.Message.Chat.ID, update.Message.Text)
					handleMessage(ctx, chain, store, notifier, update.Message)
				case update.CallbackQuery != nil:
					log.Printf("🔘 [CALLBACK_DISPATCH] Update %d | Data: %q", update.UpdateID, update.CallbackQuery.Data)
					handleCallback(store, notifier, update.CallbackQuery)
				default:
					log.Printf("📝 [POLLER_SKIP] Non-message update %d. Skipping.", update.UpdateID)
				}
			}
		}
	}
}

func handleMessage(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, msg *TelegramMessage) {
	if msg.Text == "" {
		log.Printf("ℹ️  [ROUTER] Ignoring empty message ID %d.", msg.MessageID)
		return
	}

	chatID := msg.Chat.ID
	username := "unknown"
	if msg.From != nil && msg.From.Username != "" {
		username = msg.From.Username
	}

	log.Printf("🎮 [COMMAND_MATCH] %q from @%s (ID: %d)", msg.Text, username, chatID)

	fields := strings.Fields(msg.Text)
	command := fields[0]
	args := fields[1:]

	switch command {
	case "/start":
		isNew, err := store.AddSubscriber(chatID)
		if err != nil {
			log.Printf("❌ [REGISTRATION_ERR] Failed to persist subscriber %d: %v", chatID, err)
		}

		log.Printf("💾 [REGISTRATION] isNew=%t for ChatID %d", isNew, chatID)

		if isNew {
			log.Printf("🎉 [NEW_USER] New subscriber %d. Notifying maintainer...", chatID)

			// Mark every existing changelog as seen so new users only receive
			// notes for versions released after they joined.
			for _, entry := range Changelogs {
				if err := store.MarkChangelogSeen(chatID, entry.Version); err != nil {
					log.Printf("⚠️  [CHANGELOG] Could not baseline v%s for new user %d: %v", entry.Version, chatID, err)
				}
			}

			fromID := chatID
			if msg.From != nil {
				fromID = msg.From.ID
			}
			maintainerMsg := fmt.Sprintf("<b>🎉 New User Joined!</b>\n<b>ID:</b> %d\n<b>Username:</b> @%s", fromID, username)

			mID, parseErr := strconv.ParseInt(MaintainerChatID, 10, 64)
			if parseErr != nil {
				log.Printf("❌ [PARSE_ERR] Invalid MaintainerChatID %q: %v", MaintainerChatID, parseErr)
			} else {
				_ = notifier.Send(mID, maintainerMsg)
			}
		} else {
			// Returning user — deliver any changelogs they haven't seen yet.
			sendPendingChangelogs(store, notifier, chatID)
		}

		welcome := "👋 <b>Welcome to English Muscle Memory Bot!</b>\n\n" +
			"Every 30 minutes I send you something to practice — alternating between:\n" +
			"• 🎯 a <b>grammar drill</b> (one verb across 21 forms — tap ▶️ to page through tenses, conditionals & more)\n" +
			"• 📘 a <b>vocabulary word</b> (meaning, pronunciation, synonyms, opposites & examples)\n\n" +
			"💬 <b>Tip:</b> send me <b>any word</b> (English or Persian) and I'll explain it like a vocabulary card!\n\n" +
			"📚 <b>Commands:</b>\n" +
			"/drill — Get a grammar drill right now\n" +
			"/word — Get a vocabulary word right now\n" +
			"/idiom — Get an idiom of the day\n" +
			"/quiz — Test yourself on a word you've learned\n" +
			"/level — Choose your difficulty (beginner/intermediate/advanced)\n" +
			"/interval — Choose how often you get practice\n" +
			"/tts — Turn pronunciation audio on/off\n" +
			"/stats — See your progress and streak\n" +
			"/pause — Pause scheduled sends\n" +
			"/resume — Resume scheduled sends\n" +
			"/reset — Clear your practiced history\n" +
			"/help — How it works"
		_ = notifier.Send(chatID, welcome)

	case "/help":
		helpText := "💡 <b>How Muscle Memory Practice Works</b>\n\n" +
			"Don't just read — <b>say everything out loud!</b> Repeating correct sentences and new words fast builds the subconscious instincts you need for natural speech.\n\n" +
			"🎯 <b>Grammar drills</b> take one everyday verb through <b>21 forms</b>, split into easy pages — tap <b>◀️ Back</b> / <b>Next ▶️</b> to move between them:\n" +
			"present, past & future tenses (Simple, Continuous, Perfect, Perfect Continuous), all four conditionals, plus modals, passive, imperative and <i>used to</i>.\n\n" +
			"📘 <b>Vocabulary words</b> give you the meaning, pronunciation, synonyms, opposites and real examples for a useful new word.\n\n" +
			"🗣️ <b>Idiom of the day:</b> a common English expression with its meaning and real examples — sent once a day, or anytime via /idiom.\n\n" +
			"🧠 <b>Spaced repetition:</b> words you've learned come back as quick <b>memory checks</b> at growing intervals — tap ✅ Knew it / ❌ Forgot and I'll tune when you see each one next.\n\n" +
			"🧩 <b>Quizzes:</b> multiple-choice questions test your recall — send /quiz anytime, and one pops up now and then. Your answers also tune your review schedule.\n\n" +
			"💬 <b>Look up any word:</b> just type it (English or Persian) and I'll send a full card for it.\n\n" +
			"You get one of each per hour, about 30 minutes apart (quiet overnight).\n\n" +
			"/drill — generate a grammar drill on demand\n" +
			"/word — generate a vocabulary word on demand\n" +
			"/idiom — get a common idiom with meaning and examples\n" +
			"/quiz — test yourself on a word you've already learned\n" +
			"/level — set difficulty: beginner, intermediate or advanced\n" +
			"/interval — set how often scheduled practice arrives\n" +
			"/tts — turn pronunciation audio on or off\n" +
			"/stats — see your progress, streak and totals\n" +
			"/pause — stop scheduled sends (on-demand still works)\n" +
			"/resume — re-enable scheduled sends\n" +
			"/reset — clear history and see old verbs & words again"
		_ = notifier.Send(chatID, helpText)

	case "/drill":
		log.Printf("🤖 [AI_FLOW] /drill requested by ChatID %d.", chatID)
		_ = notifier.Send(chatID, "🔄 <b>Generating your drill...</b>")

		drill, err := serveContent(ctx, chain, store, chatID, kindDrill, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Drill delivered to ChatID %d.", chatID)
		_ = sendDrill(notifier, chatID, drill)

	case "/word":
		log.Printf("🤖 [AI_FLOW] /word requested by ChatID %d.", chatID)
		_ = notifier.Send(chatID, "🔄 <b>Finding a fresh word for you...</b>")

		card, err := serveContent(ctx, chain, store, chatID, kindWord, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Word generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Word delivered to ChatID %d.", chatID)
		if err := sendWordCardWithTTS(ctx, store, notifier, chatID, card); err != nil {
			log.Printf("❌ [WORD_SEND_ERR] Word send failed for ChatID %d: %v", chatID, err)
		}

	case "/idiom":
		log.Printf("🤖 [AI_FLOW] /idiom requested by ChatID %d.", chatID)
		_ = notifier.Send(chatID, "🔄 <b>Finding an idiom for you...</b>")

		card, err := serveContent(ctx, chain, store, chatID, kindIdiom, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Idiom generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Idiom delivered to ChatID %d.", chatID)
		_ = notifier.Send(chatID, card)

	case "/level":
		handleLevel(store, notifier, chatID, args)

	case "/interval":
		handleInterval(store, notifier, chatID, args)

	case "/tts":
		handleTTS(store, notifier, chatID, args)

	case "/stats":
		log.Printf("📊 [STATS] requested by ChatID %d.", chatID)
		stats, err := store.UserStats(chatID)
		if err != nil {
			log.Printf("❌ [STATS_ERR] Could not build stats for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't pull your stats right now. Please try again.")
			return
		}
		_ = notifier.Send(chatID, formatStats(stats))

	case "/quiz":
		log.Printf("🧩 [QUIZ] requested by ChatID %d.", chatID)
		handleQuiz(store, notifier, chatID)

	case "/pause":
		if err := store.SetPaused(chatID, true); err != nil {
			log.Printf("❌ [PAUSE_ERR] Could not pause ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't pause right now. Please try again.")
			return
		}
		log.Printf("⏸️  [PAUSE] ChatID %d paused scheduled sends.", chatID)
		_ = notifier.Send(chatID, "⏸️ <b>Paused.</b>\n\nYou won't receive scheduled drills or words until you send /resume. You can still use /drill and /word anytime.")

	case "/resume":
		if err := store.SetPaused(chatID, false); err != nil {
			log.Printf("❌ [RESUME_ERR] Could not resume ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't resume right now. Please try again.")
			return
		}
		log.Printf("▶️  [RESUME] ChatID %d resumed scheduled sends.", chatID)
		_ = notifier.Send(chatID, "▶️ <b>Resumed!</b>\n\nScheduled practice is back on — see you every 30 minutes (quiet hours aside).")

	case "/reset":
		log.Printf("♻️  [RESET] ChatID %d requested history wipe.", chatID)
		clearedVerbs, err := store.ResetSentWords(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear verb history for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}
		clearedWords, err := store.ResetSentVocab(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear vocab history for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}
		clearedIdioms, err := store.ResetSentIdiom(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear idiom history for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}

		log.Printf("✅ [RESET] Cleared %d verbs, %d words and %d idioms for ChatID %d.", clearedVerbs, clearedWords, clearedIdioms, chatID)
		_ = notifier.Send(chatID, fmt.Sprintf(
			"♻️ <b>History reset!</b>\n\nCleared <b>%d</b> practiced verbs, <b>%d</b> vocabulary words and <b>%d</b> idioms. You may see them again in future sends.", clearedVerbs, clearedWords, clearedIdioms,
		))

	case "/metrics":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleMetrics(store, chain, notifier, chatID)

	case "/announce":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		announceText := ""
		if spaceIdx := strings.Index(msg.Text, " "); spaceIdx != -1 {
			announceText = strings.TrimSpace(msg.Text[spaceIdx+1:])
		}
		handleAnnounce(store, notifier, chatID, announceText)

	case "/health":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleHealth(store, chain, notifier, chatID)

	default:
		if strings.HasPrefix(command, "/") {
			log.Printf("ℹ️  [ROUTER_UNHANDLED] Unknown command %q from ChatID %d.", msg.Text, chatID)
			_ = notifier.Send(chatID, "🤖 I don't know that command. Try /drill, /word, /quiz, /tts, /stats or /help — or just send me any word to look it up!")
			return
		}
		// Plain text (no leading slash) is treated as a word lookup (Change M).
		handleWordLookup(ctx, chain, store, notifier, chatID, msg.Text)
	}
}

// handleLevel handles the /level command. With a valid argument it sets the
// level directly; otherwise it shows the current level with inline buttons.
func handleLevel(store *Store, notifier Notifier, chatID int64, args []string) {
	current := store.GetLevel(chatID)

	if len(args) > 0 {
		level, ok := normalizeLevel(args[0])
		if !ok {
			_ = notifier.Send(chatID, "🤔 I don't know that level. Choose <b>beginner</b>, <b>intermediate</b> or <b>advanced</b>.")
			return
		}
		if err := store.SetLevel(chatID, level); err != nil {
			log.Printf("❌ [LEVEL_ERR] Could not set level for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't change your level right now. Please try again.")
			return
		}
		log.Printf("🎚️  [LEVEL] ChatID %d set level to %s.", chatID, level)
		_ = notifier.Send(chatID, fmt.Sprintf("✅ Difficulty set to <b>%s</b>. New drills and words will match it.", levelLabel(level)))
		return
	}

	keyboard := levelKeyboard(current)
	text := fmt.Sprintf(
		"🎚️ <b>Difficulty level</b>\n\nYour current level is <b>%s</b>.\nTap a button to change it:",
		levelLabel(current),
	)
	if err := notifier.SendKeyboard(chatID, text, keyboard); err != nil {
		log.Printf("❌ [LEVEL_ERR] Could not send level keyboard to ChatID %d: %v", chatID, err)
	}
}

// handleWordLookup treats a plain (non-command) message as a vocabulary lookup
// (Change M): it generates a /word-style card for the user-supplied term at their
// level, translating from another language when needed, then pools and records it.
func handleWordLookup(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, chatID int64, text string) {
	term := strings.TrimSpace(text)
	fields := strings.Fields(term)
	if len(fields) == 0 {
		return
	}
	if len(fields) > 4 || len([]rune(term)) > 40 {
		_ = notifier.Send(chatID, "🔎 Send me a single word (or a short phrase) and I'll explain it — that looked more like a sentence!")
		return
	}

	log.Printf("🔎 [LOOKUP] ChatID %d looked up %q.", chatID, term)
	_ = notifier.Send(chatID, "🔄 <b>Looking that up...</b>")

	level := store.GetLevel(chatID)
	card, word, meaning, provider, err := generateWordFor(ctx, chain, level, term)
	if err != nil {
		log.Printf("❌ [LOOKUP_ERR] Generation failed for ChatID %d term %q: %v", chatID, term, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't look that up right now. Please try again.")
		return
	}

	// Treat a successful lookup like an on-demand /word: pool it and record it so
	// it counts toward /stats, feeds the daily review, and isn't repeated.
	if word != "" {
		if err := store.AddToPool(kindWord, level, word, meaning, card); err != nil {
			log.Printf("⚠️  [LOOKUP] Could not pool %q: %v", word, err)
		}
		if err := store.recordSentFor(kindWord, chatID, word); err != nil {
			log.Printf("⚠️  [LOOKUP] Could not record %q for chat %d: %v", word, chatID, err)
		}
	}
	log.Printf("✅ [LOOKUP] Delivered %q (resolved %q) to chat %d via %s.", term, word, chatID, provider)
	if err := sendWordCardWithTTS(ctx, store, notifier, chatID, card); err != nil {
		log.Printf("❌ [LOOKUP_ERR] Send failed for chat %d term %q: %v", chatID, term, err)
	}
}

// ---------------------------------------------------------------------------
// Admin commands (Change J) — gated by MAINTAINER_CHAT_ID
// ---------------------------------------------------------------------------

// isMaintainer reports whether chatID matches the configured maintainer.
func isMaintainer(chatID int64) bool {
	mID, err := strconv.ParseInt(MaintainerChatID, 10, 64)
	if err != nil {
		return false
	}
	return chatID == mID
}

// handleMetrics sends an operational summary to the maintainer (/metrics).
func handleMetrics(store *Store, chain *ProviderChain, notifier Notifier, chatID int64) {
	log.Printf("📊 [ADMIN] /metrics requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("📊 <b>Bot Metrics</b>\n\n")

	total, active, paused := store.SubscriberStats()
	b.WriteString(fmt.Sprintf("👥 Subscribers: <b>%d</b> (active: %d, paused: %d)\n\n", total, active, paused))

	b.WriteString("📦 <b>Pool depth:</b>\n")
	levels, _ := store.ActiveLevels()
	for _, kind := range []string{kindDrill, kindWord, kindIdiom} {
		for _, level := range levels {
			count, _ := store.PoolCount(kind, level)
			target := poolTargetFor(level)
			b.WriteString(fmt.Sprintf("  %s/%s: <b>%d</b>/%d\n", kind, level, count, target))
		}
	}
	b.WriteString("\n")

	totalAnswered, totalCorrect, _ := store.TotalQuizStats()
	if totalAnswered > 0 {
		pct := totalCorrect * 100 / totalAnswered
		b.WriteString(fmt.Sprintf("🧩 Quiz volume: <b>%d</b> answers (%d%% correct)\n", totalAnswered, pct))
	} else {
		b.WriteString("🧩 Quiz volume: <b>0</b> answers\n")
	}

	totalMastered, _ := store.TotalMasteredCount()
	b.WriteString(fmt.Sprintf("🧠 Words mastered (all users): <b>%d</b>\n", totalMastered))

	b.WriteString(fmt.Sprintf("\n⚡ Providers enabled: <b>%d</b>\n", len(chain.providers)))
	for _, p := range chain.providers {
		b.WriteString(fmt.Sprintf("  • %s\n", p.Name()))
	}

	_ = notifier.Send(chatID, b.String())
}

// handleAnnounce broadcasts an HTML message to all non-paused subscribers (/announce).
func handleAnnounce(store *Store, notifier Notifier, chatID int64, text string) {
	if text == "" {
		_ = notifier.Send(chatID, "Usage: <code>/announce &lt;HTML message&gt;</code>")
		return
	}
	log.Printf("📣 [ADMIN] /announce by ChatID %d: %q", chatID, text)

	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [ADMIN] Could not read subscribers: %v", err)
		_ = notifier.Send(chatID, "❌ Could not read subscribers.")
		return
	}

	sent, failed := 0, 0
	for _, id := range chats {
		if store.IsPaused(id) {
			continue
		}
		if err := notifier.Send(id, text); err != nil {
			failed++
		} else {
			sent++
		}
	}
	log.Printf("📣 [ADMIN] Announcement delivered to %d, failed %d.", sent, failed)
	_ = notifier.Send(chatID, fmt.Sprintf("📣 Announcement delivered to <b>%d</b> subscriber(s) (%d failed).", sent, failed))
}

// handleHealth sends a quick system health check to the maintainer (/health).
func handleHealth(store *Store, chain *ProviderChain, notifier Notifier, chatID int64) {
	log.Printf("🏥 [ADMIN] /health requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("🏥 <b>Health Check</b>\n\n")

	if err := store.db.Ping(); err != nil {
		b.WriteString("💾 Database: ❌ unreachable\n")
	} else {
		b.WriteString("💾 Database: ✅ OK\n")
	}

	b.WriteString(fmt.Sprintf("⚡ Providers: <b>%d</b> enabled\n", len(chain.providers)))
	for _, p := range chain.providers {
		b.WriteString(fmt.Sprintf("  • %s: ✅\n", p.Name()))
	}

	now := time.Now().In(appLocation)
	b.WriteString(fmt.Sprintf("\n🕐 Server time: <b>%s</b>\n", now.Format("2006-01-02 15:04:05 MST")))
	b.WriteString(fmt.Sprintf("🌙 Quiet hours: %s–%s", quietStart, quietEnd))
	if isQuietHours(time.Now()) {
		b.WriteString(" (currently quiet)")
	}
	b.WriteString("\n")

	_ = notifier.Send(chatID, b.String())
}

// levelKeyboard builds a one-row inline keyboard of the three levels, marking the
// current one with a check.
func levelKeyboard(current string) [][]inlineButton {
	var row []inlineButton
	for _, l := range allLevels {
		label := levelLabel(l)
		if l == current {
			label = "✅ " + label
		}
		row = append(row, inlineButton{Text: label, CallbackData: "level:" + l})
	}
	return [][]inlineButton{row}
}

// handleInterval handles the /interval command. With a valid numeric argument it
// sets the send interval directly; otherwise it shows the current interval with
// an inline keyboard of the allowed options.
func handleInterval(store *Store, notifier Notifier, chatID int64, args []string) {
	current := store.GetInterval(chatID)

	if len(args) > 0 {
		minutes, perr := strconv.Atoi(strings.TrimSpace(args[0]))
		if perr != nil {
			_ = notifier.Send(chatID, "🤔 Please give the interval in minutes, e.g. <code>/interval 60</code>.")
			return
		}
		iv, ok := normalizeInterval(minutes)
		if !ok {
			_ = notifier.Send(chatID, "🤔 That's not one of the options. Choose one of: "+intervalOptionsText()+".")
			return
		}
		if err := store.SetInterval(chatID, iv); err != nil {
			log.Printf("❌ [INTERVAL_ERR] Could not set interval for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't change your interval right now. Please try again.")
			return
		}
		log.Printf("⏱️  [INTERVAL] ChatID %d set interval to %d min.", chatID, iv)
		_ = notifier.Send(chatID, fmt.Sprintf("✅ Send interval set to <b>%s</b>. You'll get practice that often (quiet hours aside).", intervalLabel(iv)))
		return
	}

	text := fmt.Sprintf(
		"⏱️ <b>Send interval</b>\n\nYou currently receive practice every <b>%s</b>.\nTap a button to change how often:",
		intervalLabel(current),
	)
	if err := notifier.SendKeyboard(chatID, text, intervalKeyboard(current)); err != nil {
		log.Printf("❌ [INTERVAL_ERR] Could not send interval keyboard to ChatID %d: %v", chatID, err)
	}
}

// handleTTS handles /tts on|off, controlling pronunciation voice messages per user.
func handleTTS(store *Store, notifier Notifier, chatID int64, args []string) {
	current := store.GetTTSEnabled(chatID)

	if len(args) > 0 {
		mode := strings.ToLower(strings.TrimSpace(args[0]))
		var enabled bool
		switch mode {
		case "on":
			enabled = true
		case "off":
			enabled = false
		default:
			_ = notifier.Send(chatID, "Usage: <code>/tts on</code> or <code>/tts off</code>.")
			return
		}

		if err := store.SetTTSEnabled(chatID, enabled); err != nil {
			log.Printf("❌ [TTS_ERR] Could not set TTS preference for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't change your audio preference right now. Please try again.")
			return
		}

		if enabled {
			msg := "🔊 Pronunciation audio is now <b>ON</b>."
			if !ttsEnabled {
				msg += "\n\nℹ️ It is currently disabled globally by the bot administrator."
			}
			_ = notifier.Send(chatID, msg)
			return
		}
		_ = notifier.Send(chatID, "🔇 Pronunciation audio is now <b>OFF</b>.")
		return
	}

	status := "ON"
	if !current {
		status = "OFF"
	}
	msg := fmt.Sprintf("🔊 <b>Pronunciation audio</b>\n\nCurrent setting: <b>%s</b>\nUse <code>/tts on</code> or <code>/tts off</code>.", status)
	if !ttsEnabled {
		msg += "\n\nℹ️ It is currently disabled globally by the bot administrator."
	}
	_ = notifier.Send(chatID, msg)
}

// intervalKeyboard builds an inline keyboard (rows of two) of the allowed send
// intervals, marking the current one with a check.
func intervalKeyboard(current int) [][]inlineButton {
	var rows [][]inlineButton
	var row []inlineButton
	for _, iv := range allIntervals {
		label := intervalLabel(iv)
		if iv == current {
			label = "✅ " + label
		}
		row = append(row, inlineButton{Text: label, CallbackData: fmt.Sprintf("interval:%d", iv)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// intervalOptionsText renders the allowed intervals as a human-readable list.
func intervalOptionsText() string {
	labels := make([]string, len(allIntervals))
	for i, iv := range allIntervals {
		labels[i] = intervalLabel(iv)
	}
	return strings.Join(labels, ", ")
}

// handleCallback processes inline-keyboard taps (currently level selection).
func handleCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery) {
	if cb.Message == nil {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}
	chatID := cb.Message.Chat.ID

	if strings.HasPrefix(cb.Data, "level:") {
		level, ok := normalizeLevel(strings.TrimPrefix(cb.Data, "level:"))
		if !ok {
			_ = notifier.AnswerCallback(cb.ID, "Unknown level")
			return
		}
		if err := store.SetLevel(chatID, level); err != nil {
			log.Printf("❌ [LEVEL_ERR] Could not set level for ChatID %d: %v", chatID, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("🎚️  [LEVEL] ChatID %d set level to %s (via button).", chatID, level)
		_ = notifier.AnswerCallback(cb.ID, "Level set to "+levelLabel(level))
		_ = notifier.EditMessage(chatID, cb.Message.MessageID,
			fmt.Sprintf("🎚️ <b>Difficulty level</b>\n\nDifficulty set to <b>%s</b>. New drills and words will match it.", levelLabel(level)),
			levelKeyboard(level),
		)
		return
	}

	if strings.HasPrefix(cb.Data, "interval:") {
		minutes, perr := strconv.Atoi(strings.TrimPrefix(cb.Data, "interval:"))
		if perr != nil {
			_ = notifier.AnswerCallback(cb.ID, "Unknown interval")
			return
		}
		iv, ok := normalizeInterval(minutes)
		if !ok {
			_ = notifier.AnswerCallback(cb.ID, "Unknown interval")
			return
		}
		if err := store.SetInterval(chatID, iv); err != nil {
			log.Printf("❌ [INTERVAL_ERR] Could not set interval for ChatID %d: %v", chatID, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("⏱️  [INTERVAL] ChatID %d set interval to %d min (via button).", chatID, iv)
		_ = notifier.AnswerCallback(cb.ID, "Interval set to "+intervalLabel(iv))
		_ = notifier.EditMessage(chatID, cb.Message.MessageID,
			fmt.Sprintf("⏱️ <b>Send interval</b>\n\nYou'll now receive practice every <b>%s</b> (quiet hours aside).", intervalLabel(iv)),
			intervalKeyboard(iv),
		)
		return
	}

	if strings.HasPrefix(cb.Data, "srs:") {
		handleReviewCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "quiz:") {
		handleQuizCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "drill:") {
		handleDrillCallback(store, notifier, cb, chatID)
		return
	}

	log.Printf("ℹ️  [CALLBACK_UNHANDLED] Unknown callback data %q from ChatID %d.", cb.Data, chatID)
	_ = notifier.AnswerCallback(cb.ID, "")
}

// handleReviewCallback applies a spaced-repetition self-grade ("Knew it" /
// "Forgot") from a memory-check card tap (Change D). Callback data is of the
// form "srs:known:<word>" or "srs:forgot:<word>".
func handleReviewCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "srs:")
	action, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	now := time.Now()
	var (
		ok      bool
		err     error
		toast   string
		confirm string
	)
	switch action {
	case "known":
		ok, err = store.ApplyReviewKnown(chatID, word, now)
		toast = "Great — pushed further out 👍"
		confirm = fmt.Sprintf("✅ <b>%s</b> — nice! I'll show it again later, spaced further out.", word)
	case "forgot":
		ok, err = store.ApplyReviewForgot(chatID, word, now)
		toast = "No worries — you'll see it again soon"
		confirm = fmt.Sprintf("❌ <b>%s</b> — no problem. I'll bring it back soon so it sticks.", word)
	default:
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	if err != nil {
		log.Printf("❌ [SRS] Could not apply %q for chat %d word %q: %v", action, chatID, word, err)
		_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
		return
	}
	if !ok {
		_ = notifier.AnswerCallback(cb.ID, "That review has expired")
		return
	}
	log.Printf("🧠 [SRS] ChatID %d graded %q as %q.", chatID, word, action)
	_ = notifier.AnswerCallback(cb.ID, toast)
	if cb.Message != nil {
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, confirm, [][]inlineButton{})
	}
}

// sendDrill delivers a drill as page 1 of a paged message with prev/next
// navigation buttons (Change N). When the drill is a single page (e.g. legacy
// content that can't be parsed into forms) it falls back to a plain send.
func sendDrill(notifier Notifier, chatID int64, fullText string) error {
	text, total := renderDrillPage(fullText, 1)
	kb := drillNavKeyboard(parseVerb(fullText), 1, total)
	if len(kb) == 0 {
		return notifier.Send(chatID, text)
	}
	return notifier.SendKeyboard(chatID, text, kb)
}

// handleDrillCallback turns the page of a paged drill in place. Callback data is
// "drill:<page>:<verb>"; the full drill is reloaded from the pool by verb and the
// requested page is re-rendered via editMessageText. "drill:noop" (the page
// indicator button) is acknowledged silently.
func handleDrillCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "drill:")
	if rest == "noop" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}
	pageStr, term, found := strings.Cut(rest, ":")
	page, perr := strconv.Atoi(pageStr)
	if !found || term == "" || perr != nil {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	fullText, ok, err := store.DrillText(term)
	if err != nil {
		log.Printf("❌ [DRILL] Could not load drill %q for chat %d: %v", term, chatID, err)
		_ = notifier.AnswerCallback(cb.ID, "Could not load, try again")
		return
	}
	if !ok {
		_ = notifier.AnswerCallback(cb.ID, "That drill is no longer available")
		return
	}

	text, total := renderDrillPage(fullText, page)
	_ = notifier.AnswerCallback(cb.ID, "")
	if cb.Message != nil {
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, text, drillNavKeyboard(term, page, total))
	}
}

// ---------------------------------------------------------------------------
// Notifier interface (strategy pattern for Telegram sends — testable via DI)
// ---------------------------------------------------------------------------

// Notifier abstracts the four Telegram Bot API send operations so handlers and
// schedulers can be tested without HTTP calls. The production implementation is
// telegramNotifier; tests inject a mockNotifier.
type Notifier interface {
	Send(chatID int64, text string) error
	SendWithMessageID(chatID int64, text string) (int64, error)
	SendVoice(chatID int64, voice []byte, filename string, replyToMessageID int64) error
	SendKeyboard(chatID int64, text string, keyboard [][]inlineButton) error
	EditMessage(chatID, messageID int64, text string, keyboard [][]inlineButton) error
	AnswerCallback(callbackID, text string) error
}

// telegramNotifier is the real Notifier that talks to the Telegram Bot API.
type telegramNotifier struct{}

func (n *telegramNotifier) Send(chatID int64, text string) error {
	_, err := n.SendWithMessageID(chatID, text)
	return err
}

func (n *telegramNotifier) SendWithMessageID(chatID int64, text string) (int64, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TelegramBotToken)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	jsonPayload, _ := json.Marshal(payload)

	log.Printf("➔ [HTTP_POST] sendMessage to ChatID %d | payload %d bytes", chatID, len(jsonPayload))
	resp, err := telegramHTTPClient.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("telegram sendMessage read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return 0, fmt.Errorf("telegram sendMessage parse error: %w", err)
	}
	return parsed.Result.MessageID, nil
}

func (n *telegramNotifier) SendVoice(chatID int64, voice []byte, filename string, replyToMessageID int64) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendVoice", TelegramBotToken)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if replyToMessageID > 0 {
		if err := writer.WriteField("reply_to_message_id", strconv.FormatInt(replyToMessageID, 10)); err != nil {
			return err
		}
	}

	part, err := writer.CreateFormFile("voice", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(voice); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendVoice returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// telegramPost marshals payload and POSTs it to the given Bot API method.
func telegramPost(method string, payload map[string]interface{}) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", TelegramBotToken, method)
	jsonPayload, _ := json.Marshal(payload)

	resp, err := telegramHTTPClient.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram %s returned status %d: %s", method, resp.StatusCode, string(respBody))
	}
	return nil
}

func (n *telegramNotifier) SendKeyboard(chatID int64, text string, keyboard [][]inlineButton) error {
	log.Printf("➔ [HTTP_POST] sendMessage(+keyboard) to ChatID %d", chatID)
	return telegramPost("sendMessage", map[string]interface{}{
		"chat_id":      chatID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

func (n *telegramNotifier) EditMessage(chatID, messageID int64, text string, keyboard [][]inlineButton) error {
	return telegramPost("editMessageText", map[string]interface{}{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

func (n *telegramNotifier) AnswerCallback(callbackID, text string) error {
	payload := map[string]interface{}{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	return telegramPost("answerCallbackQuery", payload)
}

// sendPendingChangelogs delivers any changelog entries the user has not yet
// seen and marks them as delivered immediately after each successful send.
func sendPendingChangelogs(store *Store, notifier Notifier, chatID int64) {
	unseen, err := store.UnseenChangelogs(chatID)
	if err != nil {
		log.Printf("⚠️  [CHANGELOG] Could not fetch unseen changelogs for ChatID %d: %v", chatID, err)
		return
	}
	for _, entry := range unseen {
		if err := notifier.Send(chatID, entry.Text); err != nil {
			log.Printf("❌ [CHANGELOG] Failed to deliver v%s to ChatID %d: %v", entry.Version, chatID, err)
			continue
		}
		if err := store.MarkChangelogSeen(chatID, entry.Version); err != nil {
			log.Printf("⚠️  [CHANGELOG] Could not mark v%s seen for ChatID %d: %v", entry.Version, chatID, err)
		}
		log.Printf("📣 [CHANGELOG] Delivered v%s to ChatID %d.", entry.Version, chatID)
	}
}

// ---------------------------------------------------------------------------
// SQLite store
// ---------------------------------------------------------------------------

// openStore opens (or creates) the SQLite database, applies the schema and
// migrates any legacy subscribers.json file on first run.
func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS subscribers (
		chat_id    INTEGER PRIMARY KEY,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS sent_words (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_words_chat ON sent_words(chat_id);
	CREATE TABLE IF NOT EXISTS sent_vocab (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_vocab_chat ON sent_vocab(chat_id);
	CREATE TABLE IF NOT EXISTS sent_idioms (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_idioms_chat ON sent_idioms(chat_id);
	CREATE TABLE IF NOT EXISTS changelog_delivery (
		chat_id INTEGER NOT NULL,
		version TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, version)
	);
	CREATE TABLE IF NOT EXISTS content_pool (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		kind       TEXT    NOT NULL,
		term       TEXT    NOT NULL,
		meaning    TEXT    DEFAULT '',
		text       TEXT    NOT NULL,
		level      TEXT    NOT NULL DEFAULT 'intermediate',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (kind, level, term)
	);
	CREATE TABLE IF NOT EXISTS daily_review_delivery (
		chat_id     INTEGER NOT NULL,
		review_date TEXT    NOT NULL,
		sent_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, review_date)
	);
	CREATE TABLE IF NOT EXISTS idiom_delivery (
		chat_id    INTEGER NOT NULL,
		idiom_date TEXT    NOT NULL,
		sent_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, idiom_date)
	);
	CREATE TABLE IF NOT EXISTS user_prefs (
		chat_id          INTEGER PRIMARY KEY,
		level            TEXT    NOT NULL DEFAULT 'intermediate',
		paused           INTEGER NOT NULL DEFAULT 0,
		interval_minutes INTEGER NOT NULL DEFAULT 30,
		tts_enabled      INTEGER NOT NULL DEFAULT 1,
		updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS review_schedule (
		chat_id       INTEGER NOT NULL,
		word          TEXT    NOT NULL,
		interval_days INTEGER NOT NULL DEFAULT 1,
		ease          REAL    NOT NULL DEFAULT 2.5,
		reps          INTEGER NOT NULL DEFAULT 0,
		due_at        DATETIME NOT NULL,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_review_due ON review_schedule(chat_id, due_at);
	CREATE TABLE IF NOT EXISTS quiz_results (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id     INTEGER NOT NULL,
		word        TEXT    NOT NULL,
		correct     INTEGER NOT NULL,
		answered_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_quiz_chat ON quiz_results(chat_id);
	CREATE TABLE IF NOT EXISTS weekly_digest_delivery (
		chat_id    INTEGER NOT NULL,
		week_start TEXT    NOT NULL,
		sent_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, week_start)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	store.migrateLegacyJSON()

	log.Println("💾 [DB] SQLite store ready (subscribers, history, content pool, prefs).")
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// migrate applies additive column migrations to pre-existing databases so older
// deployments pick up new columns without losing data.
func (s *Store) migrate() error {
	// content_pool.level was added in v1.4.0; older DBs lack the column.
	if !s.columnExists("content_pool", "level") {
		log.Println("💾 [DB_MIGRATE] Adding content_pool.level column...")
		if _, err := s.db.Exec(
			"ALTER TABLE content_pool ADD COLUMN level TEXT NOT NULL DEFAULT 'intermediate'",
		); err != nil {
			return err
		}
	}
	// Pre-v1.13 used UNIQUE(kind, term), which prevented a term from being pooled
	// at more than one level and starved non-default levels (a verb pooled at the
	// default level could never be pooled at, say, advanced). Rebuild the table
	// with UNIQUE(kind, level, term) so each level keeps its own independent pool.
	if !s.poolDedupIsLevelAware() {
		log.Println("💾 [DB_MIGRATE] Rebuilding content_pool with per-level dedup (kind, level, term)...")
		if _, err := s.db.Exec(`
			CREATE TABLE content_pool_v2 (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				kind       TEXT    NOT NULL,
				term       TEXT    NOT NULL,
				meaning    TEXT    DEFAULT '',
				text       TEXT    NOT NULL,
				level      TEXT    NOT NULL DEFAULT 'intermediate',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE (kind, level, term)
			);
			INSERT OR IGNORE INTO content_pool_v2 (id, kind, term, meaning, text, level, created_at)
				SELECT id, kind, term, meaning, text, level, created_at FROM content_pool;
			DROP TABLE content_pool;
			ALTER TABLE content_pool_v2 RENAME TO content_pool;
		`); err != nil {
			return err
		}
	}
	// The level-aware index is created here (after the column is guaranteed to
	// exist, and after any rebuild that would have dropped it) so it works on
	// fresh, pre-v1.4.0 and pre-v1.13 databases alike.
	if _, err := s.db.Exec(
		"CREATE INDEX IF NOT EXISTS idx_content_pool_kind ON content_pool(kind, level, created_at)",
	); err != nil {
		return err
	}
	// last_sent_at (recency, distinct from sent_at = first-sent) was added in
	// v1.13 to drive least-recently-served rotation without disturbing the
	// historical sent_at used by streaks/reviews. Backfill it from sent_at.
	for _, table := range []string{"sent_words", "sent_vocab", "sent_idioms"} {
		if s.columnExists(table, "last_sent_at") {
			continue
		}
		log.Printf("💾 [DB_MIGRATE] Adding %s.last_sent_at column...", table)
		if _, err := s.db.Exec(
			"ALTER TABLE " + table + " ADD COLUMN last_sent_at DATETIME",
		); err != nil {
			return err
		}
		if _, err := s.db.Exec(
			"UPDATE " + table + " SET last_sent_at = sent_at WHERE last_sent_at IS NULL",
		); err != nil {
			return err
		}
	}
	// user_prefs.interval_minutes was added in v1.6.0 (Change L).
	if !s.columnExists("user_prefs", "interval_minutes") {
		log.Println("💾 [DB_MIGRATE] Adding user_prefs.interval_minutes column...")
		if _, err := s.db.Exec(
			"ALTER TABLE user_prefs ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 30",
		); err != nil {
			return err
		}
	}
	// user_prefs.tts_enabled was added in v1.12.0 (Change I).
	if !s.columnExists("user_prefs", "tts_enabled") {
		log.Println("💾 [DB_MIGRATE] Adding user_prefs.tts_enabled column...")
		if _, err := s.db.Exec(
			"ALTER TABLE user_prefs ADD COLUMN tts_enabled INTEGER NOT NULL DEFAULT 1",
		); err != nil {
			return err
		}
	}
	return nil
}

// poolDedupIsLevelAware reports whether content_pool's UNIQUE constraint already
// includes the level column (UNIQUE(kind, level, term)). Pre-v1.13 databases use
// UNIQUE(kind, term) and return false, signalling that a rebuild is needed. On any
// inspection error it returns true (assume up-to-date) so we never rebuild blindly.
func (s *Store) poolDedupIsLevelAware() bool {
	rows, err := s.db.Query("PRAGMA index_list(content_pool)")
	if err != nil {
		return true
	}
	var uniqueIdx []string
	for rows.Next() {
		var (
			seq            int
			name, origin   string
			unique, partia int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partia); err != nil {
			rows.Close()
			return true
		}
		// origin "u" marks the implicit index backing a UNIQUE table constraint.
		if unique == 1 && origin == "u" {
			uniqueIdx = append(uniqueIdx, name)
		}
	}
	rows.Close()
	if len(uniqueIdx) == 0 {
		// No UNIQUE constraint found at all (unexpected) — leave it alone.
		return true
	}
	for _, idx := range uniqueIdx {
		for _, col := range s.indexColumns(idx) {
			if col == "level" {
				return true
			}
		}
	}
	return false
}

// indexColumns returns the column names participating in the given index, in order.
func (s *Store) indexColumns(index string) []string {
	rows, err := s.db.Query("PRAGMA index_info(" + index + ")")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			seqno, cid int
			name       sql.NullString
		)
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return cols
		}
		if name.Valid {
			cols = append(cols, name.String)
		}
	}
	return cols
}

// columnExists reports whether a table has a column with the given name.
func (s *Store) columnExists(table, column string) bool {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dfltValue  any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &primaryKey); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// migrateLegacyJSON imports subscribers from the old subscribers.json file (if
// present) into the SQLite database, then renames it so it runs only once.
func (s *Store) migrateLegacyJSON() {
	data, err := os.ReadFile(legacyDBFile)
	if err != nil {
		return
	}

	var legacy struct {
		Subscribers map[int64]bool `json:"subscribers"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		log.Printf("⚠️  [DB_MIGRATE] Could not parse legacy %s: %v", legacyDBFile, err)
		return
	}

	migrated := 0
	for chatID, active := range legacy.Subscribers {
		if !active {
			continue
		}
		if _, err := s.AddSubscriber(chatID); err != nil {
			log.Printf("⚠️  [DB_MIGRATE] Failed to migrate subscriber %d: %v", chatID, err)
			continue
		}
		migrated++
	}

	if err := os.Rename(legacyDBFile, legacyDBFile+".migrated"); err != nil {
		log.Printf("⚠️  [DB_MIGRATE] Could not rename legacy file: %v", err)
	}
	log.Printf("💾 [DB_MIGRATE] Imported %d legacy subscribers from %s into SQLite.", migrated, legacyDBFile)
}

// AddSubscriber inserts a subscriber and reports whether they were newly added.
func (s *Store) AddSubscriber(chatID int64) (bool, error) {
	res, err := s.db.Exec("INSERT OR IGNORE INTO subscribers (chat_id) VALUES (?)", chatID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// Subscribers returns every subscribed chat ID.
func (s *Store) Subscribers() ([]int64, error) {
	rows, err := s.db.Query("SELECT chat_id FROM subscribers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SentWords returns the list of verbs already sent to a given chat.
func (s *Store) SentWords(chatID int64) ([]string, error) {
	rows, err := s.db.Query("SELECT word FROM sent_words WHERE chat_id = ? ORDER BY sent_at", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	return words, rows.Err()
}

// RecordSentWord stores a verb as having been sent to a chat. The first send sets
// sent_at (used by streaks/reviews); every send bumps last_sent_at, which drives
// least-recently-served rotation once the user has seen the whole pool.
func (s *Store) RecordSentWord(chatID int64, word string) error {
	// last_sent_at uses millisecond precision (strftime %f) so the most-recently
	// served item is always unambiguous, even for rapid back-to-back serves.
	_, err := s.db.Exec(
		`INSERT INTO sent_words (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(word)),
	)
	return err
}

// ResetSentWords deletes all sent-word history for a chat and returns the
// number of records removed.
func (s *Store) ResetSentWords(chatID int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM sent_words WHERE chat_id = ?", chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SentVocab returns the list of vocabulary words already sent to a given chat.
func (s *Store) SentVocab(chatID int64) ([]string, error) {
	rows, err := s.db.Query("SELECT word FROM sent_vocab WHERE chat_id = ? ORDER BY sent_at", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	return words, rows.Err()
}

// RecordSentVocab stores a vocabulary word as having been sent to a chat. sent_at
// records the first send (used by streaks/reviews); last_sent_at is bumped on every
// send to drive least-recently-served rotation.
func (s *Store) RecordSentVocab(chatID int64, word string) error {
	_, err := s.db.Exec(
		`INSERT INTO sent_vocab (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(word)),
	)
	return err
}

// ResetSentVocab deletes all sent vocabulary history for a chat and returns the
// number of records removed.
func (s *Store) ResetSentVocab(chatID int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM sent_vocab WHERE chat_id = ?", chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecordSentIdiom stores an idiom as having been sent to a chat. sent_at records the
// first send; last_sent_at is bumped on every send to drive least-recently-served
// rotation.
func (s *Store) RecordSentIdiom(chatID int64, idiom string) error {
	_, err := s.db.Exec(
		`INSERT INTO sent_idioms (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(idiom)),
	)
	return err
}

// ResetSentIdiom deletes all sent idiom history for a chat and returns the number
// of records removed.
func (s *Store) ResetSentIdiom(chatID int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM sent_idioms WHERE chat_id = ?", chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkChangelogSeen records that a changelog version has been delivered to a chat.
func (s *Store) MarkChangelogSeen(chatID int64, version string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO changelog_delivery (chat_id, version) VALUES (?, ?)",
		chatID, version,
	)
	return err
}

// UnseenChangelogs returns the Changelog entries not yet delivered to chatID.
func (s *Store) UnseenChangelogs(chatID int64) ([]ChangelogEntry, error) {
	if len(Changelogs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query("SELECT version FROM changelog_delivery WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		seen[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var unseen []ChangelogEntry
	for _, entry := range Changelogs {
		if !seen[entry.Version] {
			unseen = append(unseen, entry)
		}
	}
	return unseen, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// lookupEnv reports whether an environment variable is set and returns its value.
func lookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}
