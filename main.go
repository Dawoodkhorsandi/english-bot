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

	"google.golang.org/genai"
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

// Store wraps the SQLite connection used to persist subscribers and the
// per-user history of verbs that have already been sent.
type Store struct {
	db *sql.DB
}

type TelegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	Chat      TelegramChat  `json:"chat"`
	Text      string        `json:"text"`
	From      *TelegramUser `json:"from"`
}

type TelegramChat struct {
	ID int64 `json:"id"`
}

type TelegramUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func main() {
	log.Println("⚙️  [INIT] Initializing Telegram Grammar Muscle Memory Bot...")

	// Mask and print tokens for debug safety validation
	log.Printf("⚙️  [CONFIG] Telegram Token Loaded: %t (Length: %d)", TelegramBotToken != "YOUR_TELEGRAM_BOT_TOKEN" && TelegramBotToken != "", len(TelegramBotToken))
	log.Printf("⚙️  [CONFIG] Gemini API Key Loaded: %t (Length: %d)", GeminiAPIKey != "YOUR_GEMINI_API_KEY" && GeminiAPIKey != "", len(GeminiAPIKey))
	log.Printf("⚙️  [CONFIG] Maintainer Chat ID Target: %s", MaintainerChatID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Gemini Client with correct modern ClientConfig matching modern SDK syntax
	log.Println("⚙️  [INIT] Establishing connection to Google GenAI platform...")
	aiClient, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: GeminiAPIKey})
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Failed to initialize Gemini client: %v", err)
	}
	log.Println("✅ [INIT] Google GenAI Client ready.")

	// 2. Open SQLite store (subscribers + per-user sent-word history)
	store, err := openStore(dbFile)
	if err != nil {
		log.Fatalf("❌ [CRITICAL] Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	// 3. Start Hourly Ticker for Broadcasts
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	go func() {
		log.Println("⏰ [SYSTEM] Hourly background broadcast ticker routine started.")
		for {
			select {
			case <-ticker.C:
				log.Println("⏰ [TIMER] 1 Hour elapsed. Triggering automatic distribution cycle...")
				broadcastDrill(ctx, aiClient, store)
			case <-ctx.Done():
				log.Println("⏰ [TIMER] Broadcast loop context cancelled. Exiting routine.")
				return
			}
		}
	}()

	// 4. Start long polling for /commands
	log.Println("📡 [SYSTEM] Launching Telegram incoming updates consumer engine...")
	go pollTelegramUpdates(ctx, aiClient, store)

	// Graceful shutdown listener
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	log.Println("🚀 [SYSTEM] Startup complete. Bot is fully operational and listening.")
	sig := <-stopChan
	log.Printf("🛑 [SYSTEM] Shutdown intercept caught OS Signal: %v. Cleaning tasks...", sig)
}

func broadcastDrill(ctx context.Context, aiClient *genai.Client, store *Store) {
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [BROADCAST_ERR] Could not read subscriber pool from store: %v", err)
		return
	}

	subscriberCount := len(chats)
	log.Printf("📢 [BROADCAST] Active target pool size: %d subscriber nodes.", subscriberCount)
	if subscriberCount == 0 {
		log.Println("📢 [BROADCAST] Queue empty. Skipping execution pipeline.")
		return
	}

	log.Printf("📢 [BROADCAST] Starting fan-out personalized distribution sequence across %d targets...", len(chats))
	for index, chatID := range chats {
		log.Printf("➔ [SENDING] Index [%d/%d] preparing unique drill for ChatID: %d", index+1, subscriberCount, chatID)

		drillText, err := generatePersonalizedDrill(ctx, aiClient, store, chatID)
		if err != nil {
			log.Printf("❌ [BROADCAST_ERR] Could not generate drill for target %d: %v", chatID, err)
			continue
		}

		if err := sendToTelegram(chatID, drillText); err != nil {
			log.Printf("❌ [SEND_ERR] Failed transmission node packet target %d: %v", chatID, err)
		}
	}
	log.Println("✅ [BROADCAST] Finished out broadcast stream sweep updates cycle.")
}

