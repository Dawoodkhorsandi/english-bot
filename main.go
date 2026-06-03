package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

	// Background workers (v2):
	//   1. poolFiller keeps the pre-generated content pool stocked.
	//   2. broadcast scheduler fans pooled content out every 30 min (quiet-hour aware).
	//   3. daily review scheduler sends a bedtime word recap at local midnight.
	go poolFiller(ctx, chain, store)
	go runBroadcastScheduler(ctx, chain, store)
	go runDailyReviewScheduler(ctx, store)

	log.Println("📡 [SYSTEM] Launching Telegram incoming updates consumer engine...")
	go pollTelegramUpdates(ctx, chain, store)

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	log.Println("🚀 [SYSTEM] Startup complete. Bot is fully operational and listening.")
	sig := <-stopChan
	log.Printf("🛑 [SYSTEM] Shutdown intercept caught OS Signal: %v. Cleaning tasks...", sig)
}

func pollTelegramUpdates(ctx context.Context, chain *ProviderChain, store *Store) {
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
					handleMessage(ctx, chain, store, update.Message)
				case update.CallbackQuery != nil:
					log.Printf("🔘 [CALLBACK_DISPATCH] Update %d | Data: %q", update.UpdateID, update.CallbackQuery.Data)
					handleCallback(store, update.CallbackQuery)
				default:
					log.Printf("📝 [POLLER_SKIP] Non-message update %d. Skipping.", update.UpdateID)
				}
			}
		}
	}
}

