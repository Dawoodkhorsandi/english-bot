package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tz database so Asia/Tehran resolves without OS tzdata

	_ "modernc.org/sqlite"

	"github.com/Dawoodkhorsandi/english-bot/internal/ai"
	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/content"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

const (
	dbFile       = "subscribers.db"
	legacyDBFile = "subscribers.json"
)

// Store wraps the SQLite connection used to persist subscribers and the
// per-user history of verbs that have already been sent.
type Store struct {
	db *sql.DB
}

func Run() {
	log.Println("⚙️  [INIT] Initializing Telegram English Muscle Memory Bot...")

	log.Printf("⚙️  [CONFIG] Telegram Token Loaded: %t (Length: %d)", config.TelegramBotToken != "YOUR_TELEGRAM_BOT_TOKEN" && config.TelegramBotToken != "", len(config.TelegramBotToken))
	log.Printf("⚙️  [CONFIG] Maintainer Chat ID Target: %s", config.MaintainerChatID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config.LoadLocation()

	log.Println("⚙️  [INIT] Building AI provider chain...")
	chain := ai.NewProviderChain(ctx)
	if !chain.HasAny() {
		log.Println("⚠️  [INIT] No AI providers enabled. Set at least one provider API key (e.g. GEMINI_API_KEY).")
	}

	store, err := openStore(dbFile)
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Failed to initialize SQLite store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Load persisted admin config overrides before any scheduling decisions.
	store.LoadBotConfig()

	// Seed the curated Leitner decks (idempotent) so the Mini App can serve them.
	if err := store.SeedDecks(); err != nil {
		log.Printf("⚠️  [DECKS] Seeding failed: %v", err)
	}

	notifier := telegram.NewNotifier()

	// Start the optional Mini App web server (requires WEB_APP_URL to be set).
	// When WEB_APP_URL is empty the server never starts, so nothing listens on
	// WEB_APP_PORT — a reverse proxy in front of it will return 502, and /stats
	// shows no Dashboard button. Log both states explicitly so that is obvious.
	if config.WebAppURL != "" {
		if !strings.HasPrefix(config.WebAppURL, "https://") {
			log.Printf("⚠️  [WEBAPP] WEB_APP_URL=%q is not https:// — Telegram rejects non-HTTPS Mini App buttons, so /stats will fail to send.", config.WebAppURL)
		}
		startWebServer(store, notifier)
		// Make the Mini App the chat's persistent menu button (the "sticky"
		// button next to the message input), so it's always one tap away.
		setChatMenuButton()
	} else {
		log.Printf("ℹ️  [WEBAPP] WEB_APP_URL not set — Mini App dashboard disabled; /stats shows text only and port %s is not served.", config.WebAppPort)
	}

	// Register bot commands with Telegram so users see a command menu.
	registerBotCommands()

	// On deploy: immediately broadcast unseen changelogs to all subscribers.
	broadcastChangelogsOnStartup(store, notifier)

	// Background workers (v2):
	//   1. poolFiller keeps the pre-generated content pool stocked.
	//   2. broadcast scheduler fans pooled content out every 30 min (quiet-hour aware).
	//   3. daily review scheduler sends a bedtime word recap at local midnight.
	//   4. spaced-repetition scheduler resurfaces due words for review (Change D).
	//   5. quiz scheduler periodically tests learned words (Change E).
	//   6. weekly digest scheduler sends a recap every DIGEST_DAY at DIGEST_TIME (Change K).
	//   7. daily tip scheduler sends one grammar tip at TIP_TIME.
	//   8. nightly backup scheduler sends SQLite snapshot to maintainer at BACKUP_TIME.
	//   9. collocation scheduler sends one collocation card at COLLOCATION_TIME.
	//  10. mini story scheduler sends one reading-practice story at STORY_TIME.
	go poolFiller(ctx, chain, store)
	go runBroadcastScheduler(ctx, chain, store, notifier)
	go runDailyReviewScheduler(ctx, store, notifier)
	go runReviewScheduler(ctx, store, notifier)
	go runQuizScheduler(ctx, store, notifier)
	go runWeeklyDigestScheduler(ctx, store, notifier)
	go runIdiomScheduler(ctx, chain, store, notifier)
	go runDailyTipScheduler(ctx, chain, store, notifier)
	go runDBBackupScheduler(ctx, store, notifier)
	go runCollocationScheduler(ctx, chain, store, notifier)
	go runStoryScheduler(ctx, chain, store, notifier)
	go runDeckBackfill(ctx, chain, store)

	log.Println("📡 [SYSTEM] Launching Telegram incoming updates consumer engine...")
	go pollTelegramUpdates(ctx, chain, store, notifier)

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	log.Println("🚀 [SYSTEM] Startup complete. Bot is fully operational and listening.")
	sig := <-stopChan
	log.Printf("🛑 [SYSTEM] Shutdown intercept caught OS Signal: %v. Cleaning tasks...", sig)
}

func pollTelegramUpdates(ctx context.Context, chain *ai.ProviderChain, store *Store, notifier telegram.Notifier) {
	var offset int64 = 0
	client := &http.Client{Timeout: 35 * time.Second}

	log.Println("📡 [POLLER] Thread loop listening. Long-polling via Telegram Gateway started.")
	for {
		select {
		case <-ctx.Done():
			log.Println("📡 [POLLER] Thread context killed. Terminating polling interface safely.")
			return
		default:
			url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s",
				config.TelegramBotToken, offset,
				"%5B%22message%22%2C%22callback_query%22%2C%22poll_answer%22%5D")

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
				Ok          bool              `json:"ok"`
				Description string            `json:"description"`
				Result      []telegram.Update `json:"result"`
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
				case update.PollAnswer != nil:
					log.Printf("🗳️ [POLL_ANSWER] Update %d | Poll %q", update.UpdateID, update.PollAnswer.PollID)
					go handleQuizPollAnswer(store, update.PollAnswer)
				default:
					log.Printf("📝 [POLLER_SKIP] Non-message update %d. Skipping.", update.UpdateID)
				}
			}
		}
	}
}