// generatePersonalizedDrill produces a drill for a single user that avoids any
// verb already sent to that user, then records the newly used verb.
func generatePersonalizedDrill(ctx context.Context, aiClient *genai.Client, store *Store, chatID int64) (string, error) {
	sentWords, err := store.SentWords(chatID)
	if err != nil {
		log.Printf("⚠️  [HISTORY] Could not load sent-word history for %d: %v (continuing without exclusions)", chatID, err)
	}
	log.Printf("🧾 [HISTORY] ChatID %d has %d previously sent verbs to exclude.", chatID, len(sentWords))

	drillText, verb, err := generateGeminiDrill(ctx, aiClient, sentWords)
	if err != nil {
		return "", err
	}

	if verb != "" {
		if err := store.RecordSentWord(chatID, verb); err != nil {
			log.Printf("⚠️  [HISTORY] Failed to record verb %q for chat %d: %v", verb, chatID, err)
		} else {
			log.Printf("🧾 [HISTORY] Recorded verb %q for ChatID %d.", verb, chatID)
		}
	} else {
		log.Printf("⚠️  [HISTORY] Could not parse a verb from the generated drill for ChatID %d; nothing recorded.", chatID)
	}

	return drillText, nil
}

func pollTelegramUpdates(ctx context.Context, aiClient *genai.Client, store *Store) {
	var offset int64 = 0
	client := &http.Client{Timeout: 35 * time.Second} // Long poll safety margin timeout

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
				log.Printf("❌ [POLLER_NET_ERR] Network socket error connecting to Telegram API: %v. Retrying connection loop in 5 seconds...", err)
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
				log.Printf("❌ [POLLER_JSON_ERR] Unmarshal exception structure. Source raw: %s. Error message context: %v", string(body), err)
				time.Sleep(2 * time.Second)
				continue
			}

			if !updateResp.Ok {
				log.Printf("❌ [TELEGRAM_API_REJECT] Server false execution return status. Response Code payload reason: %s", updateResp.Description)
				time.Sleep(5 * time.Second)
				continue
			}

			updateCount := len(updateResp.Result)
			if updateCount > 0 {
				log.Printf("📥 [POLLER_INBOUND] Received packet batch stream container containing (%d) updates blocks.", updateCount)
			}

			for _, update := range updateResp.Result {
				offset = update.UpdateID + 1
				if update.Message != nil {
					log.Printf("📩 [MESSAGE_DISPATCH] Parsing message update index: %d | Chat ID: %d | Content: %q", update.UpdateID, update.Message.Chat.ID, update.Message.Text)
					handleMessage(ctx, aiClient, store, update.Message)
				} else {
					log.Printf("📝 [POLLER_SKIP] Received non-message telemetry block event update index: %d. Moving forward.", update.UpdateID)
				}
			}
		}
	}
}

