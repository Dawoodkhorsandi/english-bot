package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// /stats — personal progress summary (Change G)
// ---------------------------------------------------------------------------

// UserStats is a read-only snapshot of a user's progress, derived entirely from
// existing tables (sent_words, sent_vocab, subscribers). Quiz accuracy and
// "mastered" counts will be added alongside Changes D/E.
type UserStats struct {
	Level          string
	Paused         bool
	Verbs          int // total grammar drills practised
	Words          int // total vocabulary words learned
	Mastered       int // words moved into long-term memory (Change D)
	QuizAnswered   int // quiz questions answered (Change E)
	QuizCorrect    int // quiz questions answered correctly (Change E)
	Idioms         int // idioms of the day received
	Collocations   int // collocations of the day received
	Stories        int // mini stories received
	Tips           int // grammar tips received
	ActiveDays     int // distinct local days with any activity
	CurrentStreak  int // consecutive active days ending today/yesterday
	LongestStreak  int // best run of consecutive active days ever
	MemberSince    time.Time
	HasMemberSince bool
	ActivityDays   []string       // "2006-01-02" strings for the mini-app calendar
	ActivityCounts map[string]int // items received per "2006-01-02" day (heatmap intensity)
}

// parseStoredUTC interprets a SQLite DATETIME value (which the driver may hand
// back as a string, []byte or time.Time) as a UTC instant.
func parseStoredUTC(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case []byte:
		return parseStoredUTC(string(t))
	case string:
		for _, layout := range []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
			time.RFC3339,
		} {
			if ts, err := time.ParseInLocation(layout, t, time.UTC); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}

// UserStats assembles a progress snapshot for a chat.
func (s *Store) UserStats(chatID int64) (UserStats, error) {
	prefs, err := s.GetPrefs(chatID)
	if err != nil {
		return UserStats{}, err
	}
	st := UserStats{Level: prefs.Level, Paused: prefs.Paused}

	if err := s.db.QueryRow("SELECT COUNT(*) FROM sent_words WHERE chat_id = ?", chatID).Scan(&st.Verbs); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sent_vocab WHERE chat_id = ?", chatID).Scan(&st.Words); err != nil {
		return st, err
	}
	if mastered, err := s.MasteredCount(chatID); err == nil {
		st.Mastered = mastered
	}
	if answered, correct, err := s.QuizStats(chatID); err == nil {
		st.QuizAnswered = answered
		st.QuizCorrect = correct
	}
	st.Idioms = s.countByChat("sent_idioms", chatID)
	st.Collocations = s.countByChat("sent_collocations", chatID)
	st.Stories = s.countByChat("sent_stories", chatID)
	st.Tips = s.countByChat("sent_tips", chatID)

	var created any
	if err := s.db.QueryRow("SELECT created_at FROM subscribers WHERE chat_id = ?", chatID).Scan(&created); err == nil {
		if ts, ok := parseStoredUTC(created); ok {
			st.MemberSince = ts.In(appLocation)
			st.HasMemberSince = true
		}
	}

	counts, err := s.activityDays(chatID)
	if err != nil {
		return st, err
	}
	st.ActivityCounts = counts
	st.ActiveDays = len(counts)
	days := make(map[string]bool, len(counts))
	for d := range counts {
		days[d] = true
		st.ActivityDays = append(st.ActivityDays, d)
	}
	st.CurrentStreak, st.LongestStreak = computeStreaks(days, time.Now().In(appLocation))
	sort.Strings(st.ActivityDays)
	return st, nil
}

// countByChat returns the row count for a chat in one of the fixed per-user
// content tables. The table name is always a hardcoded constant (no injection).
func (s *Store) countByChat(table string, chatID int64) int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE chat_id = ?", chatID).Scan(&n)
	return n
}