func handleMessage(ctx context.Context, chain *ai.ProviderChain, store *Store, notifier telegram.Notifier, msg *telegram.Message) {
	if msg.Text == "" {
		log.Printf("ℹ️  [ROUTER] Ignoring empty message ID %d.", msg.MessageID)
		return
	}

	chatID := msg.Chat.ID
	username := "unknown"
	firstName := ""
	if msg.From != nil {
		if msg.From.Username != "" {
			username = msg.From.Username
		}
		firstName = msg.From.FirstName
		store.SetFirstName(chatID, firstName)
	}

	log.Printf("🎮 [COMMAND_MATCH] %q from @%s (ID: %d)", msg.Text, username, chatID)

	// Map persistent reply-keyboard button labels to slash commands before
	// splitting on whitespace — the label includes a space (e.g. "🎯 Drill")
	// so it must be matched against the full text, not just the first field.
	trimmed := strings.TrimSpace(msg.Text)
	switch trimmed {
	case "📘 Word":
		trimmed = "/word"
	case "🎯 Drill":
		trimmed = "/drill"
	case "🧩 Quiz":
		trimmed = "/quiz"
	case "📊 Stats":
		trimmed = "/stats"
	}

	fields := strings.Fields(trimmed)
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

			mID, parseErr := strconv.ParseInt(config.MaintainerChatID, 10, 64)
			if parseErr != nil {
				log.Printf("❌ [PARSE_ERR] Invalid MaintainerChatID %q: %v", config.MaintainerChatID, parseErr)
			} else {
				_ = notifier.Send(mID, maintainerMsg)
			}
		} else {
			// Returning user — deliver any changelogs they haven't seen yet.
			sendPendingChangelogs(store, notifier, chatID)
		}

		greeting := "👋"
		if firstName != "" {
			greeting = fmt.Sprintf("👋 Hey <b>%s</b>!", firstName)
		}
		welcome := greeting + " <b>Welcome to English Muscle Memory Bot!</b>\n\n" +
			"I'll send you grammar drills and vocabulary words on a schedule, alternating between:\n" +
			"• 🎯 a <b>grammar drill</b> (one verb across 21 forms — tap ▶️ to page through tenses, conditionals & more)\n" +
			"• 📘 a <b>vocabulary word</b> (meaning, pronunciation, synonyms, opposites & examples)\n\n" +
			"💬 <b>Tip:</b> send me <b>any word</b> (English or Persian) and I'll explain it like a vocabulary card!\n\n" +
			"⏱ <b>Start here:</b> use /setup to tell me how much time you have per day — I'll tune everything automatically.\n\n" +
			"📚 <b>Commands:</b>\n" +
			"/setup — Quick-setup based on your daily time budget\n" +
			"/settings — View and change all settings\n" +
			"/drill — Get a grammar drill right now\n" +
			"/word — Get a vocabulary word right now\n" +
			"/idiom — Get an idiom of the day\n" +
			"/collocation — Get a natural word partnership to learn\n" +
			"/story — Get a mini story to read at your level\n" +
			"/tip — Get a grammar tip right now\n" +
			"/quiz — Test yourself on a word you've learned\n" +
			"/grammar — Bite-size grammar lessons (easy → advanced)\n" +
			"/level — Choose your difficulty (beginner/intermediate/upper-intermediate/advanced)\n" +
			"/interval — Choose how often you get practice\n" +
			"/tts — Turn pronunciation audio on/off\n" +
			"/stats — See your progress and streak\n" +
			"/pause — Pause scheduled sends\n" +
			"/resume — Resume scheduled sends\n" +
			"/reset — Clear your practiced history\n" +
			"/help — How it works"
		replyKB := [][]string{{"📘 Word", "🎯 Drill"}, {"🧩 Quiz", "📊 Stats"}}
		_ = notifier.SendWithReplyKeyboard(chatID, welcome, replyKB)

	case "/help":
		helpText := "💡 <b>How Muscle Memory Practice Works</b>\n\n" +
			"Don't just read — <b>say everything out loud!</b> Repeating correct sentences and new words fast builds the subconscious instincts you need for natural speech.\n\n" +
			"🎯 <b>Grammar drills</b> take one everyday verb through <b>21 forms</b>, split into easy pages — tap <b>◀️ Back</b> / <b>Next ▶️</b> to move between them:\n" +
			"present, past & future tenses (Simple, Continuous, Perfect, Perfect Continuous), all four conditionals, plus modals, passive, imperative and <i>used to</i>.\n\n" +
			"📘 <b>Vocabulary words</b> give you the meaning, pronunciation, synonyms, opposites and real examples for a useful new word.\n\n" +
			"🗣️ <b>Idiom of the day:</b> a common English expression with its meaning and real examples — sent once a day, or anytime via /idiom.\n\n" +
			"🔗 <b>Collocation of the day:</b> a natural word partnership (like <i>make a decision</i>) with examples and the mistakes to avoid — sent once a day, or anytime via /collocation.\n\n" +
			"📖 <b>Mini stories:</b> a short story at your level with key vocabulary and a comprehension question — sent once a day, or anytime via /story.\n\n" +
			"🧠 <b>Spaced repetition:</b> words you've learned come back as quick <b>memory checks</b> at growing intervals — tap ✅ Knew it / ❌ Forgot and I'll tune when you see each one next.\n\n" +
			"🧩 <b>Quizzes:</b> multiple-choice questions test your recall — send /quiz anytime, and one pops up now and then. Your answers also tune your review schedule.\n\n" +
			"📖 <b>Grammar lessons:</b> bite-size lessons from easy to advanced — each shows the pattern, a clear explanation, examples and quick practice. Send /grammar anytime, or open the app.\n\n" +
			"💬 <b>Look up any word:</b> just type it (English or Persian) and I'll send a full card for it.\n\n" +
			"You get one of each per hour, about 30 minutes apart (quiet overnight).\n\n" +
			"/drill — generate a grammar drill on demand\n" +
			"/word — generate a vocabulary word on demand\n" +
			"/idiom — get a common idiom with meaning and examples\n" +
			"/collocation — get a natural word partnership with examples\n" +
			"/story — get a mini story to read at your level\n" +
			"/tip — grammar tip now; /tip on or /tip off for daily tips\n" +
			"/quiz — test yourself on a word you've already learned\n" +
			"/grammar — bite-size grammar lessons; /grammar 1 opens a lesson\n" +
			"/level — set difficulty: beginner, intermediate, upper-intermediate or advanced\n" +
			"/interval — set how often scheduled practice arrives\n" +
			"/tts — turn pronunciation audio on or off\n" +
			"/stats — see your progress, streak and totals\n" +
			"/app — open your English hub: progress, word list and more\n" +
			"/mywords — browse all your learned vocabulary\n" +
			"/bookmark — view or toggle bookmarks on important words\n" +
			"/pause — stop scheduled sends (on-demand still works)\n" +
			"/resume — re-enable scheduled sends\n" +
			"/reset — clear history and see old verbs & words again"
		_ = notifier.Send(chatID, helpText)

	case "/drill":
		log.Printf("🤖 [AI_FLOW] /drill requested by ChatID %d.", chatID)
		notifier.SendTyping(chatID)
		_ = notifier.Send(chatID, "🔄 <b>Generating your drill...</b>")

		drill, _, err := serveContent(ctx, chain, store, notifier, chatID, config.KindDrill, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Drill delivered to ChatID %d.", chatID)
		_ = sendDrill(notifier, chatID, drill)
		go checkStreakCelebration(store, notifier, chatID, firstName)

	case "/word":
		log.Printf("🤖 [AI_FLOW] /word requested by ChatID %d.", chatID)
		notifier.SendTyping(chatID)
		_ = notifier.Send(chatID, "🔄 <b>Finding a fresh word for you...</b>")

		card, term, err := serveContent(ctx, chain, store, notifier, chatID, config.KindWord, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Word generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Word delivered to ChatID %d.", chatID)
		if err := sendWordCardWithTTS(ctx, store, notifier, chatID, card, term); err != nil {
			log.Printf("❌ [WORD_SEND_ERR] Word send failed for ChatID %d: %v", chatID, err)
		}
		go checkStreakCelebration(store, notifier, chatID, firstName)

	case "/idiom":
		log.Printf("🤖 [AI_FLOW] /idiom requested by ChatID %d.", chatID)
		notifier.SendTyping(chatID)
		_ = notifier.Send(chatID, "🔄 <b>Finding an idiom for you...</b>")

		card, _, err := serveContent(ctx, chain, store, notifier, chatID, config.KindIdiom, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Idiom generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Idiom delivered to ChatID %d.", chatID)
		if err := sendCardWithTTS(ctx, store, notifier, chatID, card); err != nil {
			log.Printf("❌ [IDIOM_SEND_ERR] Idiom send failed for ChatID %d: %v", chatID, err)
		}

	case "/collocation":
		log.Printf("🤖 [AI_FLOW] /collocation requested by ChatID %d.", chatID)
		notifier.SendTyping(chatID)
		_ = notifier.Send(chatID, "🔄 <b>Finding a collocation for you...</b>")

		card, _, err := serveContent(ctx, chain, store, notifier, chatID, config.KindCollocation, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Collocation generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Collocation delivered to ChatID %d.", chatID)
		if err := sendCardWithTTS(ctx, store, notifier, chatID, card); err != nil {
			log.Printf("❌ [COLLOCATION_SEND_ERR] Collocation send failed for ChatID %d: %v", chatID, err)
		}

	case "/story":
		log.Printf("🤖 [AI_FLOW] /story requested by ChatID %d.", chatID)
		notifier.SendTyping(chatID)
		_ = notifier.Send(chatID, "🔄 <b>Writing a mini story for you...</b>")

		card, _, err := serveContent(ctx, chain, store, notifier, chatID, config.KindStory, store.GetLevel(chatID), true)
		if err != nil {
			log.Printf("❌ [AI_ERR] Story generation failed for ChatID %d: %v", chatID, err)
			_ = notifier.Send(chatID, "❌ Sorry, I couldn't reach the AI right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Story delivered to ChatID %d.", chatID)
		if err := notifier.Send(chatID, card); err != nil {
			log.Printf("❌ [STORY_SEND_ERR] Story send failed for ChatID %d: %v", chatID, err)
		}

	case "/settings":
		handleSettings(store, notifier, chatID)

	case "/setup":
		handleSetup(store, notifier, chatID)

	case "/tip":
		notifier.SendTyping(chatID)
		handleTip(ctx, chain, store, notifier, chatID, args)

	case "/grammar":
		handleGrammar(store, notifier, chatID, args)

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
		if config.WebAppURL != "" {
			kb := [][]telegram.InlineButton{{{
				Text:   "📊 Full Dashboard",
				WebApp: &telegram.WebAppInfo{URL: config.WebAppURL + "/stats"},
			}}}
			_ = notifier.SendKeyboard(chatID, formatStats(stats, firstName), kb)
		} else {
			_ = notifier.Send(chatID, formatStats(stats, firstName))
		}

	case "/app":
		if config.WebAppURL == "" {
			_ = notifier.Send(chatID, "📱 The in-app experience isn't available right now. You can still use all features here in the chat.")
			return
		}
		kb := [][]telegram.InlineButton{{{
			Text:   "📱 Open the app",
			WebApp: &telegram.WebAppInfo{URL: config.WebAppURL},
		}}}
		_ = notifier.SendKeyboard(chatID,
			"📱 <b>Your English hub</b>\n\nProgress, your word list and more — all in one place. Tap below to open it.",
			kb)

	case "/quiz":
		log.Printf("🧩 [QUIZ] requested by ChatID %d.", chatID)
		handleQuiz(store, notifier, chatID)

	case "/mywords":
		handleMyWords(store, notifier, chatID, args)

	case "/bookmark":
		handleBookmarkCommand(store, notifier, chatID, args)

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

	case "/poolusage":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handlePoolUsage(store, notifier, chatID)

	case "/admin":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleAdminHelp(notifier, chatID)

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

	case "/backup":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleBackup(store, notifier, chatID)

	case "/users":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleAdminUsers(store, notifier, chatID)

	case "/config":
		if !isMaintainer(chatID) {
			_ = notifier.Send(chatID, "🔒 This command is only available to the bot maintainer.")
			return
		}
		handleAdminConfig(store, notifier, chatID)

	default:
		if strings.HasPrefix(command, "/") {
			log.Printf("ℹ️  [ROUTER_UNHANDLED] Unknown command %q from ChatID %d.", msg.Text, chatID)
			_ = notifier.Send(chatID, "🤖 I don't know that command. Try /drill, /word, /tip, /quiz, /tts, /stats or /help — or just send me any word to look it up!")
			return
		}
		// If the maintainer is in "message user" mode, intercept the text.
		if isMaintainer(chatID) && adminMsgTarget.Load() != 0 {
			handleAdminMsgSend(notifier, chatID, msg.Text)
			return
		}
		// Plain text (no leading slash) is treated as a word lookup (Change M).
		handleWordLookup(ctx, chain, store, notifier, chatID, msg.Text)
	}
}

// handleLevel handles the /level command. With a valid argument it sets the
// level directly; otherwise it shows the current level with inline buttons.
func handleLevel(store *Store, notifier telegram.Notifier, chatID int64, args []string) {
	current := store.GetLevel(chatID)

	if len(args) > 0 {
		level, ok := normalizeLevel(args[0])
		if !ok {
			_ = notifier.Send(chatID, "🤔 I don't know that level. Choose <b>beginner</b>, <b>intermediate</b>, <b>upper-intermediate</b> or <b>advanced</b>.")
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
func handleWordLookup(ctx context.Context, chain *ai.ProviderChain, store *Store, notifier telegram.Notifier, chatID int64, text string) {
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
	card, word, meaning, provider, err := content.GenerateWordFor(ctx, chain, level, term)
	if err != nil {
		log.Printf("❌ [LOOKUP_ERR] Generation failed for ChatID %d term %q: %v", chatID, term, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't look that up right now. Please try again.")
		return
	}

	// Treat a successful lookup like an on-demand /word: pool it and record it so
	// it counts toward /stats, feeds the daily review, and isn't repeated.
	if word != "" {
		if err := store.AddToPool(config.KindWord, level, word, meaning, card); err != nil {
			log.Printf("⚠️  [LOOKUP] Could not pool %q: %v", word, err)
		}
		if err := store.recordSentFor(config.KindWord, chatID, word); err != nil {
			log.Printf("⚠️  [LOOKUP] Could not record %q for chat %d: %v", word, chatID, err)
		}
	}
	log.Printf("✅ [LOOKUP] Delivered %q (resolved %q) to chat %d via %s.", term, word, chatID, provider)
	if err := sendWordCardWithTTS(ctx, store, notifier, chatID, card, word); err != nil {
		log.Printf("❌ [LOOKUP_ERR] Send failed for chat %d term %q: %v", chatID, term, err)
	}
}

// ---------------------------------------------------------------------------
// Admin commands (Change J) — gated by MAINTAINER_CHAT_ID
// ---------------------------------------------------------------------------

// isMaintainer reports whether chatID matches the configured maintainer.
func isMaintainer(chatID int64) bool {
	mID, ok := maintainerID()
	if !ok {
		return false
	}
	return chatID == mID
}

// maintainerID parses MAINTAINER_CHAT_ID and reports whether it's usable.
func maintainerID() (int64, bool) {
	mID, err := strconv.ParseInt(config.MaintainerChatID, 10, 64)
	if err != nil || mID == 0 {
		return 0, false
	}
	return mID, true
}

// handleMetrics sends an operational summary to the maintainer (/metrics).
func handleMetrics(store *Store, chain *ai.ProviderChain, notifier telegram.Notifier, chatID int64) {
	log.Printf("📊 [ADMIN] /metrics requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("📊 <b>Bot Metrics</b>\n\n")

	total, active, paused := store.SubscriberStats()
	b.WriteString(fmt.Sprintf("👥 Subscribers: <b>%d</b> (active: %d, paused: %d)\n\n", total, active, paused))

	b.WriteString("📦 <b>Pool depth:</b>\n")
	levels, _ := store.ActiveLevels()
	for _, kind := range []string{config.KindDrill, config.KindWord, config.KindIdiom, config.KindCollocation, config.KindStory} {
		for _, level := range levels {
			count, _ := store.PoolCount(kind, level)
			target := poolTargetFor(kind, level)
			b.WriteString(fmt.Sprintf("  %s/%s: <b>%d</b>/%d\n", kind, level, count, target))
		}
	}
	// Tips are level-independent — stocked only at the default level.
	tipCount, _ := store.PoolCount(config.KindTip, config.DefaultLevel)
	b.WriteString(fmt.Sprintf("  %s: <b>%d</b>/%d\n", config.KindTip, tipCount, poolTargetFor(config.KindTip, config.DefaultLevel)))
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

	providerNames := chain.ProviderNames()
	b.WriteString(fmt.Sprintf("\n⚡ Providers enabled: <b>%d</b>\n", len(providerNames)))
	for _, name := range providerNames {
		b.WriteString(fmt.Sprintf("  • %s\n", name))
	}

	_ = notifier.Send(chatID, b.String())
}

// handlePoolUsage sends the maintainer a per-kind/level breakdown of how heavily
// each pool is being consumed. For every (kind, level) it finds the single most
// active user — the one who has seen the most of the items currently pooled — and
// reports that user's consumption as a percentage of the items currently pooled.
// Each line also shows "pool <depth>/<target>": the depth is how many items exist
// right now, the target is the configured size (raised via /config). When depth is
// below target the background filler is still generating, so a high percentage of a
// not-yet-full pool is expected and self-resolves as the pool grows.
func handlePoolUsage(store *Store, notifier telegram.Notifier, chatID int64) {
	log.Printf("📈 [ADMIN] /poolusage requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("📈 <b>Pool usage</b>\n")
	b.WriteString("<i>Busiest user's consumption vs current pool depth.\n")
	b.WriteString("\"pool X/Y\" = items generated so far / configured target.</i>\n\n")

	levels, _ := store.ActiveLevels()

	// Build the (kind, level) work list: level-aware kinds across every active
	// level, plus level-independent tips at the default level.
	type poolKey struct{ kind, level string }
	var keys []poolKey
	for _, kind := range []string{config.KindDrill, config.KindWord, config.KindIdiom, config.KindCollocation, config.KindStory} {
		for _, level := range levels {
			keys = append(keys, poolKey{kind, level})
		}
	}
	keys = append(keys, poolKey{config.KindTip, config.DefaultLevel})

	for _, k := range keys {
		count, _ := store.PoolCount(k.kind, k.level)
		target := poolTargetFor(k.kind, k.level)
		label := k.kind + "/" + k.level
		if k.kind == config.KindTip {
			label = config.KindTip
		}
		if count == 0 {
			b.WriteString(fmt.Sprintf("  %s: <i>empty</i> · pool 0/%d\n", label, target))
			continue
		}
		leader, seen, ok, _ := store.PoolUsageLeader(k.kind, k.level)
		if !ok || seen == 0 {
			b.WriteString(fmt.Sprintf("  %s: <b>0%%</b> no usage · pool %d/%d\n", label, count, target))
			continue
		}
		pct := seen * 100 / count
		b.WriteString(fmt.Sprintf("  %s: <b>%d%%</b> (chat %d saw %d/%d) · pool %d/%d\n", label, pct, leader, seen, count, count, target))
	}

	_ = notifier.Send(chatID, b.String())
}

// handleAdminHelp lists every maintainer-only command so the admin doesn't have to
// remember them (they're intentionally hidden from the public /help and the Telegram
// command menu). Sent only to the maintainer.
func handleAdminHelp(notifier telegram.Notifier, chatID int64) {
	log.Printf("🛠️  [ADMIN] /admin help requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("🛠️ <b>Maintainer Commands</b>\n\n")
	b.WriteString("📊 /metrics — subscriber stats, pool depth, quiz volume, mastered count\n")
	b.WriteString("📈 /poolusage — per kind/level, the most active user's pool consumption %\n")
	b.WriteString("🏥 /health — database + enabled AI providers\n")
	b.WriteString("⚙️ /config — runtime settings panel (pool sizes, quiet hours, TTS, …)\n")
	b.WriteString("👥 /users — paginated user list, detail view & direct messaging\n")
	b.WriteString("📣 /announce &lt;text&gt; — broadcast an HTML message to all active users\n")
	b.WriteString("🗄️ /backup — send a point-in-time SQLite backup to this chat\n")
	b.WriteString("🛠️ /admin — show this list\n\n")
	b.WriteString("<i>These are visible only to you and don't appear in the command menu.</i>")

	_ = notifier.Send(chatID, b.String())
}

// handleAnnounce broadcasts an HTML message to all non-paused subscribers (/announce).
func handleAnnounce(store *Store, notifier telegram.Notifier, chatID int64, text string) {
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
func handleHealth(store *Store, chain *ai.ProviderChain, notifier telegram.Notifier, chatID int64) {
	log.Printf("🏥 [ADMIN] /health requested by ChatID %d.", chatID)

	var b strings.Builder
	b.WriteString("🏥 <b>Health Check</b>\n\n")

	if err := store.db.Ping(); err != nil {
		b.WriteString("💾 Database: ❌ unreachable\n")
	} else {
		b.WriteString("💾 Database: ✅ OK\n")
	}

	providerNames := chain.ProviderNames()
	b.WriteString(fmt.Sprintf("⚡ Providers: <b>%d</b> enabled\n", len(providerNames)))
	for _, name := range providerNames {
		b.WriteString(fmt.Sprintf("  • %s: ✅\n", name))
	}

	now := time.Now().In(config.AppLocation)
	b.WriteString(fmt.Sprintf("\n🕐 Server time: <b>%s</b>\n", now.Format("2006-01-02 15:04:05 MST")))
	b.WriteString(fmt.Sprintf("🌙 Quiet hours: %s–%s", config.QuietStart, config.QuietEnd))
	if isQuietHours(time.Now()) {
		b.WriteString(" (currently quiet)")
	}
	b.WriteString("\n")

	_ = notifier.Send(chatID, b.String())
}

// handleBackup creates a point-in-time SQLite backup and sends it to the maintainer.
func handleBackup(store *Store, notifier telegram.Notifier, chatID int64) {
	log.Printf("🗄️  [ADMIN] /backup requested by ChatID %d.", chatID)
	if err := sendMaintainerDBBackup(store, notifier, "manual /backup"); err != nil {
		log.Printf("❌ [BACKUP] Manual backup failed: %v", err)
		_ = notifier.Send(chatID, "❌ Backup failed. Check logs and try again.")
		return
	}
}

// handleAdminConfig lets the maintainer tweak selected runtime settings via an
// interactive inline-keyboard panel. All changes are persisted in bot_config
// and survive restarts.
func handleAdminConfig(store *Store, notifier telegram.Notifier, chatID int64) {
	log.Printf("⚙️  [ADMIN] /config requested by ChatID %d.", chatID)
	if err := notifier.SendKeyboard(chatID, configText(), configKeyboard()); err != nil {
		log.Printf("❌ [CONFIG] Could not send config panel to ChatID %d: %v", chatID, err)
	}
}

// configText formats the admin config panel body.
func configText() string {
	onOff := "ON"
	if !config.TTSEnabled {
		onOff = "OFF"
	}
	klOverrides, kindOverrides, levelOverrides := config.OverrideCounts()
	return fmt.Sprintf(
		"⚙️ <b>Bot Config</b> (Admin)\n\n"+
			"📦 <b>Pool target:</b> %d  •  <b>Pool min:</b> %d\n"+
			"📦 <b>Pool overrides:</b> %d kind+level, %d per-kind, %d per-level\n"+
			"🌙 <b>Quiet hours:</b> %s – %s\n"+
			"🔊 <b>Global TTS:</b> %s\n"+
			"⏱ <b>Gen spacing:</b> %s\n"+
			"🧠 <b>Review batch max:</b> %d\n\n"+
			"Tap a setting to change it.",
		config.PoolTarget, config.PoolMin,
		klOverrides, kindOverrides, levelOverrides,
		config.QuietStart, config.QuietEnd,
		onOff,
		config.GenSpacing,
		config.ReviewBatchMax,
	)
}

// configKeyboard builds the admin config panel inline keyboard.
func configKeyboard() [][]telegram.InlineButton {
	ttsLabel := "🔊 Global TTS: ON"
	if !config.TTSEnabled {
		ttsLabel = "🔊 Global TTS: OFF"
	}
	return [][]telegram.InlineButton{
		{
			{Text: fmt.Sprintf("📦 Pool target: %d", config.PoolTarget), CallbackData: "cfg:goto:pool_target"},
			{Text: fmt.Sprintf("📦 Pool min: %d", config.PoolMin), CallbackData: "cfg:goto:pool_min"},
		},
		{
			{Text: fmt.Sprintf("🌙 Quiet start: %s", config.QuietStart), CallbackData: "cfg:goto:quiet_start"},
			{Text: fmt.Sprintf("🌙 Quiet end: %s", config.QuietEnd), CallbackData: "cfg:goto:quiet_end"},
		},
		{
			{Text: ttsLabel, CallbackData: "cfg:toggle:tts"},
			{Text: fmt.Sprintf("⏱ Gen spacing: %s", config.GenSpacing), CallbackData: "cfg:goto:gen_spacing"},
		},
		{
			{Text: fmt.Sprintf("🧠 Review batch: %d", config.ReviewBatchMax), CallbackData: "cfg:goto:review_batch"},
		},
		{
			{Text: "📦 Per kind + level", CallbackData: "cfg:goto:pool_kl"},
		},
		{
			{Text: "📦 Per-kind pools", CallbackData: "cfg:goto:pool_kinds"},
			{Text: "📦 Per-level pools", CallbackData: "cfg:goto:pool_levels"},
		},
	}
}

// configPoolTargetKeyboard builds the pool target selection sub-keyboard.
func configPoolTargetKeyboard() [][]telegram.InlineButton {
	presets := []int{200, 300, 400, 500, 600, 800, 1000, 1500}
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, v := range presets {
		label := fmt.Sprintf("%d", v)
		if v == config.PoolTarget {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("cfg:pool_target:%d", v)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configPoolMinKeyboard builds the pool min selection sub-keyboard.
func configPoolMinKeyboard() [][]telegram.InlineButton {
	presets := []int{100, 150, 200, 300, 400, 500}
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, v := range presets {
		label := fmt.Sprintf("%d", v)
		if v == config.PoolMin {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("cfg:pool_min:%d", v)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configQuietStartKeyboard builds the quiet-hours start time sub-keyboard.
func configQuietStartKeyboard() [][]telegram.InlineButton {
	presets := []string{"21:00", "22:00", "23:00", "00:00", "01:00", "02:00"}
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, v := range presets {
		label := v
		if v == config.QuietStart {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: "cfg:quiet_start:" + v})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configQuietEndKeyboard builds the quiet-hours end time sub-keyboard.
func configQuietEndKeyboard() [][]telegram.InlineButton {
	presets := []string{"06:00", "07:00", "08:00", "09:00", "10:00", "11:00"}
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, v := range presets {
		label := v
		if v == config.QuietEnd {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: "cfg:quiet_end:" + v})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configGenSpacingKeyboard builds the generation spacing sub-keyboard.
func configGenSpacingKeyboard() [][]telegram.InlineButton {
	presets := []time.Duration{
		1 * time.Second, 2 * time.Second, 3 * time.Second,
		5 * time.Second, 8 * time.Second, 10 * time.Second,
	}
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, v := range presets {
		label := v.String()
		if v == config.GenSpacing {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: "cfg:gen_spacing:" + v.String()})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configReviewBatchKeyboard builds the review batch max sub-keyboard.
func configReviewBatchKeyboard() [][]telegram.InlineButton {
	presets := []int{1, 2, 3, 5}
	var row []telegram.InlineButton
	for _, v := range presets {
		label := fmt.Sprintf("%d", v)
		if v == config.ReviewBatchMax {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("cfg:review_batch:%d", v)})
	}
	return [][]telegram.InlineButton{
		row,
		{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}},
	}
}

// poolOverrideLabel renders an override value for a button label, or "default"
// when no override is set.
func poolOverrideLabel(v int, ok bool) string {
	if !ok {
		return "default"
	}
	return fmt.Sprintf("%d", v)
}

// configPoolKindsKeyboard lists every configurable content kind with its current
// per-kind pool override (or "default"), each opening a value picker.
func configPoolKindsKeyboard() [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, k := range config.ConfigurableKinds {
		v, ok := config.PoolKindOverride(k)
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("%s: %s", k, poolOverrideLabel(v, ok)),
			CallbackData: "cfg:goto:pk:" + k,
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configPoolLevelsKeyboard lists every difficulty level with its current per-level
// pool override (or "default"), each opening a value picker.
func configPoolLevelsKeyboard() [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, l := range config.AllLevels {
		v, ok := config.PoolLevelOverride(l)
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("%s: %s", levelLabel(l), poolOverrideLabel(v, ok)),
			CallbackData: "cfg:goto:pl:" + l,
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configPoolKLKindsKeyboard lists the level-aware content kinds for the per
// kind+level override flow (tips are excluded — they aren't level-partitioned).
// Tapping a kind opens its per-level picker.
func configPoolKLKindsKeyboard() [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, k := range config.ConfigurableKinds {
		if k == config.KindTip {
			continue // tips have a single, level-independent pool
		}
		row = append(row, telegram.InlineButton{
			Text:         k,
			CallbackData: "cfg:goto:klk:" + k,
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Config", CallbackData: "cfg:show"}})
	return rows
}

// configPoolKLLevelsKeyboard lists every difficulty level for a chosen kind, each
// showing its current per-(kind,level) override (or "default"), and opening a value
// picker for that exact pool (e.g. "upper-intermediate words").
func configPoolKLLevelsKeyboard(kind string) [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, l := range config.AllLevels {
		v, ok := config.PoolKindLevelOverride(kind, l)
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("%s: %s", levelLabel(l), poolOverrideLabel(v, ok)),
			CallbackData: "cfg:goto:klv:" + kind + ":" + l,
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back", CallbackData: "cfg:goto:pool_kl"}})
	return rows
}

// configPoolKLValueKeyboard builds the value picker for a specific (kind, level)
// pool. The first button clears the override; the rest are preset targets. Back
// returns to that kind's level picker.
func configPoolKLValueKeyboard(kind, level string, current int, hasOverride bool) [][]telegram.InlineButton {
	presets := []int{50, 100, 150, 200, 300, 500, 800}
	var rows [][]telegram.InlineButton

	defLabel := "♻️ Default"
	if !hasOverride {
		defLabel = "✅ ♻️ Default"
	}
	rows = append(rows, []telegram.InlineButton{{Text: defLabel, CallbackData: fmt.Sprintf("cfg:kl:%s:%s:0", kind, level)}})

	var row []telegram.InlineButton
	for _, v := range presets {
		label := fmt.Sprintf("%d", v)
		if hasOverride && v == current {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("cfg:kl:%s:%s:%d", kind, level, v)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back", CallbackData: "cfg:goto:klk:" + kind}})
	return rows
}

// configPoolValueKeyboard builds a value picker for a per-kind ("pk") or per-level
// ("pl") override. The first button clears the override (back to the global rule);
// the rest are preset targets. current/hasOverride drive the ✅ marker. The Back
// button returns to the matching kinds/levels submenu.
func configPoolValueKeyboard(prefix, item string, current int, hasOverride bool) [][]telegram.InlineButton {
	presets := []int{50, 100, 150, 200, 300, 500, 800}
	var rows [][]telegram.InlineButton

	defLabel := "♻️ Default"
	if !hasOverride {
		defLabel = "✅ ♻️ Default"
	}
	rows = append(rows, []telegram.InlineButton{{Text: defLabel, CallbackData: fmt.Sprintf("cfg:%s:%s:0", prefix, item)}})

	var row []telegram.InlineButton
	for _, v := range presets {
		label := fmt.Sprintf("%d", v)
		if hasOverride && v == current {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("cfg:%s:%s:%d", prefix, item, v)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	back := "cfg:goto:pool_kinds"
	if prefix == "pl" {
		back = "cfg:goto:pool_levels"
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back", CallbackData: back}})
	return rows
}

// handleConfigCallback processes all "cfg:*" inline button taps from the admin
// config panel. It handles show, goto (sub-keyboard), set, and toggle operations,
// then edits the message in place.
func handleConfigCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "cfg:")

	// Helper: refresh back to the main config panel.
	refresh := func(toast string) {
		_ = notifier.AnswerCallback(cb.ID, toast)
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID, configText(), configKeyboard())
		}
	}

	// cfg:show — refresh the main config panel.
	if rest == "show" {
		refresh("")
		return
	}

	// cfg:toggle:tts — flip global TTS.
	if rest == "toggle:tts" {
		config.TTSEnabled = !config.TTSEnabled
		val := "false"
		if config.TTSEnabled {
			val = "true"
		}
		_ = store.SetBotConfig("tts_enabled", val)
		label := "OFF"
		if config.TTSEnabled {
			label = "ON"
		}
		log.Printf("⚙️  [ADMIN] /config toggled tts_enabled -> %s by ChatID %d.", label, chatID)
		refresh("Global TTS: " + label)
		return
	}

	// cfg:goto:<key> — show sub-keyboard for a config key.
	if strings.HasPrefix(rest, "goto:") {
		_ = notifier.AnswerCallback(cb.ID, "")
		if cb.Message == nil {
			return
		}
		key := strings.TrimPrefix(rest, "goto:")
		// Dynamic per-kind / per-level value pickers: "goto:pk:<kind>" / "goto:pl:<level>".
		if kind, ok := strings.CutPrefix(key, "pk:"); ok {
			if !config.IsConfigurableKind(kind) {
				return
			}
			cur, has := config.PoolKindOverride(kind)
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>Pool size — %s</b>\n\nCurrent: <b>%s</b>\n♻️ Default uses the global target/min rule.\nTap to change:", kind, poolOverrideLabel(cur, has)),
				configPoolValueKeyboard("pk", kind, cur, has),
			)
			return
		}
		if level, ok := strings.CutPrefix(key, "pl:"); ok {
			lv, valid := normalizeLevel(level)
			if !valid {
				return
			}
			cur, has := config.PoolLevelOverride(lv)
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>Pool size — %s</b>\n\nCurrent: <b>%s</b>\n♻️ Default uses the global target/min rule.\nTap to change:", levelLabel(lv), poolOverrideLabel(cur, has)),
				configPoolValueKeyboard("pl", lv, cur, has),
			)
			return
		}
		// "goto:klv:<kind>:<level>" — value picker for one exact (kind, level) pool.
		if after, ok := strings.CutPrefix(key, "klv:"); ok {
			kind, level, found := strings.Cut(after, ":")
			lv, valid := normalizeLevel(level)
			if !found || !config.IsConfigurableKind(kind) || kind == config.KindTip || !valid {
				return
			}
			cur, has := config.PoolKindLevelOverride(kind, lv)
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>Pool size — %s %s</b>\n\nCurrent: <b>%s</b>\n♻️ Default falls back to per-kind, then per-level, then the global rule.\nTap to change:", levelLabel(lv), kind, poolOverrideLabel(cur, has)),
				configPoolKLValueKeyboard(kind, lv, cur, has),
			)
			return
		}
		// "goto:klk:<kind>" — per-level picker for a chosen kind.
		if kind, ok := strings.CutPrefix(key, "klk:"); ok {
			if !config.IsConfigurableKind(kind) || kind == config.KindTip {
				return
			}
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>%s pools by level</b>\n\nSet the target stocked for this kind at a specific level (e.g. upper-intermediate %s). Most specific override — wins over per-kind, per-level and the global rule.\nTap a level:", kind, kind),
				configPoolKLLevelsKeyboard(kind),
			)
			return
		}
		switch key {
		case "pool_kl":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				"📦 <b>Per kind + level pools</b>\n\nSet the pool size for one content kind at one difficulty level (e.g. upper-intermediate words).\nPick a kind:",
				configPoolKLKindsKeyboard(),
			)
		case "pool_kinds":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				"📦 <b>Per-kind pool sizes</b>\n\nOverride the target stocked per content kind. Per-kind wins over per-level and the global rule.\nTap a kind:",
				configPoolKindsKeyboard(),
			)
		case "pool_levels":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				"📦 <b>Per-level pool sizes</b>\n\nOverride the target stocked per difficulty level. Applies to every kind unless a per-kind override is set.\nTap a level:",
				configPoolLevelsKeyboard(),
			)
		case "pool_target":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>Pool Target</b>\n\nCurrent: <b>%d</b>\nTap to change:", config.PoolTarget),
				configPoolTargetKeyboard(),
			)
		case "pool_min":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>Pool Min</b>\n\nCurrent: <b>%d</b>\nTap to change:", config.PoolMin),
				configPoolMinKeyboard(),
			)
		case "quiet_start":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("🌙 <b>Quiet Hours Start</b>\n\nCurrent: <b>%s</b>\nTap to change:", config.QuietStart),
				configQuietStartKeyboard(),
			)
		case "quiet_end":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("🌙 <b>Quiet Hours End</b>\n\nCurrent: <b>%s</b>\nTap to change:", config.QuietEnd),
				configQuietEndKeyboard(),
			)
		case "gen_spacing":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("⏱ <b>AI Generation Spacing</b>\n\nCurrent: <b>%s</b>\nTap to change:", config.GenSpacing),
				configGenSpacingKeyboard(),
			)
		case "review_batch":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("🧠 <b>Review Batch Max</b>\n\nCurrent: <b>%d</b>\nTap to change:", config.ReviewBatchMax),
				configReviewBatchKeyboard(),
			)
		}
		return
	}

	// cfg:pool_target:<n> — set pool target.
	if strings.HasPrefix(rest, "pool_target:") {
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "pool_target:"))
		if err != nil || n <= 0 {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		old := config.PoolTarget
		config.PoolTarget = n
		_ = store.SetBotConfig("pool_target", strconv.Itoa(n))
		log.Printf("⚙️  [ADMIN] /config pool_target %d -> %d by ChatID %d.", old, n, chatID)
		refresh(fmt.Sprintf("Pool target: %d", n))
		return
	}

	// cfg:pool_min:<n> — set pool min.
	if strings.HasPrefix(rest, "pool_min:") {
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "pool_min:"))
		if err != nil || n <= 0 {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		old := config.PoolMin
		config.PoolMin = n
		_ = store.SetBotConfig("pool_min", strconv.Itoa(n))
		log.Printf("⚙️  [ADMIN] /config pool_min %d -> %d by ChatID %d.", old, n, chatID)
		refresh(fmt.Sprintf("Pool min: %d", n))
		return
	}

	// cfg:quiet_start:<v> — set quiet hours start.
	if strings.HasPrefix(rest, "quiet_start:") {
		v := strings.TrimPrefix(rest, "quiet_start:")
		old := config.QuietStart
		config.QuietStart = v
		_ = store.SetBotConfig("quiet_start", v)
		log.Printf("⚙️  [ADMIN] /config quiet_start %s -> %s by ChatID %d.", old, v, chatID)
		refresh("Quiet start: " + v)
		return
	}

	// cfg:quiet_end:<v> — set quiet hours end.
	if strings.HasPrefix(rest, "quiet_end:") {
		v := strings.TrimPrefix(rest, "quiet_end:")
		old := config.QuietEnd
		config.QuietEnd = v
		_ = store.SetBotConfig("quiet_end", v)
		log.Printf("⚙️  [ADMIN] /config quiet_end %s -> %s by ChatID %d.", old, v, chatID)
		refresh("Quiet end: " + v)
		return
	}

	// cfg:gen_spacing:<v> — set generation spacing.
	if strings.HasPrefix(rest, "gen_spacing:") {
		v := strings.TrimPrefix(rest, "gen_spacing:")
		d, err := time.ParseDuration(v)
		if err != nil {
			_ = notifier.AnswerCallback(cb.ID, "Invalid duration")
			return
		}
		old := config.GenSpacing
		config.GenSpacing = d
		_ = store.SetBotConfig("gen_spacing", v)
		log.Printf("⚙️  [ADMIN] /config gen_spacing %s -> %s by ChatID %d.", old, d, chatID)
		refresh("Gen spacing: " + d.String())
		return
	}

	// cfg:review_batch:<n> — set review batch max.
	if strings.HasPrefix(rest, "review_batch:") {
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "review_batch:"))
		if err != nil || n <= 0 {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		old := config.ReviewBatchMax
		config.ReviewBatchMax = n
		_ = store.SetBotConfig("review_batch_max", strconv.Itoa(n))
		log.Printf("⚙️  [ADMIN] /config review_batch_max %d -> %d by ChatID %d.", old, n, chatID)
		refresh(fmt.Sprintf("Review batch: %d", n))
		return
	}

	// cfg:kl:<kind>:<level>:<n> — set (n>0) or clear (n==0) a per-(kind,level) override.
	if after, ok := strings.CutPrefix(rest, "kl:"); ok {
		parts := strings.SplitN(after, ":", 3)
		if len(parts) != 3 {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		kind, level, nStr := parts[0], parts[1], parts[2]
		n, err := strconv.Atoi(nStr)
		lv, valid := normalizeLevel(level)
		if err != nil || n < 0 || !config.IsConfigurableKind(kind) || kind == config.KindTip || !valid {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		config.SetPoolKindLevelOverride(kind, lv, n)
		key := "pool_kl_" + kind + "_" + lv
		var toast string
		if n == 0 {
			_ = store.DeleteBotConfig(key)
			toast = fmt.Sprintf("%s %s: default", levelLabel(lv), kind)
		} else {
			_ = store.SetBotConfig(key, strconv.Itoa(n))
			toast = fmt.Sprintf("%s %s: %d", levelLabel(lv), kind, n)
		}
		log.Printf("⚙️  [ADMIN] /config %s -> %d by ChatID %d.", key, n, chatID)
		_ = notifier.AnswerCallback(cb.ID, toast)
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("📦 <b>%s pools by level</b>\n\nSet the target stocked for this kind at a specific level (e.g. upper-intermediate %s). Most specific override — wins over per-kind, per-level and the global rule.\nTap a level:", kind, kind),
				configPoolKLLevelsKeyboard(kind),
			)
		}
		return
	}

	// cfg:pk:<kind>:<n> — set (n>0) or clear (n==0) a per-kind pool override.
	if after, ok := strings.CutPrefix(rest, "pk:"); ok {
		kind, nStr, found := strings.Cut(after, ":")
		n, err := strconv.Atoi(nStr)
		if !found || err != nil || n < 0 || !config.IsConfigurableKind(kind) {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		config.SetPoolKindOverride(kind, n)
		key := "pool_kind_" + kind
		var toast string
		if n == 0 {
			_ = store.DeleteBotConfig(key)
			toast = fmt.Sprintf("%s: default", kind)
		} else {
			_ = store.SetBotConfig(key, strconv.Itoa(n))
			toast = fmt.Sprintf("%s: %d", kind, n)
		}
		log.Printf("⚙️  [ADMIN] /config %s -> %d by ChatID %d.", key, n, chatID)
		_ = notifier.AnswerCallback(cb.ID, toast)
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				"📦 <b>Per-kind pool sizes</b>\n\nOverride the target stocked per content kind. Per-kind wins over per-level and the global rule.\nTap a kind:",
				configPoolKindsKeyboard(),
			)
		}
		return
	}

	// cfg:pl:<level>:<n> — set (n>0) or clear (n==0) a per-level pool override.
	if after, ok := strings.CutPrefix(rest, "pl:"); ok {
		level, nStr, found := strings.Cut(after, ":")
		n, err := strconv.Atoi(nStr)
		lv, valid := normalizeLevel(level)
		if !found || err != nil || n < 0 || !valid {
			_ = notifier.AnswerCallback(cb.ID, "Invalid value")
			return
		}
		config.SetPoolLevelOverride(lv, n)
		key := "pool_level_" + lv
		var toast string
		if n == 0 {
			_ = store.DeleteBotConfig(key)
			toast = fmt.Sprintf("%s: default", levelLabel(lv))
		} else {
			_ = store.SetBotConfig(key, strconv.Itoa(n))
			toast = fmt.Sprintf("%s: %d", levelLabel(lv), n)
		}
		log.Printf("⚙️  [ADMIN] /config %s -> %d by ChatID %d.", key, n, chatID)
		_ = notifier.AnswerCallback(cb.ID, toast)
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				"📦 <b>Per-level pool sizes</b>\n\nOverride the target stocked per difficulty level. Applies to every kind unless a per-kind override is set.\nTap a level:",
				configPoolLevelsKeyboard(),
			)
		}
		return
	}

	_ = notifier.AnswerCallback(cb.ID, "")
}

// levelKeyboard builds a two-row inline keyboard of the four levels, marking the
// current one with a check.
func levelKeyboard(current string) [][]telegram.InlineButton {
	var row1, row2 []telegram.InlineButton
	for i, l := range config.AllLevels {
		label := levelLabel(l)
		if l == current {
			label = "✅ " + label
		}
		btn := telegram.InlineButton{Text: label, CallbackData: "level:" + l}
		if i < 2 {
			row1 = append(row1, btn)
		} else {
			row2 = append(row2, btn)
		}
	}
	return [][]telegram.InlineButton{row1, row2}
}

// handleInterval handles the /interval command. With a valid numeric argument it
// sets the send interval directly; otherwise it shows the current interval with
// an inline keyboard of the allowed options.
func handleInterval(store *Store, notifier telegram.Notifier, chatID int64, args []string) {
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
func handleTTS(store *Store, notifier telegram.Notifier, chatID int64, args []string) {
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
			if !config.TTSEnabled {
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
	if !config.TTSEnabled {
		msg += "\n\nℹ️ It is currently disabled globally by the bot administrator."
	}
	_ = notifier.Send(chatID, msg)
}

// handleTip handles /tip commands:
//
//	/tip       => on-demand tip
//	/tip on    => enable scheduled daily tips
//	/tip off   => disable scheduled daily tips
func handleTip(ctx context.Context, chain *ai.ProviderChain, store *Store, notifier telegram.Notifier, chatID int64, args []string) {
	if len(args) > 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "on":
			if err := store.SetTipsEnabled(chatID, true); err != nil {
				log.Printf("❌ [TIP] Could not enable tips for ChatID %d: %v", chatID, err)
				_ = notifier.Send(chatID, "❌ Sorry, I couldn't enable daily tips right now. Please try again.")
				return
			}
			_ = notifier.Send(chatID, "✅ Daily grammar tips are <b>ON</b>. You'll receive one each day.")
			return
		case "off":
			if err := store.SetTipsEnabled(chatID, false); err != nil {
				log.Printf("❌ [TIP] Could not disable tips for ChatID %d: %v", chatID, err)
				_ = notifier.Send(chatID, "❌ Sorry, I couldn't disable daily tips right now. Please try again.")
				return
			}
			_ = notifier.Send(chatID, "🛑 Daily grammar tips are <b>OFF</b>. Use <code>/tip on</code> anytime to re-enable.")
			return
		default:
			_ = notifier.Send(chatID, "Usage: <code>/tip</code>, <code>/tip on</code>, or <code>/tip off</code>")
			return
		}
	}

	log.Printf("💡 [TIP] /tip requested by ChatID %d.", chatID)
	_ = notifier.Send(chatID, "🔄 <b>Fetching your grammar tip...</b>")
	tip, _, err := serveContent(ctx, chain, store, notifier, chatID, config.KindTip, config.DefaultLevel, true)
	if err != nil {
		log.Printf("❌ [TIP] On-demand tip failed for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't fetch a tip right now. Please try again.")
		return
	}
	_ = notifier.Send(chatID, tip)
}

// ---------------------------------------------------------------------------
// /setup — daily time-budget quick-setup (v1.17.0)
// ---------------------------------------------------------------------------

// timeBudgetPreset defines the settings applied for a given daily-time budget.
type timeBudgetPreset struct {
	label              string // human-readable button label
	minutesPerDay      int    // key used in callback data
	interval           int    // broadcast interval (minutes)
	quizIntervalHours  int    // per-user quiz interval (hours)
	quizEnabled        bool
	idiomEnabled       bool
	collocationEnabled bool
	storyEnabled       bool
	tipsEnabled        bool
	reviewEnabled      bool // SRS word reviews
	dailyReviewEnabled bool
	digestEnabled      bool
}

// timeBudgetPresets is the ordered list of selectable daily-time presets.
var timeBudgetPresets = []timeBudgetPreset{
	{
		label: "⚡ 5 min/day", minutesPerDay: 5,
		interval: 240, quizIntervalHours: 24,
		quizEnabled: true, idiomEnabled: false, collocationEnabled: false, storyEnabled: false, tipsEnabled: false,
		reviewEnabled: true, dailyReviewEnabled: false, digestEnabled: false,
	},
	{
		label: "🎯 15 min/day", minutesPerDay: 15,
		interval: 120, quizIntervalHours: 24,
		quizEnabled: true, idiomEnabled: false, collocationEnabled: false, storyEnabled: false, tipsEnabled: true,
		reviewEnabled: true, dailyReviewEnabled: false, digestEnabled: false,
	},
	{
		label: "📚 30 min/day", minutesPerDay: 30,
		interval: 60, quizIntervalHours: 12,
		quizEnabled: true, idiomEnabled: true, collocationEnabled: true, storyEnabled: true, tipsEnabled: true,
		reviewEnabled: true, dailyReviewEnabled: true, digestEnabled: true,
	},
	{
		label: "🔥 1 hour/day", minutesPerDay: 60,
		interval: 30, quizIntervalHours: 6,
		quizEnabled: true, idiomEnabled: true, collocationEnabled: true, storyEnabled: true, tipsEnabled: true,
		reviewEnabled: true, dailyReviewEnabled: true, digestEnabled: true,
	},
	{
		label: "💪 2 hours/day", minutesPerDay: 120,
		interval: 30, quizIntervalHours: 3,
		quizEnabled: true, idiomEnabled: true, collocationEnabled: true, storyEnabled: true, tipsEnabled: true,
		reviewEnabled: true, dailyReviewEnabled: true, digestEnabled: true,
	},
}

// presetByMinutes looks up a preset by its minutesPerDay key.
func presetByMinutes(minutes int) (timeBudgetPreset, bool) {
	for _, p := range timeBudgetPresets {
		if p.minutesPerDay == minutes {
			return p, true
		}
	}
	return timeBudgetPreset{}, false
}

// applyTimeBudgetPreset writes all settings defined by the preset to user_prefs.
func applyTimeBudgetPreset(store *Store, chatID int64, p timeBudgetPreset) error {
	if err := store.SetInterval(chatID, p.interval); err != nil {
		return err
	}
	if err := store.SetQuizIntervalHours(chatID, p.quizIntervalHours); err != nil {
		return err
	}
	if err := store.SetQuizEnabled(chatID, p.quizEnabled); err != nil {
		return err
	}
	if err := store.SetIdiomEnabled(chatID, p.idiomEnabled); err != nil {
		return err
	}
	if err := store.SetCollocationEnabled(chatID, p.collocationEnabled); err != nil {
		return err
	}
	if err := store.SetStoryEnabled(chatID, p.storyEnabled); err != nil {
		return err
	}
	if err := store.SetTipsEnabled(chatID, p.tipsEnabled); err != nil {
		return err
	}
	if err := store.SetReviewEnabled(chatID, p.reviewEnabled); err != nil {
		return err
	}
	if err := store.SetDailyReviewEnabled(chatID, p.dailyReviewEnabled); err != nil {
		return err
	}
	if err := store.SetDigestEnabled(chatID, p.digestEnabled); err != nil {
		return err
	}
	return nil
}

// setupKeyboard builds the time-budget selection keyboard.
func setupKeyboard() [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, p := range timeBudgetPresets {
		row = append(row, telegram.InlineButton{
			Text:         p.label,
			CallbackData: fmt.Sprintf("setup:%d", p.minutesPerDay),
		})
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

// handleSetup sends the daily-time-budget quick-setup keyboard.
func handleSetup(store *Store, notifier telegram.Notifier, chatID int64) {
	text := "⏱ <b>Quick Setup — How much time can you spend per day?</b>\n\n" +
		"I'll tune your settings to fit your schedule:\n\n" +
		"⚡ <b>5 min</b> — light touch: drill or word every 4 hours + SRS reviews\n" +
		"🎯 <b>15 min</b> — steady: every 2 hours + daily tip + SRS reviews\n" +
		"📚 <b>30 min</b> — solid: every hour + all daily features\n" +
		"🔥 <b>1 hour</b> — active: every 30 min + all features + quizzes every 6h\n" +
		"💪 <b>2 hours</b> — intensive: every 30 min + maximum quiz frequency\n\n" +
		"You can always fine-tune later with /settings."
	if err := notifier.SendKeyboard(chatID, text, setupKeyboard()); err != nil {
		log.Printf("❌ [SETUP] Could not send setup keyboard to ChatID %d: %v", chatID, err)
	}
}

// handleSetupCallback applies the chosen time-budget preset and shows the result.
func handleSetupCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
	minutes, err := strconv.Atoi(strings.TrimPrefix(cb.Data, "setup:"))
	if err != nil {
		_ = notifier.AnswerCallback(cb.ID, "Unknown option")
		return
	}
	p, ok := presetByMinutes(minutes)
	if !ok {
		_ = notifier.AnswerCallback(cb.ID, "Unknown option")
		return
	}
	if err := applyTimeBudgetPreset(store, chatID, p); err != nil {
		log.Printf("❌ [SETUP] Could not apply preset for ChatID %d: %v", chatID, err)
		_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
		return
	}
	log.Printf("⚙️  [SETUP] ChatID %d applied preset %q (%d min/day).", chatID, p.label, p.minutesPerDay)
	_ = notifier.AnswerCallback(cb.ID, "Applied: "+p.label)

	// Replace the setup message with a settings-hub view so the user can see
	// exactly what changed and tweak anything they like.
	prefs, _ := store.GetPrefs(chatID)
	confirmText := fmt.Sprintf(
		"✅ <b>Settings applied for %s</b>\n\n"+
			"Broadcasts: every <b>%s</b>\n"+
			"Quizzes: every <b>%s</b>\n\n"+
			"Use /settings anytime to adjust individual options.",
		p.label,
		intervalLabel(p.interval),
		quizIntervalLabel(p.quizIntervalHours),
	)
	if cb.Message != nil {
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, confirmText, settingsKeyboard(prefs))
	} else {
		_ = notifier.Send(chatID, confirmText)
	}
}

// ---------------------------------------------------------------------------
// /settings — unified settings hub (v1.17.0)
// ---------------------------------------------------------------------------

// handleSettings sends (or refreshes) the settings hub for the user.
func handleSettings(store *Store, notifier telegram.Notifier, chatID int64) {
	prefs, err := store.GetPrefs(chatID)
	if err != nil {
		log.Printf("❌ [SETTINGS] Could not load prefs for ChatID %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't load your settings right now. Please try again.")
		return
	}
	text := settingsText(prefs)
	kb := settingsKeyboard(prefs)
	if err := notifier.SendKeyboard(chatID, text, kb); err != nil {
		log.Printf("❌ [SETTINGS] Could not send settings to ChatID %d: %v", chatID, err)
	}
}

// settingsText formats the settings hub message body.
func settingsText(prefs UserPrefs) string {
	onOff := func(b bool) string {
		if b {
			return "ON"
		}
		return "OFF"
	}
	status := "Active"
	if prefs.Paused {
		status = "Paused"
	}
	return fmt.Sprintf(
		"⚙️ <b>Your Settings</b>\n\n"+
			"📚 <b>Level:</b> %s\n"+
			"⏱ <b>Broadcast interval:</b> every %s  •  <b>Status:</b> %s\n\n"+
			"🔊 <b>Voice pronunciation (TTS):</b> %s\n"+
			"    <i>Sends a short audio clip after each word/idiom so you hear the right accent</i>\n\n"+
			"🔁 <b>SRS word reviews:</b> %s\n"+
			"    <i>Spaced-repetition: learned words come back at growing intervals to lock them into memory</i>\n\n"+
			"🧩 <b>Quizzes:</b> %s  (every %s)\n"+
			"    <i>Multiple-choice questions on words you've already learned</i>\n\n"+
			"🗣️ <b>Idiom of the day:</b> %s\n"+
			"    <i>One common English expression with meaning and examples, sent once a day</i>\n\n"+
			"🔗 <b>Collocation of the day:</b> %s\n"+
			"    <i>One natural word partnership (like \"make a decision\") with examples, sent once a day</i>\n\n"+
			"📖 <b>Daily mini story:</b> %s\n"+
			"    <i>A short reading-practice story at your level with key vocabulary, sent once a day</i>\n\n"+
			"💡 <b>Daily grammar tip:</b> %s\n"+
			"    <i>One focused grammar rule with correct/incorrect examples, sent once a day</i>\n\n"+
			"🌙 <b>Midnight recap:</b> %s\n"+
			"    <i>A quick summary of today's words sent at midnight — great for a last look before sleep</i>\n\n"+
			"📊 <b>Weekly digest:</b> %s\n"+
			"    <i>Every Sunday evening: a recap of the week's words, quiz accuracy and streak</i>\n\n"+
			"Tap a button below to change any setting.",
		levelLabel(prefs.Level),
		intervalLabel(prefs.Interval), status,
		onOff(prefs.TTSEnabled),
		onOff(prefs.ReviewEnabled),
		onOff(prefs.QuizEnabled), quizIntervalLabel(prefs.QuizIntervalHours),
		onOff(prefs.IdiomEnabled),
		onOff(prefs.CollocationEnabled),
		onOff(prefs.StoryEnabled),
		onOff(prefs.TipsEnabled),
		onOff(prefs.DailyReviewEnabled),
		onOff(prefs.DigestEnabled),
	)
}

// settingsKeyboard builds the settings hub inline keyboard.
func settingsKeyboard(prefs UserPrefs) [][]telegram.InlineButton {
	tog := func(label string, key string, on bool) telegram.InlineButton {
		icon := "✅"
		if !on {
			icon = "❌"
		}
		return telegram.InlineButton{Text: fmt.Sprintf("%s %s: %s", icon, label, map[bool]string{true: "ON", false: "OFF"}[on]), CallbackData: "settings:toggle:" + key}
	}
	pauseBtn := telegram.InlineButton{Text: "⏸ Pause broadcasts", CallbackData: "settings:toggle:pause"}
	if prefs.Paused {
		pauseBtn = telegram.InlineButton{Text: "▶️ Resume broadcasts", CallbackData: "settings:toggle:pause"}
	}
	return [][]telegram.InlineButton{
		{
			{Text: "📚 Level: " + levelLabel(prefs.Level), CallbackData: "settings:goto:level"},
			{Text: "⏱ Every " + intervalLabel(prefs.Interval), CallbackData: "settings:goto:interval"},
		},
		{
			tog("🔊 Voice pronunciation", "tts", prefs.TTSEnabled),
			tog("💡 Daily grammar tip", "tips", prefs.TipsEnabled),
		},
		{
			tog("🗣️ Idiom of the day", "idiom", prefs.IdiomEnabled),
			tog("🌙 Midnight recap", "daily_review", prefs.DailyReviewEnabled),
		},
		{
			tog("🔗 Collocation of the day", "collocation", prefs.CollocationEnabled),
			tog("📖 Daily mini story", "story", prefs.StoryEnabled),
		},
		{
			tog("🧩 Quizzes", "quiz", prefs.QuizEnabled),
			tog("🔁 Word reviews (SRS)", "srs", prefs.ReviewEnabled),
		},
		{
			tog("📊 Weekly digest", "digest", prefs.DigestEnabled),
			pauseBtn,
		},
		{
			{Text: "⏱ Quiz every " + quizIntervalLabel(prefs.QuizIntervalHours), CallbackData: "settings:goto:quiz_interval"},
		},
	}
}

// settingsLevelKeyboard builds the level selection keyboard for use within the
// settings hub, using settings-namespaced callback data and a Back button.
func settingsLevelKeyboard(current string) [][]telegram.InlineButton {
	var row1, row2 []telegram.InlineButton
	for i, l := range config.AllLevels {
		label := levelLabel(l)
		if l == current {
			label = "✅ " + label
		}
		btn := telegram.InlineButton{Text: label, CallbackData: "settings:level:" + l}
		if i < 2 {
			row1 = append(row1, btn)
		} else {
			row2 = append(row2, btn)
		}
	}
	return [][]telegram.InlineButton{
		row1, row2,
		{{Text: "⬅️ Back to Settings", CallbackData: "settings:show"}},
	}
}

// settingsIntervalKeyboard builds the interval keyboard for use within settings.
func settingsIntervalKeyboard(current int) [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, iv := range allIntervals {
		label := intervalLabel(iv)
		if iv == current {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("settings:interval:%d", iv)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "⬅️ Back to Settings", CallbackData: "settings:show"}})
	return rows
}

// settingsQuizIntervalKeyboard builds the quiz-interval keyboard within settings.
func settingsQuizIntervalKeyboard(current int) [][]telegram.InlineButton {
	var row []telegram.InlineButton
	for _, h := range allQuizIntervalHours {
		label := quizIntervalLabel(h)
		if h == current {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("settings:quiz_interval:%d", h)})
	}
	return [][]telegram.InlineButton{
		row,
		{{Text: "⬅️ Back to Settings", CallbackData: "settings:show"}},
	}
}

// intervalKeyboard builds an inline keyboard (rows of two) of the allowed send
// intervals, marking the current one with a check.
func intervalKeyboard(current int) [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	var row []telegram.InlineButton
	for _, iv := range allIntervals {
		label := intervalLabel(iv)
		if iv == current {
			label = "✅ " + label
		}
		row = append(row, telegram.InlineButton{Text: label, CallbackData: fmt.Sprintf("interval:%d", iv)})
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
func handleCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery) {
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

	if strings.HasPrefix(cb.Data, "setup:") {
		handleSetupCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "settings:") {
		handleSettingsCallback(store, notifier, cb, chatID)
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

	if strings.HasPrefix(cb.Data, "mywords:") || strings.HasPrefix(cb.Data, "mybm:") {
		handleMyWordsCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "bookmark:") {
		handleBookmarkCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "admin:") {
		if !isMaintainer(chatID) {
			_ = notifier.AnswerCallback(cb.ID, "🔒 Admin only")
			return
		}
		handleAdminCallback(store, notifier, cb, chatID)
		return
	}

	if strings.HasPrefix(cb.Data, "cfg:") {
		if !isMaintainer(chatID) {
			_ = notifier.AnswerCallback(cb.ID, "🔒 Admin only")
			return
		}
		handleConfigCallback(store, notifier, cb, chatID)
		return
	}

	log.Printf("ℹ️  [CALLBACK_UNHANDLED] Unknown callback data %q from ChatID %d.", cb.Data, chatID)
	_ = notifier.AnswerCallback(cb.ID, "")
}

// handleSettingsCallback processes all "settings:*" inline button taps from the
// settings hub. It handles toggle, goto (sub-keyboard), and set operations,
// then edits the message in place so the hub stays up-to-date.
func handleSettingsCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "settings:")

	// Helper: reload prefs and refresh the settings message in-place.
	refresh := func(toast string) {
		_ = notifier.AnswerCallback(cb.ID, toast)
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("❌ [SETTINGS] Could not reload prefs for chat %d: %v", chatID, err)
			return
		}
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID, settingsText(prefs), settingsKeyboard(prefs))
		}
	}

	// "settings:show" — just refresh (used by Back buttons in sub-keyboards).
	if rest == "show" {
		refresh("")
		return
	}

	// "settings:goto:level" / "settings:goto:interval" / "settings:goto:quiz_interval"
	if strings.HasPrefix(rest, "goto:") {
		_ = notifier.AnswerCallback(cb.ID, "")
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			_ = notifier.AnswerCallback(cb.ID, "Could not load settings")
			return
		}
		if cb.Message == nil {
			return
		}
		switch strings.TrimPrefix(rest, "goto:") {
		case "level":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("🎚️ <b>Difficulty level</b>\n\nCurrent: <b>%s</b>\nTap to change:", levelLabel(prefs.Level)),
				settingsLevelKeyboard(prefs.Level),
			)
		case "interval":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("⏱ <b>Broadcast interval</b>\n\nCurrent: every <b>%s</b>\nTap to change:", intervalLabel(prefs.Interval)),
				settingsIntervalKeyboard(prefs.Interval),
			)
		case "quiz_interval":
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				fmt.Sprintf("⏱ <b>Quiz interval</b>\n\nCurrent: every <b>%s</b>\nTap to change:", quizIntervalLabel(prefs.QuizIntervalHours)),
				settingsQuizIntervalKeyboard(prefs.QuizIntervalHours),
			)
		}
		return
	}

	// "settings:level:<l>" — set level from within settings sub-keyboard.
	if strings.HasPrefix(rest, "level:") {
		level, ok := normalizeLevel(strings.TrimPrefix(rest, "level:"))
		if !ok {
			_ = notifier.AnswerCallback(cb.ID, "Unknown level")
			return
		}
		if err := store.SetLevel(chatID, level); err != nil {
			log.Printf("❌ [SETTINGS] Could not set level for chat %d: %v", chatID, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("🎚️  [SETTINGS] Chat %d set level to %s.", chatID, level)
		refresh("Level set to " + levelLabel(level))
		return
	}

	// "settings:interval:<n>" — set broadcast interval from within settings.
	if strings.HasPrefix(rest, "interval:") {
		minutes, perr := strconv.Atoi(strings.TrimPrefix(rest, "interval:"))
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
			log.Printf("❌ [SETTINGS] Could not set interval for chat %d: %v", chatID, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("⏱️  [SETTINGS] Chat %d set interval to %d min.", chatID, iv)
		refresh("Interval set to " + intervalLabel(iv))
		return
	}

	// "settings:quiz_interval:<n>" — set quiz interval from within settings.
	if strings.HasPrefix(rest, "quiz_interval:") {
		hours, perr := strconv.Atoi(strings.TrimPrefix(rest, "quiz_interval:"))
		if perr != nil {
			_ = notifier.AnswerCallback(cb.ID, "Unknown interval")
			return
		}
		qh, ok := normalizeQuizIntervalHours(hours)
		if !ok {
			_ = notifier.AnswerCallback(cb.ID, "Unknown interval")
			return
		}
		if err := store.SetQuizIntervalHours(chatID, qh); err != nil {
			log.Printf("❌ [SETTINGS] Could not set quiz interval for chat %d: %v", chatID, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("⏱️  [SETTINGS] Chat %d set quiz interval to %dh.", chatID, qh)
		refresh("Quiz interval set to " + quizIntervalLabel(qh))
		return
	}

	// "settings:toggle:<key>" — toggle a boolean setting.
	if strings.HasPrefix(rest, "toggle:") {
		key := strings.TrimPrefix(rest, "toggle:")
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			_ = notifier.AnswerCallback(cb.ID, "Could not load settings")
			return
		}
		var saveErr error
		var toast string
		switch key {
		case "tts":
			saveErr = store.SetTTSEnabled(chatID, !prefs.TTSEnabled)
			toast = map[bool]string{true: "TTS ON", false: "TTS OFF"}[!prefs.TTSEnabled]
		case "tips":
			saveErr = store.SetTipsEnabled(chatID, !prefs.TipsEnabled)
			toast = map[bool]string{true: "Tips ON", false: "Tips OFF"}[!prefs.TipsEnabled]
		case "quiz":
			saveErr = store.SetQuizEnabled(chatID, !prefs.QuizEnabled)
			toast = map[bool]string{true: "Quizzes ON", false: "Quizzes OFF"}[!prefs.QuizEnabled]
		case "idiom":
			saveErr = store.SetIdiomEnabled(chatID, !prefs.IdiomEnabled)
			toast = map[bool]string{true: "Idiom ON", false: "Idiom OFF"}[!prefs.IdiomEnabled]
		case "collocation":
			saveErr = store.SetCollocationEnabled(chatID, !prefs.CollocationEnabled)
			toast = map[bool]string{true: "Collocation ON", false: "Collocation OFF"}[!prefs.CollocationEnabled]
		case "story":
			saveErr = store.SetStoryEnabled(chatID, !prefs.StoryEnabled)
			toast = map[bool]string{true: "Mini story ON", false: "Mini story OFF"}[!prefs.StoryEnabled]
		case "srs":
			saveErr = store.SetReviewEnabled(chatID, !prefs.ReviewEnabled)
			toast = map[bool]string{true: "SRS ON", false: "SRS OFF"}[!prefs.ReviewEnabled]
		case "digest":
			saveErr = store.SetDigestEnabled(chatID, !prefs.DigestEnabled)
			toast = map[bool]string{true: "Digest ON", false: "Digest OFF"}[!prefs.DigestEnabled]
		case "daily_review":
			saveErr = store.SetDailyReviewEnabled(chatID, !prefs.DailyReviewEnabled)
			toast = map[bool]string{true: "Daily Review ON", false: "Daily Review OFF"}[!prefs.DailyReviewEnabled]
		case "pause":
			newPaused := !prefs.Paused
			saveErr = store.SetPaused(chatID, newPaused)
			if newPaused {
				toast = "Broadcasts paused"
			} else {
				toast = "Broadcasts resumed"
			}
		default:
			_ = notifier.AnswerCallback(cb.ID, "")
			return
		}
		if saveErr != nil {
			log.Printf("❌ [SETTINGS] Could not toggle %q for chat %d: %v", key, chatID, saveErr)
			_ = notifier.AnswerCallback(cb.ID, "Could not save, try again")
			return
		}
		log.Printf("⚙️  [SETTINGS] Chat %d toggled %q.", chatID, key)
		refresh(toast)
		return
	}

	_ = notifier.AnswerCallback(cb.ID, "")
}

// handleReviewCallback applies a spaced-repetition self-grade ("Knew it" /
// "Forgot") from a memory-check card tap (Change D). Callback data is of the
// form "srs:known:<word>" or "srs:forgot:<word>".
func handleReviewCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "srs:")
	action, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	now := time.Now()
	// The memory-check card hides the meaning until the user answers. Fetch the
	// stored meaning and full vocabulary card so we can reveal it now: a brief
	// reminder on "Knew it", the full detailed card on "Forgot".
	meaning, card, _, lookupErr := store.WordCard(word)
	if lookupErr != nil {
		log.Printf("⚠️  [SRS] Could not load card for %q (chat %d): %v", word, chatID, lookupErr)
	}

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
		// Reveal a short reminder so they can confirm they had it right.
		if meaning != "" {
			confirm = fmt.Sprintf("✅ <b>%s</b> — %s\n\nNice! I'll show it again later, spaced further out.", word, meaning)
		} else {
			confirm = fmt.Sprintf("✅ <b>%s</b> — nice! I'll show it again later, spaced further out.", word)
		}
	case "forgot":
		ok, err = store.ApplyReviewForgot(chatID, word, now)
		toast = "No worries — you'll see it again soon"
		// Reveal the full detailed card so they can re-learn it properly.
		switch {
		case card != "":
			confirm = fmt.Sprintf("❌ <b>%s</b> — no problem. Here's the full card to refresh it:\n\n%s\n\nI'll bring it back soon so it sticks.", word, card)
		case meaning != "":
			confirm = fmt.Sprintf("❌ <b>%s</b> — %s\n\nNo problem. I'll bring it back soon so it sticks.", word, meaning)
		default:
			confirm = fmt.Sprintf("❌ <b>%s</b> — no problem. I'll bring it back soon so it sticks.", word)
		}
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
	// Answering a review counts as a learning day for the streak (it leaves no
	// sent_* footprint of its own) and feeds the rolling level-suggestion window.
	if err := store.RecordActivity(chatID, now); err != nil {
		log.Printf("⚠️  [SRS] Could not record activity for chat %d: %v", chatID, err)
	}
	if err := store.RecordReviewOutcome(chatID, action == "known"); err != nil {
		log.Printf("⚠️  [SRS] Could not record review outcome for chat %d: %v", chatID, err)
	}
	_ = notifier.AnswerCallback(cb.ID, toast)
	if cb.Message != nil {
		_ = notifier.EditMessage(chatID, cb.Message.MessageID, confirm, [][]telegram.InlineButton{})
	}
}

// sendDrill delivers a drill as page 1 of a paged message with prev/next
// navigation buttons (Change N). When the drill is a single page (e.g. legacy
// content that can't be parsed into forms) it falls back to a plain send.
func sendDrill(notifier telegram.Notifier, chatID int64, fullText string) error {
	verb := content.ParseVerb(fullText)
	if verb == "" {
		// Cannot paginate without a verb — the callback handler needs it to
		// reload the drill from the pool. Send the full text as a single message.
		log.Printf("⚠️  [DRILL] ParseVerb returned empty for chat %d; sending un-paged.", chatID)
		return notifier.Send(chatID, fullText)
	}
	text, total := content.RenderDrillPage(fullText, 1)
	kb := content.DrillNavKeyboard(verb, 1, total)
	if len(kb) == 0 {
		return notifier.Send(chatID, text)
	}
	return notifier.SendKeyboard(chatID, text, kb)
}

// handleDrillCallback turns the page of a paged drill in place. Callback data is
// "drill:<page>:<verb>"; the full drill is reloaded from the pool by verb and the
// requested page is re-rendered via editMessageText. "drill:noop" (the page
// indicator button) is acknowledged silently.
func handleDrillCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
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

	text, total := content.RenderDrillPage(fullText, page)
	_ = notifier.AnswerCallback(cb.ID, "")
	if cb.Message != nil {
		if err := notifier.EditMessage(chatID, cb.Message.MessageID, text, content.DrillNavKeyboard(term, page, total)); err != nil {
			log.Printf("⚠️  [DRILL] EditMessage failed for chat %d msg %d page %d term %q: %v", chatID, cb.Message.MessageID, page, term, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Streak milestone celebration
// ---------------------------------------------------------------------------

var streakMilestones = []int{3, 7, 14, 30, 60}

// checkStreakCelebration fires a one-time congratulatory message when the user
// reaches a new streak milestone. Best-effort; errors are silently dropped.
func checkStreakCelebration(store *Store, notifier telegram.Notifier, chatID int64, firstName string) {
	stats, err := store.UserStats(chatID)
	if err != nil || stats.CurrentStreak == 0 {
		return
	}
	streak := stats.CurrentStreak

	milestone := 0
	for _, m := range streakMilestones {
		if streak >= m {
			milestone = m
		}
	}
	if milestone == 0 {
		return
	}
	if store.GetStreakCelebrated(chatID) >= milestone {
		return
	}
	store.SetStreakCelebrated(chatID, milestone)

	name := firstName
	if name == "" {
		if prefs, err := store.GetPrefs(chatID); err == nil && prefs.FirstName != "" {
			name = prefs.FirstName
		}
	}
	if name == "" {
		name = "friend"
	}

	var msg string
	switch milestone {
	case 3:
		msg = fmt.Sprintf("🎉 <b>3-day streak!</b> Great start, %s! Consistency is the secret sauce.", name)
	case 7:
		msg = fmt.Sprintf("🔥 <b>7-day streak!</b> One full week, %s! You're building a real habit.", name)
	case 14:
		msg = fmt.Sprintf("⚡ <b>14-day streak!</b> Two weeks strong, %s! Your vocabulary is growing fast.", name)
	case 30:
		msg = fmt.Sprintf("🏆 <b>30-day streak!</b> A whole month, %s! You're officially dedicated.", name)
	case 60:
		msg = fmt.Sprintf("💎 <b>60-day streak!</b> Two months of daily practice, %s! You're unstoppable.", name)
	}
	if msg != "" {
		_ = notifier.Send(chatID, msg)
	}
}

// SQLiteBackup creates a point-in-time SQLite snapshot at destPath.
func (s *Store) SQLiteBackup(destPath string) error {
	if strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("backup destination path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	escaped := strings.ReplaceAll(destPath, "'", "''")
	_, err := s.db.Exec("VACUUM INTO '" + escaped + "'")
	return err
}

// sendMaintainerDBBackup snapshots the SQLite DB and sends it privately to the maintainer.
func sendMaintainerDBBackup(store *Store, notifier telegram.Notifier, trigger string) error {
	mID, ok := maintainerID()
	if !ok {
		return fmt.Errorf("invalid MAINTAINER_CHAT_ID")
	}

	now := time.Now().In(config.AppLocation)
	filename := fmt.Sprintf("english-bot-backup-%s.sqlite", now.Format("20060102-150405"))
	destPath := filepath.Join(os.TempDir(), filename)

	if err := store.SQLiteBackup(destPath); err != nil {
		return fmt.Errorf("sqlite backup snapshot: %w", err)
	}
	defer os.Remove(destPath)

	doc, err := os.ReadFile(destPath)
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}

	caption := fmt.Sprintf(
		"🗄️ <b>SQLite Backup</b>\n<b>Trigger:</b> %s\n<b>Time:</b> %s\n<b>Size:</b> %d bytes",
		trigger,
		now.Format("2006-01-02 15:04:05 MST"),
		len(doc),
	)
	if err := notifier.SendDocument(mID, doc, filename, caption); err != nil {
		return fmt.Errorf("send backup document: %w", err)
	}
	log.Printf("✅ [BACKUP] Sent SQLite backup to maintainer (trigger=%s, size=%d bytes).", trigger, len(doc))
	return nil
}

// registerBotCommands calls setMyCommands so Telegram shows a command menu when
// users tap "/" in the chat. Best-effort; failure is non-fatal.
//
// NOTE: When adding a new user-facing command, add it to the slice below so
// users can discover it through the Telegram command menu.
func registerBotCommands() {
	commands := []map[string]string{
		{"command": "drill", "description": "Get a grammar drill right now"},
		{"command": "word", "description": "Get a vocabulary word right now"},
		{"command": "idiom", "description": "Get an idiom of the day"},
		{"command": "collocation", "description": "Get a natural word partnership to learn"},
		{"command": "story", "description": "Get a mini story to read at your level"},
		{"command": "tip", "description": "Get a grammar tip (or /tip on|off)"},
		{"command": "quiz", "description": "Test yourself on a learned word"},
		{"command": "grammar", "description": "Bite-size grammar lessons (easy to advanced)"},
		{"command": "setup", "description": "Quick-setup: pick how much time you have per day"},
		{"command": "settings", "description": "View and change all your settings"},
		{"command": "level", "description": "Set difficulty (beginner/intermediate/upper-intermediate/advanced)"},
		{"command": "interval", "description": "Set how often practice arrives"},
		{"command": "tts", "description": "Toggle pronunciation audio on/off"},
		{"command": "stats", "description": "See your progress and streak"},
		{"command": "app", "description": "Open your English hub (progress, words & more)"},
		{"command": "mywords", "description": "Browse your learned vocabulary"},
		{"command": "bookmark", "description": "Bookmark or view important words"},
		{"command": "pause", "description": "Pause scheduled sends"},
		{"command": "resume", "description": "Resume scheduled sends"},
		{"command": "reset", "description": "Clear your practiced history"},
		{"command": "help", "description": "How it works"},
	}
	payload := map[string]interface{}{"commands": commands}
	if err := telegram.Post("setMyCommands", payload); err != nil {
		log.Printf("⚠️  [INIT] Could not register bot commands: %v", err)
	} else {
		log.Printf("✅ [INIT] Registered %d bot commands with Telegram.", len(commands))
	}
}

// setChatMenuButton makes the Mini App the bot's default chat menu button — the
// persistent button beside the message input that opens the app for every user.
// Requires WEB_APP_URL to be a public https:// URL (caller ensures it is set).
func setChatMenuButton() {
	payload := map[string]interface{}{
		"menu_button": map[string]interface{}{
			"type":    "web_app",
			"text":    "📱 App",
			"web_app": map[string]interface{}{"url": config.WebAppURL},
		},
	}
	if err := telegram.Post("setChatMenuButton", payload); err != nil {
		log.Printf("⚠️  [WEBAPP] Could not set chat menu button: %v", err)
	} else {
		log.Printf("✅ [WEBAPP] Mini App set as the persistent chat menu button.")
	}
}

// sendPendingChangelogs delivers any changelog entries the user has not yet
// seen and marks them as delivered immediately after each successful send.
// Silent entries are marked as seen without sending a message to regular users,
// but the maintainer still receives them with an internal-deploy indicator.
func sendPendingChangelogs(store *Store, notifier telegram.Notifier, chatID int64) {
	unseen, err := store.UnseenChangelogs(chatID)
	if err != nil {
		log.Printf("⚠️  [CHANGELOG] Could not fetch unseen changelogs for ChatID %d: %v", chatID, err)
		return
	}
	for _, entry := range unseen {
		if entry.Silent {
			// Maintainer receives a private deploy notice.
			if isMaintainer(chatID) {
				msg := fmt.Sprintf("🔧 <b>[Internal Deploy v%s]</b>\n\n%s", entry.Version, entry.Text)
				_ = notifier.Send(chatID, msg)
			}
			if err := store.MarkChangelogSeen(chatID, entry.Version); err != nil {
				log.Printf("⚠️  [CHANGELOG] Could not mark silent v%s seen for ChatID %d: %v", entry.Version, chatID, err)
			}
			continue
		}
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
	CREATE TABLE IF NOT EXISTS sent_tips (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_tips_chat ON sent_tips(chat_id);
	CREATE TABLE IF NOT EXISTS sent_collocations (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_collocations_chat ON sent_collocations(chat_id);
	CREATE TABLE IF NOT EXISTS sent_stories (
		chat_id INTEGER NOT NULL,
		word    TEXT    NOT NULL,
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE INDEX IF NOT EXISTS idx_sent_stories_chat ON sent_stories(chat_id);
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
	CREATE TABLE IF NOT EXISTS daily_tip_delivery (
		chat_id  INTEGER NOT NULL,
		tip_date TEXT    NOT NULL,
		sent_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, tip_date)
	);
	CREATE TABLE IF NOT EXISTS collocation_delivery (
		chat_id          INTEGER NOT NULL,
		collocation_date TEXT    NOT NULL,
		sent_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, collocation_date)
	);
	CREATE TABLE IF NOT EXISTS story_delivery (
		chat_id    INTEGER NOT NULL,
		story_date TEXT    NOT NULL,
		sent_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, story_date)
	);
	CREATE TABLE IF NOT EXISTS pool_exhaustion_notice (
		chat_id     INTEGER NOT NULL,
		kind        TEXT    NOT NULL,
		level       TEXT    NOT NULL,
		pool_count  INTEGER NOT NULL DEFAULT 0,
		notified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, kind, level)
	);
	CREATE TABLE IF NOT EXISTS user_prefs (
		chat_id              INTEGER PRIMARY KEY,
		level                TEXT    NOT NULL DEFAULT 'intermediate',
		paused               INTEGER NOT NULL DEFAULT 0,
		interval_minutes     INTEGER NOT NULL DEFAULT 60,
		tts_enabled          INTEGER NOT NULL DEFAULT 1,
		tips_enabled         INTEGER NOT NULL DEFAULT 1,
		quiz_enabled         INTEGER NOT NULL DEFAULT 1,
		idiom_enabled        INTEGER NOT NULL DEFAULT 1,
		collocation_enabled  INTEGER NOT NULL DEFAULT 1,
		story_enabled        INTEGER NOT NULL DEFAULT 1,
		review_enabled       INTEGER NOT NULL DEFAULT 1,
		digest_enabled       INTEGER NOT NULL DEFAULT 1,
		daily_review_enabled INTEGER NOT NULL DEFAULT 1,
		quiz_interval_hours  INTEGER NOT NULL DEFAULT 6,
		first_name           TEXT    NOT NULL DEFAULT '',
		display_name         TEXT    NOT NULL DEFAULT '',
		streak_celebrated    INTEGER NOT NULL DEFAULT 0,
		public_id            TEXT    NOT NULL DEFAULT '',
		updated_at           DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS kudos (
		from_chat_id INTEGER NOT NULL,
		to_chat_id   INTEGER NOT NULL,
		created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (from_chat_id, to_chat_id)
	);
	CREATE INDEX IF NOT EXISTS idx_kudos_to ON kudos(to_chat_id);
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
	CREATE TABLE IF NOT EXISTS activity_log (
		chat_id INTEGER NOT NULL,
		day     TEXT    NOT NULL,
		cnt     INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (chat_id, day)
	);
	CREATE TABLE IF NOT EXISTS review_perf (
		chat_id         INTEGER NOT NULL PRIMARY KEY,
		window_correct  INTEGER NOT NULL DEFAULT 0,
		window_total    INTEGER NOT NULL DEFAULT 0,
		last_suggest_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS deck_cards (
		deck_id       TEXT    NOT NULL,
		term          TEXT    NOT NULL,
		definition    TEXT    NOT NULL DEFAULT '',
		example       TEXT    NOT NULL DEFAULT '',
		group_label   TEXT    NOT NULL DEFAULT '',
		ordering      INTEGER NOT NULL DEFAULT 0,
		persian       TEXT    NOT NULL DEFAULT '',
		pronunciation TEXT    NOT NULL DEFAULT '',
		mnemonic      TEXT    NOT NULL DEFAULT '',
		PRIMARY KEY (deck_id, term)
	);
	CREATE TABLE IF NOT EXISTS leitner_progress (
		chat_id    INTEGER  NOT NULL,
		deck_id    TEXT     NOT NULL,
		term       TEXT     NOT NULL,
		box        INTEGER  NOT NULL DEFAULT 1,
		due_at     DATETIME NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, deck_id, term)
	);
	CREATE INDEX IF NOT EXISTS idx_leitner_due ON leitner_progress(chat_id, deck_id, due_at);
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
	CREATE TABLE IF NOT EXISTS audio_cache (
		word       TEXT    PRIMARY KEY,
		file_id    TEXT    NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS bookmarks (
		chat_id  INTEGER NOT NULL,
		word     TEXT    NOT NULL,
		added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (chat_id, word)
	);
	CREATE TABLE IF NOT EXISTS bot_config (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
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

// ---------------------------------------------------------------------------
// bot_config — persistent key-value runtime configuration
// ---------------------------------------------------------------------------

// SetBotConfig upserts a key-value pair in bot_config.
func (s *Store) SetBotConfig(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO bot_config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value,
	)
	return err
}

// DeleteBotConfig removes a config key (used to clear an override back to its
// default). A missing key is not an error.
func (s *Store) DeleteBotConfig(key string) error {
	_, err := s.db.Exec("DELETE FROM bot_config WHERE key = ?", key)
	return err
}

// GetBotConfig reads a single config value. Returns ("", false) if not set.
func (s *Store) GetBotConfig(key string) (string, bool) {
	var v string
	err := s.db.QueryRow("SELECT value FROM bot_config WHERE key = ?", key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// LoadBotConfig reads all rows from bot_config and overwrites the corresponding
// global variables. Environment variables act as defaults; DB values (set via
// /config) override them and survive restarts.
func (s *Store) LoadBotConfig() {
	rows, err := s.db.Query("SELECT key, value FROM bot_config")
	if err != nil {
		log.Printf("⚠️  [CONFIG] Could not load bot_config: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		switch k {
		case "pool_target":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				config.PoolTarget = n
			}
		case "pool_min":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				config.PoolMin = n
			}
		case "quiet_start":
			config.QuietStart = v
		case "quiet_end":
			config.QuietEnd = v
		case "tts_enabled":
			switch strings.ToLower(v) {
			case "true", "1", "on":
				config.TTSEnabled = true
			case "false", "0", "off":
				config.TTSEnabled = false
			}
		case "gen_spacing":
			if d, err := time.ParseDuration(v); err == nil {
				config.GenSpacing = d
			}
		case "review_batch_max":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				config.ReviewBatchMax = n
			}
		default:
			// Per-(kind,level) ("pool_kl_<kind>_<level>"), per-kind ("pool_kind_<kind>")
			// and per-level ("pool_level_<level>") pool-size overrides. Unknown keys
			// are ignored. pool_kl_ is checked first; its key never collides with the
			// other two prefixes.
			if rest, ok := strings.CutPrefix(k, "pool_kl_"); ok {
				kind, level, found := strings.Cut(rest, "_")
				if n, err := strconv.Atoi(v); found && err == nil && n > 0 && config.IsConfigurableKind(kind) {
					if lv, valid := normalizeLevel(level); valid {
						config.SetPoolKindLevelOverride(kind, lv, n)
					}
				}
			} else if kind, ok := strings.CutPrefix(k, "pool_kind_"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 && config.IsConfigurableKind(kind) {
					config.SetPoolKindOverride(kind, n)
				}
			} else if level, ok := strings.CutPrefix(k, "pool_level_"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					if lv, valid := normalizeLevel(level); valid {
						config.SetPoolLevelOverride(lv, n)
					}
				}
			}
		}
		count++
	}
	if count > 0 {
		log.Printf("⚙️  [CONFIG] Loaded %d setting(s) from bot_config (override env defaults).", count)
	}
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
			"ALTER TABLE user_prefs ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 60",
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
	// user_prefs.tips_enabled was added in v1.15.0.
	if !s.columnExists("user_prefs", "tips_enabled") {
		log.Println("💾 [DB_MIGRATE] Adding user_prefs.tips_enabled column...")
		if _, err := s.db.Exec(
			"ALTER TABLE user_prefs ADD COLUMN tips_enabled INTEGER NOT NULL DEFAULT 1",
		); err != nil {
			return err
		}
	}
	// user_prefs feature-toggle and quiz-interval columns added in v1.17.0.
	for col, def := range map[string]string{
		"quiz_enabled":         "INTEGER NOT NULL DEFAULT 1",
		"idiom_enabled":        "INTEGER NOT NULL DEFAULT 1",
		"review_enabled":       "INTEGER NOT NULL DEFAULT 1",
		"digest_enabled":       "INTEGER NOT NULL DEFAULT 1",
		"daily_review_enabled": "INTEGER NOT NULL DEFAULT 1",
		"quiz_interval_hours":  "INTEGER NOT NULL DEFAULT 6",
	} {
		if !s.columnExists("user_prefs", col) {
			log.Printf("💾 [DB_MIGRATE] Adding user_prefs.%s column...", col)
			if _, err := s.db.Exec("ALTER TABLE user_prefs ADD COLUMN " + col + " " + def); err != nil {
				return err
			}
		}
	}
	// user_prefs collocation/story toggle columns added in v1.23.0.
	for col, def := range map[string]string{
		"collocation_enabled": "INTEGER NOT NULL DEFAULT 1",
		"story_enabled":       "INTEGER NOT NULL DEFAULT 1",
	} {
		if !s.columnExists("user_prefs", col) {
			log.Printf("💾 [DB_MIGRATE] Adding user_prefs.%s column...", col)
			if _, err := s.db.Exec("ALTER TABLE user_prefs ADD COLUMN " + col + " " + def); err != nil {
				return err
			}
		}
	}
	// deck_cards Persian/pronunciation/mnemonic columns added in v1.30.0; filled
	// from the source JSON (where present) and the AI backfill worker otherwise.
	for _, col := range []string{"persian", "pronunciation", "mnemonic"} {
		if !s.columnExists("deck_cards", col) {
			log.Printf("💾 [DB_MIGRATE] Adding deck_cards.%s column...", col)
			if _, err := s.db.Exec("ALTER TABLE deck_cards ADD COLUMN " + col + " TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	// user_prefs UI-enhancement columns added in v1.18.0; display_name (Mini App
	// leaderboard) added in v1.24.0; photo_url (leaderboard avatars) in v1.28.0;
	// public_id (opaque id for the Mini App profile pages, never the chat_id) in v1.32.0.
	for col, def := range map[string]string{
		"first_name":        "TEXT NOT NULL DEFAULT ''",
		"display_name":      "TEXT NOT NULL DEFAULT ''",
		"streak_celebrated": "INTEGER NOT NULL DEFAULT 0",
		"photo_url":         "TEXT NOT NULL DEFAULT ''",
		"public_id":         "TEXT NOT NULL DEFAULT ''",
	} {
		if !s.columnExists("user_prefs", col) {
			log.Printf("💾 [DB_MIGRATE] Adding user_prefs.%s column...", col)
			if _, err := s.db.Exec("ALTER TABLE user_prefs ADD COLUMN " + col + " " + def); err != nil {
				return err
			}
		}
	}
	// Index public_id here (not in the always-run schema block) so it's created
	// only after the column exists on migrated legacy DBs (v1.32.0).
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_user_public ON user_prefs(public_id)"); err != nil {
		return err
	}
	// bookmarks table added in v1.21.0; CREATE IF NOT EXISTS is safe for existing DBs.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS bookmarks (
			chat_id  INTEGER NOT NULL,
			word     TEXT    NOT NULL,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (chat_id, word)
		)`); err != nil {
		return err
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

// RecordSentTip stores a grammar tip topic as having been sent to a chat (idempotent).
func (s *Store) RecordSentTip(chatID int64, topic string) error {
	_, err := s.db.Exec(
		`INSERT INTO sent_tips (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(topic)),
	)
	return err
}

// RecordSentCollocation stores a collocation as having been sent to a chat.
// sent_at records the first send; last_sent_at is bumped on every send to drive
// least-recently-served rotation.
func (s *Store) RecordSentCollocation(chatID int64, collocation string) error {
	_, err := s.db.Exec(
		`INSERT INTO sent_collocations (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(collocation)),
	)
	return err
}

// RecordSentStory stores a mini-story title as having been sent to a chat.
// sent_at records the first send; last_sent_at is bumped on every send to drive
// least-recently-served rotation.
func (s *Store) RecordSentStory(chatID int64, title string) error {
	_, err := s.db.Exec(
		`INSERT INTO sent_stories (chat_id, word, last_sent_at) VALUES (?, ?, strftime('%Y-%m-%d %H:%M:%f','now'))
		 ON CONFLICT(chat_id, word) DO UPDATE SET last_sent_at = strftime('%Y-%m-%d %H:%M:%f','now')`,
		chatID, strings.ToLower(strings.TrimSpace(title)),
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

// CachedAudioFileID returns the Telegram file_id for a previously uploaded
// pronunciation of the given word, or "" if none is cached.
func (s *Store) CachedAudioFileID(word string) string {
	var fileID string
	err := s.db.QueryRow(
		"SELECT file_id FROM audio_cache WHERE word = ?",
		strings.ToLower(strings.TrimSpace(word)),
	).Scan(&fileID)
	if err != nil {
		return ""
	}
	return fileID
}

// CacheAudioFileID stores the Telegram file_id for a word's pronunciation audio
// so subsequent sends can reuse it without regenerating.
func (s *Store) CacheAudioFileID(word, fileID string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO audio_cache (word, file_id) VALUES (?, ?)",
		strings.ToLower(strings.TrimSpace(word)), fileID,
	)
	return err
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
