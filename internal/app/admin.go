package app

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// adminMsgTarget holds the chat ID of the user the admin wants to message.
// Zero means the admin is not in "message user" mode.
var adminMsgTarget atomic.Int64

// ---------------------------------------------------------------------------
// Admin panel — user list with pagination & individual user detail view
// ---------------------------------------------------------------------------

const adminUsersPerPage = 8 // users per page in the /users list

// AdminUserRow is a lightweight row for the paginated user list.
type AdminUserRow struct {
	ChatID    int64
	FirstName string
	Level     string
	Paused    bool
	CreatedAt time.Time
}

// AdminUserDetail holds the full detail for a single user viewed by admin.
type AdminUserDetail struct {
	ChatID    int64
	FirstName string
	CreatedAt time.Time

	// Prefs
	Level              string
	Paused             bool
	Interval           int
	TTSEnabled         bool
	TipsEnabled        bool
	QuizEnabled        bool
	IdiomEnabled       bool
	ReviewEnabled      bool
	DigestEnabled      bool
	DailyReviewEnabled bool
	QuizIntervalHours  int

	// Stats
	Verbs         int
	Words         int
	Idioms        int
	Tips          int
	Mastered      int
	QuizAnswered  int
	QuizCorrect   int
	ActiveDays    int
	CurrentStreak int
	LongestStreak int

	// SRS
	DueReviews  int
	TotalReview int
}

// ---------------------------------------------------------------------------
// Store methods for admin
// ---------------------------------------------------------------------------

// AdminListUsers returns a page of users (0-indexed) ordered by creation date
// descending, plus the total count.
func (s *Store) AdminListUsers(page, perPage int) ([]AdminUserRow, int, error) {
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM subscribers").Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := page * perPage
	rows, err := s.db.Query(`
		SELECT s.chat_id, COALESCE(p.first_name, ''), COALESCE(p.level, 'intermediate'),
		       COALESCE(p.paused, 0), s.created_at
		FROM subscribers s
		LEFT JOIN user_prefs p ON p.chat_id = s.chat_id
		ORDER BY s.created_at DESC
		LIMIT ? OFFSET ?`,
		perPage, offset,
	)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()

	var users []AdminUserRow
	for rows.Next() {
		var u AdminUserRow
		var paused int
		var rawCreated any
		if err := rows.Scan(&u.ChatID, &u.FirstName, &u.Level, &paused, &rawCreated); err != nil {
			return nil, total, err
		}
		u.Paused = paused == 1
		if ts, ok := parseStoredUTC(rawCreated); ok {
			u.CreatedAt = ts.In(config.AppLocation)
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// AdminUserDetail returns a comprehensive detail view for one user.
func (s *Store) AdminUserDetail(chatID int64) (AdminUserDetail, error) {
	var d AdminUserDetail
	d.ChatID = chatID

	// Prefs
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		return d, err
	}
	d.FirstName = prefs.FirstName
	d.Level = prefs.Level
	d.Paused = prefs.Paused
	d.Interval = prefs.Interval
	d.TTSEnabled = prefs.TTSEnabled
	d.TipsEnabled = prefs.TipsEnabled
	d.QuizEnabled = prefs.QuizEnabled
	d.IdiomEnabled = prefs.IdiomEnabled
	d.ReviewEnabled = prefs.ReviewEnabled
	d.DigestEnabled = prefs.DigestEnabled
	d.DailyReviewEnabled = prefs.DailyReviewEnabled
	d.QuizIntervalHours = prefs.QuizIntervalHours

	// Created at
	var rawCreated any
	if err := s.db.QueryRow("SELECT created_at FROM subscribers WHERE chat_id = ?", chatID).Scan(&rawCreated); err == nil {
		if ts, ok := parseStoredUTC(rawCreated); ok {
			d.CreatedAt = ts.In(config.AppLocation)
		}
	}

	// Counts
	_ = s.db.QueryRow("SELECT COUNT(*) FROM sent_words WHERE chat_id = ?", chatID).Scan(&d.Verbs)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM sent_vocab WHERE chat_id = ?", chatID).Scan(&d.Words)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM sent_idioms WHERE chat_id = ?", chatID).Scan(&d.Idioms)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM sent_tips WHERE chat_id = ?", chatID).Scan(&d.Tips)

	d.Mastered, _ = s.MasteredCount(chatID)
	d.QuizAnswered, d.QuizCorrect, _ = s.QuizStats(chatID)

	// SRS
	_ = s.db.QueryRow("SELECT COUNT(*) FROM review_schedule WHERE chat_id = ?", chatID).Scan(&d.TotalReview)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM review_schedule WHERE chat_id = ? AND due_at <= ?",
		chatID, time.Now().UTC().Format("2006-01-02 15:04:05")).Scan(&d.DueReviews)

	// Activity / streaks
	counts, err := s.activityDays(chatID)
	if err == nil {
		d.ActiveDays = len(counts)
		days := make(map[string]bool, len(counts))
		for day := range counts {
			days[day] = true
		}
		d.CurrentStreak, d.LongestStreak = computeStreaks(days, time.Now().In(config.AppLocation))
	}

	return d, nil
}