func handleMessage(ctx context.Context, chain *ProviderChain, store *Store, msg *TelegramMessage) {
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
				_ = sendToTelegram(mID, maintainerMsg)
			}
		} else {
			// Returning user — deliver any changelogs they haven't seen yet.
			sendPendingChangelogs(store, chatID)
		}

		welcome := "👋 <b>Welcome to English Muscle Memory Bot!</b>\n\n" +
			"Every 30 minutes I send you something to practice — alternating between:\n" +
			"• 🎯 a <b>grammar drill</b> (one verb across 14 tenses)\n" +
			"• 📘 a <b>vocabulary word</b> (meaning, pronunciation, synonyms, opposites & examples)\n\n" +
			"💬 <b>Tip:</b> send me <b>any word</b> (English or Persian) and I'll explain it like a vocabulary card!\n\n" +
			"📚 <b>Commands:</b>\n" +
			"/drill — Get a grammar drill right now\n" +
			"/word — Get a vocabulary word right now\n" +
			"/level — Choose your difficulty (beginner/intermediate/advanced)\n" +
			"/interval — Choose how often you get practice\n" +
			"/stats — See your progress and streak\n" +
			"/pause — Pause scheduled sends\n" +
			"/resume — Resume scheduled sends\n" +
			"/reset — Clear your practiced history\n" +
			"/help — How it works"
		_ = sendToTelegram(chatID, welcome)

	case "/help":
		helpText := "💡 <b>How Muscle Memory Practice Works</b>\n\n" +
			"Don't just read — <b>say everything out loud!</b> Repeating correct sentences and new words fast builds the subconscious instincts you need for natural speech.\n\n" +
			"🎯 <b>Grammar drills</b> take one everyday verb through <b>14 tenses</b>:\n" +
			"Simple, Continuous, Perfect, Perfect Continuous — across past, present and future — plus the First Conditional.\n\n" +
			"📘 <b>Vocabulary words</b> give you the meaning, pronunciation, synonyms, opposites and real examples for a useful new word.\n\n" +
			"💬 <b>Look up any word:</b> just type it (English or Persian) and I'll send a full card for it.\n\n" +
			"You get one of each per hour, about 30 minutes apart (quiet overnight).\n\n" +
			"/drill — generate a grammar drill on demand\n" +
			"/word — generate a vocabulary word on demand\n" +
			"/level — set difficulty: beginner, intermediate or advanced\n" +
			"/interval — set how often scheduled practice arrives\n" +
			"/stats — see your progress, streak and totals\n" +
			"/pause — stop scheduled sends (on-demand still works)\n" +
			"/resume — re-enable scheduled sends\n" +
			"/reset — clear history and see old verbs & words again"
		_ = sendToTelegram(chatID, helpText)

	case "/drill":
		log.Printf("🤖 [AI_FLOW] /drill requested by ChatID %d.", chatID)
		_ = sendToTelegram(chatID, "🔄 <b>Generating your drill...</b>")

		drill, err := serveContent(ctx, chain, store, chatID, kindDrill, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Generation failed for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Drill delivered to ChatID %d.", chatID)
		_ = sendToTelegram(chatID, drill)

	case "/word":
		log.Printf("🤖 [AI_FLOW] /word requested by ChatID %d.", chatID)
		_ = sendToTelegram(chatID, "🔄 <b>Finding a fresh word for you...</b>")

		card, err := serveContent(ctx, chain, store, chatID, kindWord, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Word generation failed for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Word delivered to ChatID %d.", chatID)
		_ = sendToTelegram(chatID, card)

	case "/level":
		handleLevel(store, chatID, args)

	case "/interval":
		handleInterval(store, chatID, args)

	case "/stats":
		log.Printf("📊 [STATS] requested by ChatID %d.", chatID)
		stats, err := store.UserStats(chatID)
		if err != nil {
			log.Printf("❌ [STATS_ERR] Could not build stats for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't pull your stats right now. Please try again.")
			return
		}
		_ = sendToTelegram(chatID, formatStats(stats))

	case "/pause":
		if err := store.SetPaused(chatID, true); err != nil {
			log.Printf("❌ [PAUSE_ERR] Could not pause ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't pause right now. Please try again.")
			return
		}
		log.Printf("⏸️  [PAUSE] ChatID %d paused scheduled sends.", chatID)
		_ = sendToTelegram(chatID, "⏸️ <b>Paused.</b>\n\nYou won't receive scheduled drills or words until you send /resume. You can still use /drill and /word anytime.")

	case "/resume":
		if err := store.SetPaused(chatID, false); err != nil {
			log.Printf("❌ [RESUME_ERR] Could not resume ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't resume right now. Please try again.")
			return
		}
		log.Printf("▶️  [RESUME] ChatID %d resumed scheduled sends.", chatID)
		_ = sendToTelegram(chatID, "▶️ <b>Resumed!</b>\n\nScheduled practice is back on — see you every 30 minutes (quiet hours aside).")

	case "/reset":
		log.Printf("♻️  [RESET] ChatID %d requested history wipe.", chatID)
		clearedVerbs, err := store.ResetSentWords(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear verb history for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}
		clearedWords, err := store.ResetSentVocab(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear vocab history for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}

		log.Printf("✅ [RESET] Cleared %d verbs and %d words for ChatID %d.", clearedVerbs, clearedWords, chatID)
		_ = sendToTelegram(chatID, fmt.Sprintf(
			"♻️ <b>History reset!</b>\n\nCleared <b>%d</b> practiced verbs and <b>%d</b> vocabulary words. You may see them again in future sends.", clearedVerbs, clearedWords,
		))

	default:
		if strings.HasPrefix(command, "/") {
			log.Printf("ℹ️  [ROUTER_UNHANDLED] Unknown command %q from ChatID %d.", msg.Text, chatID)
			_ = sendToTelegram(chatID, "🤖 I don't know that command. Try /drill, /word, /stats or /help — or just send me any word to look it up!")
			return
		}
		// Plain text (no leading slash) is treated as a word lookup (Change M).
		handleWordLookup(ctx, chain, store, chatID, msg.Text)
	}
}

// handleLevel handles the /level command. With a valid argument it sets the
// level directly; otherwise it shows the current level with inline buttons.
func handleLevel(store *Store, chatID int64, args []string) {
	current := store.GetLevel(chatID)

	if len(args) > 0 {
		level, ok := normalizeLevel(args[0])
		if !ok {
			_ = sendToTelegram(chatID, "🤔 I don't know that level. Choose <b>beginner</b>, <b>intermediate</b> or <b>advanced</b>.")
			return
		}
		if err := store.SetLevel(chatID, level); err != nil {
			log.Printf("❌ [LEVEL_ERR] Could not set level for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't change your level right now. Please try again.")
			return
		}
		log.Printf("🎚️  [LEVEL] ChatID %d set level to %s.", chatID, level)
		_ = sendToTelegram(chatID, fmt.Sprintf("✅ Difficulty set to <b>%s</b>. New drills and words will match it.", levelLabel(level)))
		return
	}

	keyboard := levelKeyboard(current)
	text := fmt.Sprintf(
		"🎚️ <b>Difficulty level</b>\n\nYour current level is <b>%s</b>.\nTap a button to change it:",
		levelLabel(current),
	)
	if err := sendToTelegramWithKeyboard(chatID, text, keyboard); err != nil {
		log.Printf("❌ [LEVEL_ERR] Could not send level keyboard to ChatID %d: %v", chatID, err)
	}
}