func handleMessage(ctx context.Context, aiClient *genai.Client, store *Store, msg *TelegramMessage) {
	if msg.Text == "" {
		log.Printf("ℹ️  [ROUTER] Ignoring incoming request message tracking ID %d. Text block is empty.", msg.MessageID)
		return
	}

	chatID := msg.Chat.ID
	username := "unknown"
	if msg.From != nil && msg.From.Username != "" {
		username = msg.From.Username
	}

	log.Printf("🎮 [COMMAND_MATCH] Routing text expression string: %q evaluated from User: @%s (ID: %d)", msg.Text, username, chatID)

	switch msg.Text {
	case "/start":
		isNew, err := store.AddSubscriber(chatID)
		if err != nil {
			log.Printf("❌ [REGISTRATION_ERR] Failed to persist subscriber %d: %v", chatID, err)
		}

		log.Printf("💾 [REGISTRATION] DB Subscriber Lookup Evaluation -> Existing User status matches: %t", !isNew)

		if isNew {
			log.Printf("🎉 [NEW_USER] Stored system state modification commit. Triggering maintainer trace notifications...")

			fromID := chatID
			if msg.From != nil {
				fromID = msg.From.ID
			}
			maintainerMsg := fmt.Sprintf(`🎉 *New User Joined!*
*ID:* %d
*Username:* @%s`, fromID, username)

			mID, parseErr := strconv.ParseInt(MaintainerChatID, 10, 64)
			if parseErr != nil {
				log.Printf("❌ [PARSE_ERR] Config error value tracking MaintainerChatID '%s': %v", MaintainerChatID, parseErr)
			} else {
				log.Printf("➔ [NOTIFY_MAINTAINER] Routing new profile analytics metrics registration target ID: %d", mID)
				_ = sendToTelegram(mID, maintainerMsg)
			}
		}

		welcome := "👋 *Welcome to English Muscle Memory Bot!*\n\nI will send you a practical grammar verb drill every hour automatically.\n\n📚 *Available Commands:*\n/drill - Get a practice drill right now\n/reset - Clear your practiced-verb history\n/help - View explanation rules"
		_ = sendToTelegram(chatID, welcome)

	case "/help":
		helpText := "💡 *How Muscle Memory Practice Works:*\n\nDon't just think about rules—say the sentences out loud! Every hour I will send a new verb timeline. Read each sentence clear and fast to build subconscious natural speaking instincts.\n\nUse /drill to generate a custom one on demand.\nUse /reset to clear your practiced-verb history and start fresh."
		_ = sendToTelegram(chatID, helpText)

	case "/drill":
		log.Printf("🤖 [AI_FLOW] Direct interaction `/drill` requested manually by user node ID: %d. Calling model inference...", chatID)
		_ = sendToTelegram(chatID, "🔄 *Generating your custom verb drill...*")

		drill, err := generatePersonalizedDrill(ctx, aiClient, store, chatID)
		if err != nil {
			log.Printf("❌ [AI_ERR] Dynamic runtime inference model query drop failure on chat node ID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reach Gemini right now. Please try again.")
			return
		}

		log.Printf("✅ [AI_SUCCESS] Returning complete text execution packet to user node ID: %d", chatID)
		_ = sendToTelegram(chatID, drill)

	case "/reset":
		log.Printf("♻️  [RESET] User node ID: %d requested a history wipe. Clearing sent-word records...", chatID)
		cleared, err := store.ResetSentWords(chatID)
		if err != nil {
			log.Printf("❌ [RESET_ERR] Failed to clear history for chat node ID %d: %v", chatID, err)
			_ = sendToTelegram(chatID, "❌ Sorry, I couldn't reset your history right now. Please try again.")
			return
		}

		log.Printf("✅ [RESET] Cleared %d stored verbs for user node ID: %d", cleared, chatID)
		_ = sendToTelegram(chatID, fmt.Sprintf("♻️ *History reset!*\n\nI cleared *%d* previously practiced verbs. You may now see them again in future drills.", cleared))

	default:
		log.Printf("ℹ️  [ROUTER_UNHANDLED] Received unstructured expression sequence data block: %q. Dismissing pattern payload execution.", msg.Text)
		_ = sendToTelegram(chatID, "🤖 I only understand commands right now. Try typing /drill or /help!")
	}
}