// ---------------------------------------------------------------------------
// Command & callback handlers
// ---------------------------------------------------------------------------

// handleAdminUsers handles the /users command — shows a paginated user list.
func handleAdminUsers(store *Store, notifier telegram.Notifier, chatID int64) {
	log.Printf("👥 [ADMIN] /users requested by ChatID %d.", chatID)
	sendAdminUserListPage(store, notifier, chatID, 0, 0)
}

// sendAdminUserListPage sends (or edits) a paginated user list message.
// If editMessageID > 0, it edits the existing message; otherwise sends a new one.
func sendAdminUserListPage(store *Store, notifier telegram.Notifier, chatID int64, page int, editMessageID int64) {
	users, total, err := store.AdminListUsers(page, adminUsersPerPage)
	if err != nil {
		log.Printf("❌ [ADMIN] Failed to list users: %v", err)
		_ = notifier.Send(chatID, "❌ Could not load user list.")
		return
	}

	totalPages := (total + adminUsersPerPage - 1) / adminUsersPerPage
	if totalPages == 0 {
		totalPages = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "👥 <b>Users</b> — Page %d/%d (total: %d)\n\n", page+1, totalPages, total)

	for i, u := range users {
		idx := page*adminUsersPerPage + i + 1
		name := u.FirstName
		if name == "" {
			name = "—"
		}
		status := "✅"
		if u.Paused {
			status = "⏸️"
		}
		fmt.Fprintf(&b, "%d. %s <b>%s</b>  %s  <code>%d</code>\n",
			idx, status, name, levelLabel(u.Level), u.ChatID)
	}

	if len(users) == 0 {
		b.WriteString("<i>No users found.</i>\n")
	}

	b.WriteString("\nTap a user below to view details:")

	// Build keyboard: user buttons (2 per row) + nav row
	var rows [][]telegram.InlineButton

	for i := 0; i < len(users); i += 2 {
		var row []telegram.InlineButton
		for j := i; j < i+2 && j < len(users); j++ {
			u := users[j]
			label := u.FirstName
			if label == "" {
				label = fmt.Sprintf("%d", u.ChatID)
			}
			if len(label) > 20 {
				label = label[:20]
			}
			row = append(row, telegram.InlineButton{
				Text:         label,
				CallbackData: fmt.Sprintf("admin:user:%d", u.ChatID),
			})
		}
		rows = append(rows, row)
	}

	// Navigation row
	var navRow []telegram.InlineButton
	if page > 0 {
		navRow = append(navRow, telegram.InlineButton{
			Text:         "◀️ Back",
			CallbackData: fmt.Sprintf("admin:users:%d", page-1),
		})
	}
	navRow = append(navRow, telegram.InlineButton{
		Text:         fmt.Sprintf("%d/%d", page+1, totalPages),
		CallbackData: "admin:noop",
	})
	if page+1 < totalPages {
		navRow = append(navRow, telegram.InlineButton{
			Text:         "Next ▶️",
			CallbackData: fmt.Sprintf("admin:users:%d", page+1),
		})
	}
	rows = append(rows, navRow)

	text := b.String()
	if editMessageID > 0 {
		_ = notifier.EditMessage(chatID, editMessageID, text, rows)
	} else {
		_ = notifier.SendKeyboard(chatID, text, rows)
	}
}