// handleWordLookup treats a plain (non-command) message as a vocabulary lookup
// (Change M): it generates a /word-style card for the user-supplied term at their
// level, translating from another language when needed, then pools and records it.
func handleWordLookup(ctx context.Context, chain *ProviderChain, store *Store, chatID int64, text string) {
	term := strings.TrimSpace(text)
	fields := strings.Fields(term)
	if len(fields) == 0 {
		return
	}
	if len(fields) > 4 || len([]rune(term)) > 40 {
		_ = sendToTelegram(chatID, "🔎 Send me a single word (or a short phrase) and I'll explain it — that looked more like a sentence!")
		return
	}

	log.Printf("🔎 [LOOKUP] ChatID %d looked up %q.", chatID, term)
	_ = sendToTelegram(chatID, "🔄 <b>Looking that up...</b>")

	level := store.GetLevel(chatID)
	card, word, meaning, provider, err := generateWordFor(ctx, chain, level, term)
	if err != nil {
		log.Printf("❌ [LOOKUP_ERR] Generation failed for ChatID %d term %q: %v", chatID, term, err)
		_ = sendToTelegram(chatID, "❌ Sorry, I couldn't look that up right now. Please try again.")
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
	_ = sendToTelegram(chatID, card)
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
func handleInterval(store *Store, chatID int64, args []string) {
	current := store.GetInterval(chatID)

	if len(args) > 0 {
		minutes, perr := strconv.Atoi(strings.TrimSpace(args[0]))
		if perr != nil {
			_ = sendToTelegram(chatID, "🤔 Please give the interval in minutes, e.g. <code>/interval 60</code>.")
			return
		}
		iv, ok := normalizeInterval(minutes)
		if !ok {
			_ = sendToTelegram(chatID, "🤔 That's not one of the options. Choose one of: "+intervalOptionsText()+".")
			return
		}
		if err := store.SetInterval(chatID, iv); err != nil {
			log.Printf("❌ [INTERVAL_ERR] Could not set interval for ChatID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't change your interval right now. Please try again.")
			return
		}
		log.Printf("⏱️  [INTERVAL] ChatID %d set interval to %d min.", chatID, iv)
		_ = sendToTelegram(chatID, fmt.Sprintf("✅ Send interval set to <b>%s</b>. You'll get practice that often (quiet hours aside).", intervalLabel(iv)))
		return
	}

	text := fmt.Sprintf(
		"⏱️ <b>Send interval</b>\n\nYou currently receive practice every <b>%s</b>.\nTap a button to change how often:",
		intervalLabel(current),
	)
	if err := sendToTelegramWithKeyboard(chatID, text, intervalKeyboard(current)); err != nil {
		log.Printf("❌ [INTERVAL_ERR] Could not send interval keyboard to ChatID %d: %v", chatID, err)
	}
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
func handleCallback(store *Store, cb *TelegramCallbackQuery) {
	if cb.Message == nil {
		_ = answerCallbackQuery(cb.ID, "")
		return
	}
	chatID := cb.Message.Chat.ID

	if strings.HasPrefix(cb.Data, "level:") {
		level, ok := normalizeLevel(strings.TrimPrefix(cb.Data, "level:"))
		if !ok {
			_ = answerCallbackQuery(cb.ID, "Unknown level")
			return
		}
		if err := store.SetLevel(chatID, level); err != nil {
			log.Printf("❌ [LEVEL_ERR] Could not set level for ChatID %d: %v", chatID, err)
			_ = answerCallbackQuery(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("🎚️  [LEVEL] ChatID %d set level to %s (via button).", chatID, level)
		_ = answerCallbackQuery(cb.ID, "Level set to "+levelLabel(level))
		_ = editMessageText(chatID, cb.Message.MessageID,
			fmt.Sprintf("🎚️ <b>Difficulty level</b>\n\nDifficulty set to <b>%s</b>. New drills and words will match it.", levelLabel(level)),
			levelKeyboard(level),
		)
		return
	}

	if strings.HasPrefix(cb.Data, "interval:") {
		minutes, perr := strconv.Atoi(strings.TrimPrefix(cb.Data, "interval:"))
		if perr != nil {
			_ = answerCallbackQuery(cb.ID, "Unknown interval")
			return
		}
		iv, ok := normalizeInterval(minutes)
		if !ok {
			_ = answerCallbackQuery(cb.ID, "Unknown interval")
			return
		}
		if err := store.SetInterval(chatID, iv); err != nil {
			log.Printf("❌ [INTERVAL_ERR] Could not set interval for ChatID %d: %v", chatID, err)
			_ = answerCallbackQuery(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("⏱️  [INTERVAL] ChatID %d set interval to %d min (via button).", chatID, iv)
		_ = answerCallbackQuery(cb.ID, "Interval set to "+intervalLabel(iv))
		_ = editMessageText(chatID, cb.Message.MessageID,
			fmt.Sprintf("⏱️ <b>Send interval</b>\n\nYou'll now receive practice every <b>%s</b> (quiet hours aside).", intervalLabel(iv)),
			intervalKeyboard(iv),
		)
		return
	}

	log.Printf("ℹ️  [CALLBACK_UNHANDLED] Unknown callback data %q from ChatID %d.", cb.Data, chatID)
	_ = answerCallbackQuery(cb.ID, "")
}

func sendToTelegram(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TelegramBotToken)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	jsonPayload, _ := json.Marshal(payload)

	log.Printf("➔ [HTTP_POST] sendMessage to ChatID %d | payload %d bytes", chatID, len(jsonPayload))
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// telegramPost marshals payload and POSTs it to the given Bot API method.
func telegramPost(method string, payload map[string]interface{}) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", TelegramBotToken, method)
	jsonPayload, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
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

// sendToTelegramWithKeyboard sends an HTML message with an inline keyboard.
func sendToTelegramWithKeyboard(chatID int64, text string, keyboard [][]inlineButton) error {
	log.Printf("➔ [HTTP_POST] sendMessage(+keyboard) to ChatID %d", chatID)
	return telegramPost("sendMessage", map[string]interface{}{
		"chat_id":      chatID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

// editMessageText edits an existing message's text and inline keyboard in place.
func editMessageText(chatID, messageID int64, text string, keyboard [][]inlineButton) error {
	return telegramPost("editMessageText", map[string]interface{}{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

// answerCallbackQuery acknowledges a button tap, optionally showing a toast.
func answerCallbackQuery(callbackID, text string) error {
	payload := map[string]interface{}{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	return telegramPost("answerCallbackQuery", payload)
}

// sendPendingChangelogs delivers any changelog entries the user has not yet
// seen and marks them as delivered immediately after each successful send.
func sendPendingChangelogs(store *Store, chatID int64) {
	unseen, err := store.UnseenChangelogs(chatID)
	if err != nil {
		log.Printf("⚠️  [CHANGELOG] Could not fetch unseen changelogs for ChatID %d: %v", chatID, err)
		return
	}
	for _, entry := range unseen {
		if err := sendToTelegram(chatID, entry.Text); err != nil {
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
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_words_chat ON sent_words(chat_id);
	CREATE TABLE IF NOT EXISTS sent_vocab (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_vocab_chat ON sent_vocab(chat_id);
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
		UNIQUE (kind, term)
	);
	CREATE TABLE IF NOT EXISTS daily_review_delivery (
		chat_id     INTEGER NOT NULL,
		review_date TEXT    NOT NULL,
		sent_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, review_date)
	);
	CREATE TABLE IF NOT EXISTS user_prefs (
		chat_id          INTEGER PRIMARY KEY,
		level            TEXT    NOT NULL DEFAULT 'intermediate',
		paused           INTEGER NOT NULL DEFAULT 0,
		interval_minutes INTEGER NOT NULL DEFAULT 30,
		updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
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
	// The level-aware index is created here (after the column is guaranteed to
	// exist) so it works on both fresh and pre-v1.4.0 databases.
	if _, err := s.db.Exec(
		"CREATE INDEX IF NOT EXISTS idx_content_pool_kind ON content_pool(kind, level, created_at)",
	); err != nil {
		return err
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
	return nil
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

// RecordSentWord stores a verb as having been sent to a chat (idempotent).
func (s *Store) RecordSentWord(chatID int64, word string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO sent_words (chat_id, word) VALUES (?, ?)",
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

// RecordSentVocab stores a vocabulary word as having been sent to a chat (idempotent).
func (s *Store) RecordSentVocab(chatID int64, word string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, ?)",
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