// generateGeminiDrill queries Gemini 2.5 Flash with a robust Exponential Backoff Retry engine.
// excludeVerbs is a list of verbs already sent to the target user; the model is
// instructed to avoid them. It returns the drill text and the verb that was used.
func generateGeminiDrill(ctx context.Context, client *genai.Client, excludeVerbs []string) (string, string, error) {
	exclusionClause := ""
	if len(excludeVerbs) > 0 {
		exclusionClause = fmt.Sprintf("\n\nIMPORTANT: Do NOT use any of these verbs (they were already practiced): %s.\nPick a different, fresh everyday verb that is NOT in that list.", strings.Join(excludeVerbs, ", "))
	}

	prompt := `Select ONE random, useful everyday English verb. 
Generate a "Grammar Muscle Memory Drill" exactly like this structure:

**Verb of the Hour:** [Verb]
* **Routine (Simple Present):** [Example sentence]
* **Right Now (Present Continuous):** [Example sentence]
* **The Finished Past (Simple Past):** [Example sentence]
* **Past Progress (Past Continuous):** [Example sentence]
* **The Future Plan (Be going to):** [Example sentence]
* **Life Experience / Duration (Present Perfect):** [Example sentence]

Keep the sentences natural, short, and practical for daily conversations.` + exclusionClause

	maxRetries := 3
	backoff := 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		log.Printf("🧠 [GEMINI_API_CALL] Dispatching context options to gemini-2.5-flash (Attempt %d/%d, exclusions: %d)...", i+1, maxRetries, len(excludeVerbs))

		resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(prompt), nil)
		if err == nil {
			if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil && len(resp.Candidates[0].Content.Parts) > 0 {
				text := resp.Candidates[0].Content.Parts[0].Text
				if strings.TrimSpace(text) != "" {
					verb := parseVerb(text)
					log.Printf("🧠 [GEMINI_API_RESPONSE] Content response chunk returned successfully. Parsed verb: %q", verb)
					return text, verb, nil
				}
			}
		}

		log.Printf("⚠️  [GEMINI_RETRY] Attempt %d failed or returned empty: %v. Retrying in %v...", i+1, err, backoff)

		select {
		case <-time.After(backoff):
			backoff *= 2 // Double the sleep penalty delay for next execution loop (2s -> 4s -> 8s)
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}

	return "", "", fmt.Errorf("gemini platform remained unavailable after %d retry attempts", maxRetries)
}

// parseVerb extracts the verb from the "Verb of the Hour:" line of a drill so it
// can be stored as part of the user's history.
func parseVerb(drill string) string {
	for _, line := range strings.Split(drill, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "verb of the hour") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		verb := line[idx+1:]
		// Strip markdown emphasis, brackets and surrounding punctuation/whitespace.
		verb = strings.Trim(verb, " *_`[]()\t\r")
		verb = strings.TrimSpace(verb)
		if verb == "" {
			continue
		}
		// Keep only the first token (the verb itself) and normalise case.
		if fields := strings.Fields(verb); len(fields) > 0 {
			verb = fields[0]
		}
		verb = strings.Trim(verb, " *_`[]().,!?")
		return strings.ToLower(verb)
	}
	return ""
}

func sendToTelegram(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TelegramBotToken)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	jsonPayload, _ := json.Marshal(payload)

	log.Printf("➔ [HTTP_POST] Target URL: %s | Outbound payload byte length: %d", fmt.Sprintf(".../bot%s/sendMessage", TelegramBotToken[:5]), len(jsonPayload))
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram returned status code: %d | Raw API response: %s", resp.StatusCode, string(respBody))
	}

	return nil
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
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	store := &Store{db: db}
	store.migrateLegacyJSON()

	log.Println("💾 [DB] SQLite store ready (subscribers + per-user sent-word history).")
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// migrateLegacyJSON imports subscribers from the old subscribers.json file (if
// present) into the SQLite database, then renames it so it runs only once.
func (s *Store) migrateLegacyJSON() {
	data, err := os.ReadFile(legacyDBFile)
	if err != nil {
		return // No legacy file, nothing to migrate.
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

// ResetSentWords deletes all sent-word history for a chat and returns the number
// of records that were removed.
func (s *Store) ResetSentWords(chatID int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM sent_words WHERE chat_id = ?", chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