// sendAdminUserDetail sends (or edits) a detailed user view for the admin.
func sendAdminUserDetail(store *Store, notifier telegram.Notifier, chatID int64, targetChatID int64, editMessageID int64) {
	d, err := store.AdminUserDetail(targetChatID)
	if err != nil {
		log.Printf("❌ [ADMIN] Failed to load user detail for %d: %v", targetChatID, err)
		_ = notifier.Send(chatID, fmt.Sprintf("❌ Could not load details for user <code>%d</code>.", targetChatID))
		return
	}

	name := d.FirstName
	if name == "" {
		name = "—"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "👤 <b>User Detail: %s</b>\n", name)
	b.WriteString(fmt.Sprintf("ID: <code>%d</code>\n", d.ChatID))
	if !d.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("📅 Joined: <b>%s</b>\n", d.CreatedAt.Format("2 Jan 2006 15:04")))
	}
	b.WriteString("\n")

	// State
	b.WriteString("⚙️ <b>Settings</b>\n")
	b.WriteString(fmt.Sprintf("  🎚️ Level: <b>%s</b>\n", levelLabel(d.Level)))
	if d.Paused {
		b.WriteString("  ⏸️ Status: <b>Paused</b>\n")
	} else {
		b.WriteString("  ✅ Status: <b>Active</b>\n")
	}
	b.WriteString(fmt.Sprintf("  ⏱️ Interval: <b>%s</b>\n", intervalLabel(d.Interval)))
	b.WriteString(fmt.Sprintf("  🧩 Quiz interval: <b>%s</b>\n", quizIntervalLabel(d.QuizIntervalHours)))
	b.WriteString("\n")

	// Toggles
	b.WriteString("🔘 <b>Toggles</b>\n")
	b.WriteString(fmt.Sprintf("  🔊 TTS: %s\n", toggleIcon(d.TTSEnabled)))
	b.WriteString(fmt.Sprintf("  💡 Tips: %s\n", toggleIcon(d.TipsEnabled)))
	b.WriteString(fmt.Sprintf("  🧩 Quiz: %s\n", toggleIcon(d.QuizEnabled)))
	b.WriteString(fmt.Sprintf("  🗣️ Idiom: %s\n", toggleIcon(d.IdiomEnabled)))
	b.WriteString(fmt.Sprintf("  🧠 SRS Review: %s\n", toggleIcon(d.ReviewEnabled)))
	b.WriteString(fmt.Sprintf("  📊 Weekly Digest: %s\n", toggleIcon(d.DigestEnabled)))
	b.WriteString(fmt.Sprintf("  🌙 Daily Review: %s\n", toggleIcon(d.DailyReviewEnabled)))
	b.WriteString("\n")

	// Progress
	b.WriteString("📈 <b>Progress</b>\n")
	b.WriteString(fmt.Sprintf("  🎯 Drills: <b>%d</b>\n", d.Verbs))
	b.WriteString(fmt.Sprintf("  📘 Words: <b>%d</b>  (mastered: <b>%d</b>)\n", d.Words, d.Mastered))
	b.WriteString(fmt.Sprintf("  🗣️ Idioms: <b>%d</b>\n", d.Idioms))
	b.WriteString(fmt.Sprintf("  💡 Tips: <b>%d</b>\n", d.Tips))
	b.WriteString("\n")

	// Quiz
	if d.QuizAnswered > 0 {
		pct := d.QuizCorrect * 100 / d.QuizAnswered
		b.WriteString(fmt.Sprintf("  🧩 Quiz: <b>%d%%</b> accuracy (%d/%d)\n", pct, d.QuizCorrect, d.QuizAnswered))
	} else {
		b.WriteString("  🧩 Quiz: <b>0</b> answers\n")
	}

	// SRS
	b.WriteString(fmt.Sprintf("  🧠 SRS: <b>%d</b> words tracked, <b>%d</b> due now\n", d.TotalReview, d.DueReviews))
	b.WriteString("\n")

	// Streaks
	flame := ""
	if d.CurrentStreak >= 3 {
		flame = " 🔥"
	}
	b.WriteString(fmt.Sprintf("⚡ Streak: <b>%d day%s</b>%s  (best: %d)\n", d.CurrentStreak, plural(d.CurrentStreak), flame, d.LongestStreak))
	b.WriteString(fmt.Sprintf("🗓️ Active days: <b>%d</b>\n", d.ActiveDays))

	// Action buttons + back button
	rows := [][]telegram.InlineButton{
		{
			{Text: "✉️ Send Message", CallbackData: fmt.Sprintf("admin:msg:%d", d.ChatID)},
		},
		{{Text: "◀️ Back to user list", CallbackData: "admin:users:0"}},
	}

	if editMessageID > 0 {
		_ = notifier.EditMessage(chatID, editMessageID, b.String(), rows)
	} else {
		_ = notifier.SendKeyboard(chatID, b.String(), rows)
	}
}