// activityDays returns, per distinct local (appLocation) calendar date keyed by
// "2006-01-02", how many drills or words the user received that day.
func (s *Store) activityDays(chatID int64) (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT sent_at FROM sent_words WHERE chat_id = ?
		UNION ALL
		SELECT sent_at FROM sent_vocab WHERE chat_id = ?`,
		chatID, chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	days := make(map[string]int)
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if ts, ok := parseStoredUTC(raw); ok {
			days[ts.In(appLocation).Format("2006-01-02")]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fold in pull-based learning (in-app reviews, deck study, quiz answers) that
	// leaves no sent_* footprint. RecordActivity stores the day pre-formatted in
	// appLocation, so it merges directly into the per-day counts above.
	logRows, err := s.db.Query("SELECT day, cnt FROM activity_log WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer logRows.Close()
	for logRows.Next() {
		var day string
		var cnt int
		if err := logRows.Scan(&day, &cnt); err != nil {
			return nil, err
		}
		days[day] += cnt
	}
	return days, logRows.Err()
}

// RecordActivity marks today (in appLocation) as an active learning day for a
// chat, independent of content delivery. It is called from pull-based learning
// touch-points — in-app reviews, deck study, and quiz answers — that do not
// otherwise land in the sent_* history tables, so the streak reflects them.
func (s *Store) RecordActivity(chatID int64, now time.Time) error {
	day := now.In(appLocation).Format("2006-01-02")
	_, err := s.db.Exec(`
		INSERT INTO activity_log (chat_id, day, cnt) VALUES (?, ?, 1)
		ON CONFLICT(chat_id, day) DO UPDATE SET cnt = cnt + 1`,
		chatID, day,
	)
	return err
}

// computeStreaks derives the current and longest consecutive-day streaks from a
// set of active local dates. The current streak counts back from today; if there
// was no activity today it still counts when yesterday was active (so a streak
// isn't "lost" until a full day is missed).
func computeStreaks(days map[string]bool, now time.Time) (current, longest int) {
	if len(days) == 0 {
		return 0, 0
	}

	const layout = "2006-01-02"
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Current streak: pick the anchor (today or, failing that, yesterday) then
	// walk backwards while each day is present.
	anchor := today
	if !days[anchor.Format(layout)] {
		anchor = today.AddDate(0, 0, -1)
	}
	for days[anchor.Format(layout)] {
		current++
		anchor = anchor.AddDate(0, 0, -1)
	}

	// Longest streak: sort the dates and scan for the longest consecutive run.
	sorted := make([]time.Time, 0, len(days))
	for d := range days {
		if ts, err := time.ParseInLocation(layout, d, now.Location()); err == nil {
			sorted = append(sorted, ts)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	run := 0
	var prev time.Time
	for i, d := range sorted {
		if i > 0 && d.Equal(prev.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = d
	}
	return current, longest
}

// progressBar renders a Unicode progress bar of the given width.
// filled/total may exceed width; the bar is clamped to full.
func progressBar(filled, total, width int) string {
	if total <= 0 || width <= 0 {
		return strings.Repeat("░", width)
	}
	n := filled * width / total
	if n > width {
		n = width
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

// formatStats renders the /stats progress summary as an HTML message.
// firstName is the user's Telegram first name; pass "" to omit the greeting.
func formatStats(st UserStats, firstName string) string {
	const barWidth = 10

	greeting := ""
	if firstName != "" {
		greeting = fmt.Sprintf("Hey <b>%s</b>! Here's where you stand:\n\n", firstName)
	}

	flame := ""
	if st.CurrentStreak >= 3 {
		flame = " 🔥"
	}

	best := st.LongestStreak
	if best < st.CurrentStreak {
		best = st.CurrentStreak
	}
	streakBar := progressBar(st.CurrentStreak, best, barWidth)

	msg := "📊 <b>Your Progress</b>\n" + greeting + "\n" +
		fmt.Sprintf("🎯 Grammar drills: <b>%d</b>\n", st.Verbs) +
		fmt.Sprintf("📘 Vocabulary words: <b>%d</b>  (<b>%d</b> mastered)\n", st.Words, st.Mastered) +
		fmt.Sprintf("\n⚡ Streak: <b>%d day%s</b>%s\n", st.CurrentStreak, plural(st.CurrentStreak), flame) +
		fmt.Sprintf("<code>%s</code>  %d / %d best\n", streakBar, st.CurrentStreak, st.LongestStreak)

	if st.QuizAnswered > 0 {
		pct := st.QuizCorrect * 100 / st.QuizAnswered
		quizBar := progressBar(pct, 100, barWidth)
		msg += fmt.Sprintf("\n🧩 Quiz accuracy: <b>%d%%</b> (%d/%d)\n", pct, st.QuizCorrect, st.QuizAnswered) +
			fmt.Sprintf("<code>%s</code>\n", quizBar)
	}

	msg += fmt.Sprintf("\n🗓️ Active days: <b>%d</b>\n", st.ActiveDays) +
		fmt.Sprintf("🎚️ Level: <b>%s</b>\n", levelLabel(st.Level))

	if st.Paused {
		msg += "⏸️ Scheduled sends: <b>paused</b> (use /resume)\n"
	}
	if st.HasMemberSince {
		msg += fmt.Sprintf("📅 Member since: <b>%s</b>\n", st.MemberSince.Format("2 Jan 2006"))
	}

	msg += "\nKeep going — say each word aloud to lock it in! 💪"
	return msg
}

// plural returns "s" unless n == 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------------------------------------------------------------------
// Admin metrics (Change J)
// ---------------------------------------------------------------------------

// SubscriberStats returns the total, active and paused subscriber counts.
func (s *Store) SubscriberStats() (total, active, paused int) {
	_ = s.db.QueryRow("SELECT COUNT(*) FROM subscribers").Scan(&total)
	_ = s.db.QueryRow(`
		SELECT COUNT(*) FROM subscribers sub
		WHERE NOT EXISTS (
			SELECT 1 FROM user_prefs up WHERE up.chat_id = sub.chat_id AND up.paused = 1
		)
	`).Scan(&active)
	paused = total - active
	return total, active, paused
}

// TotalQuizStats returns the total answered and correct quiz counts across all users.
func (s *Store) TotalQuizStats() (answered, correct int, err error) {
	err = s.db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(correct), 0) FROM quiz_results",
	).Scan(&answered, &correct)
	return answered, correct, err
}

// TotalMasteredCount returns the total number of mastered word-user pairs across
// all users (words with interval >= srsMasteredIntervalDays).
func (s *Store) TotalMasteredCount() (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM review_schedule WHERE interval_days >= ?",
		srsMasteredIntervalDays,
	).Scan(&n)
	return n, err
}