// handleAdminCallback processes all "admin:*" callback data.
func handleAdminCallback(store *Store, notifier telegram.Notifier, cb *telegram.CallbackQuery, chatID int64) {
	data := cb.Data

	// admin:noop — page indicator, just acknowledge
	if data == "admin:noop" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	// admin:users:<page> — paginate user list
	if strings.HasPrefix(data, "admin:users:") {
		pageStr := strings.TrimPrefix(data, "admin:users:")
		page, err := strconv.Atoi(pageStr)
		if err != nil || page < 0 {
			page = 0
		}
		_ = notifier.AnswerCallback(cb.ID, "")
		sendAdminUserListPage(store, notifier, chatID, page, cb.Message.MessageID)
		return
	}

	// admin:user:<chatID> — show user detail
	if strings.HasPrefix(data, "admin:user:") {
		targetStr := strings.TrimPrefix(data, "admin:user:")
		targetChatID, err := strconv.ParseInt(targetStr, 10, 64)
		if err != nil {
			_ = notifier.AnswerCallback(cb.ID, "Invalid user ID")
			return
		}
		_ = notifier.AnswerCallback(cb.ID, "")
		sendAdminUserDetail(store, notifier, chatID, targetChatID, cb.Message.MessageID)
		return
	}

	// admin:msg:<chatID> — enter "message user" mode
	if strings.HasPrefix(data, "admin:msg:") {
		targetStr := strings.TrimPrefix(data, "admin:msg:")
		targetChatID, err := strconv.ParseInt(targetStr, 10, 64)
		if err != nil {
			_ = notifier.AnswerCallback(cb.ID, "Invalid user ID")
			return
		}
		adminMsgTarget.Store(targetChatID)
		_ = notifier.AnswerCallback(cb.ID, "")

		// Look up name for the prompt
		prefs, _ := store.GetPrefs(targetChatID)
		targetName := prefs.FirstName
		if targetName == "" {
			targetName = fmt.Sprintf("%d", targetChatID)
		}

		_ = notifier.SendKeyboard(chatID,
			fmt.Sprintf("✉️ <b>Send a message to %s</b>\n\nType your message below. It will be sent as-is (HTML supported).\n\nTap Cancel to abort.", targetName),
			[][]telegram.InlineButton{
				{{Text: "❌ Cancel", CallbackData: "admin:msgcancel"}},
			},
		)
		return
	}

	// admin:msgcancel — cancel "message user" mode
	if data == "admin:msgcancel" {
		adminMsgTarget.Store(0)
		_ = notifier.AnswerCallback(cb.ID, "Cancelled")
		_ = notifier.EditMessage(chatID, cb.Message.MessageID,
			"✉️ Message sending cancelled.", nil)
		return
	}

	_ = notifier.AnswerCallback(cb.ID, "")
}

// toggleIcon returns a check or cross for a boolean toggle.
func toggleIcon(enabled bool) string {
	if enabled {
		return "✅"
	}
	return "❌"
}

// handleAdminMsgSend sends the admin's text to the target user and clears the
// message-mode state. Called from the main router when isMaintainer and
// adminMsgTarget is non-zero.
func handleAdminMsgSend(notifier telegram.Notifier, adminChatID int64, text string) {
	targetChatID := adminMsgTarget.Load()
	adminMsgTarget.Store(0)

	if targetChatID == 0 {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		_ = notifier.Send(adminChatID, "⚠️ Empty message — nothing sent.")
		return
	}

	log.Printf("✉️  [ADMIN] Sending message from admin %d to user %d.", adminChatID, targetChatID)
	if err := notifier.Send(targetChatID, text); err != nil {
		log.Printf("❌ [ADMIN] Failed to send message to %d: %v", targetChatID, err)
		_ = notifier.Send(adminChatID, fmt.Sprintf("❌ Failed to deliver message to <code>%d</code>: %v", targetChatID, err))
		return
	}
	_ = notifier.Send(adminChatID, fmt.Sprintf("✅ Message delivered to <code>%d</code>.", targetChatID))
}
