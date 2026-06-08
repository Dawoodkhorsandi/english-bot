package main

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// /mywords & /bookmark — browse learned words and bookmark favourites
//
// /mywords          — paginated list of all vocabulary words learned
// /mywords bookmarks — show only bookmarked words
// /bookmark <word>  — toggle bookmark on a word
// Inline button     — ⭐ on every word card to bookmark/un-bookmark
// ---------------------------------------------------------------------------

const myWordsPageSize = 8

// LearnedWord is one row in the /mywords listing.
type LearnedWord struct {
	Term       string
	Meaning    string
	Mastery    string // "new", "learning", "mastered"
	Bookmarked bool
}

// masteryIcon returns a display icon for a mastery level.
func masteryIcon(mastery string) string {
	switch mastery {
	case "mastered":
		return "✅"
	case "learning":
		return "📖"
	default:
		return "🆕"
	}
}

// ---------------------------------------------------------------------------
// Store methods — bookmarks
// ---------------------------------------------------------------------------

// AddBookmark idempotently bookmarks a word for a user.
func (s *Store) AddBookmark(chatID int64, word string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO bookmarks (chat_id, word) VALUES (?, ?)",
		chatID, strings.ToLower(strings.TrimSpace(word)),
	)
	return err
}

// RemoveBookmark removes a bookmark.
func (s *Store) RemoveBookmark(chatID int64, word string) error {
	_, err := s.db.Exec(
		"DELETE FROM bookmarks WHERE chat_id = ? AND word = ?",
		chatID, strings.ToLower(strings.TrimSpace(word)),
	)
	return err
}

// IsBookmarked reports whether a word is bookmarked by a user.
func (s *Store) IsBookmarked(chatID int64, word string) bool {
	var count int
	_ = s.db.QueryRow(
		"SELECT COUNT(*) FROM bookmarks WHERE chat_id = ? AND word = ?",
		chatID, strings.ToLower(strings.TrimSpace(word)),
	).Scan(&count)
	return count > 0
}

// BookmarkCount returns how many words a user has bookmarked.
func (s *Store) BookmarkCount(chatID int64) int {
	var count int
	_ = s.db.QueryRow(
		"SELECT COUNT(*) FROM bookmarks WHERE chat_id = ?",
		chatID,
	).Scan(&count)
	return count
}

// ---------------------------------------------------------------------------
// Store methods — learned words listing
// ---------------------------------------------------------------------------

// LearnedWordsCount returns the total number of vocabulary words a user has
// received. If bookmarksOnly is true, counts only bookmarked words.
func (s *Store) LearnedWordsCount(chatID int64, bookmarksOnly bool) int {
	var count int
	if bookmarksOnly {
		_ = s.db.QueryRow(`
			SELECT COUNT(*) FROM sent_vocab sv
			JOIN bookmarks bk ON bk.chat_id = sv.chat_id AND bk.word = sv.word
			WHERE sv.chat_id = ?`, chatID,
		).Scan(&count)
	} else {
		_ = s.db.QueryRow(
			"SELECT COUNT(*) FROM sent_vocab WHERE chat_id = ?", chatID,
		).Scan(&count)
	}
	return count
}

// LearnedWords returns a page of vocabulary words the user has received,
// enriched with meaning (from content_pool), mastery level (from
// review_schedule), and bookmark status. Ordered by most recently learned.
func (s *Store) LearnedWords(chatID int64, offset, limit int, bookmarksOnly bool) ([]LearnedWord, error) {
	var query string
	if bookmarksOnly {
		query = `
			SELECT sv.word,
			       COALESCE(cp.meaning, ''),
			       COALESCE(rs.interval_days, 0),
			       COALESCE(rs.reps, 0),
			       CASE WHEN bk.word IS NOT NULL THEN 1 ELSE 0 END
			FROM sent_vocab sv
			JOIN bookmarks bk ON bk.chat_id = sv.chat_id AND bk.word = sv.word
			LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = sv.word
			LEFT JOIN review_schedule rs ON rs.chat_id = sv.chat_id AND rs.word = sv.word
			WHERE sv.chat_id = ?
			ORDER BY sv.sent_at DESC
			LIMIT ? OFFSET ?`
	} else {
		query = `
			SELECT sv.word,
			       COALESCE(cp.meaning, ''),
			       COALESCE(rs.interval_days, 0),
			       COALESCE(rs.reps, 0),
			       CASE WHEN bk.word IS NOT NULL THEN 1 ELSE 0 END
			FROM sent_vocab sv
			LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = sv.word
			LEFT JOIN review_schedule rs ON rs.chat_id = sv.chat_id AND rs.word = sv.word
			LEFT JOIN bookmarks bk ON bk.chat_id = sv.chat_id AND bk.word = sv.word
			WHERE sv.chat_id = ?
			ORDER BY sv.sent_at DESC
			LIMIT ? OFFSET ?`
	}

	rows, err := s.db.Query(query, chatID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []LearnedWord
	for rows.Next() {
		var (
			word        string
			meaning     string
			intervalD   int
			reps        int
			bookmarked  int
		)
		if err := rows.Scan(&word, &meaning, &intervalD, &reps, &bookmarked); err != nil {
			return nil, err
		}
		mastery := "new"
		if intervalD >= srsMasteredIntervalDays {
			mastery = "mastered"
		} else if reps > 0 {
			mastery = "learning"
		}
		items = append(items, LearnedWord{
			Term:       word,
			Meaning:    meaning,
			Mastery:    mastery,
			Bookmarked: bookmarked == 1,
		})
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

// formatMyWordsPage renders one page of the /mywords listing.
func formatMyWordsPage(words []LearnedWord, page, totalPages, totalWords int, bookmarksOnly bool) string {
	var b strings.Builder

	if totalWords == 0 {
		// Clean header without misleading "Page 1/1" when there's nothing.
		if bookmarksOnly {
			b.WriteString("⭐ <b>Your Bookmarks</b>\n\n")
		} else {
			b.WriteString("📘 <b>Your Words</b>\n\n")
		}
	} else if bookmarksOnly {
		fmt.Fprintf(&b, "⭐ <b>Your Bookmarks</b> (Page %d/%d) — %d bookmarked\n\n", page, totalPages, totalWords)
	} else {
		fmt.Fprintf(&b, "📘 <b>Your Words</b> (Page %d/%d) — %d learned\n\n", page, totalPages, totalWords)
	}

	if len(words) == 0 {
		if bookmarksOnly {
			b.WriteString("No bookmarks yet. Use /bookmark <word> or tap ⭐ on a word card to save one!")
		} else {
			b.WriteString("No words learned yet. Use /word to get started!")
		}
		return b.String()
	}

	for i, w := range words {
		num := (page-1)*myWordsPageSize + i + 1
		prefix := ""
		if w.Bookmarked {
			prefix = "⭐ "
		}
		meaning := w.Meaning
		if len(meaning) > 50 {
			meaning = meaning[:47] + "..."
		}
		if meaning == "" {
			meaning = "(no definition)"
		}
		fmt.Fprintf(&b, "%d. %s<b>%s</b> — %s %s\n", num, prefix, w.Term, meaning, masteryIcon(w.Mastery))
	}

	b.WriteString("\n")
	b.WriteString("🆕 = new  📖 = learning  ✅ = mastered")
	return b.String()
}

// myWordsKeyboard builds the pagination inline keyboard for /mywords.
func myWordsKeyboard(page, totalPages int, bookmarksOnly bool) [][]inlineButton {
	if totalPages <= 1 {
		return nil
	}

	prefix := "mywords:"
	if bookmarksOnly {
		prefix = "mybm:"
	}

	var row []inlineButton
	if page > 1 {
		row = append(row, inlineButton{Text: "◀️ Prev", CallbackData: prefix + strconv.Itoa(page-1)})
	}
	row = append(row, inlineButton{Text: fmt.Sprintf("· %d/%d ·", page, totalPages), CallbackData: prefix + "nop"})
	if page < totalPages {
		row = append(row, inlineButton{Text: "Next ▶️", CallbackData: prefix + strconv.Itoa(page+1)})
	}

	return [][]inlineButton{row}
}

// bookmarkButton returns a single-button inline keyboard row for toggling a bookmark.
func bookmarkButton(word string, isBookmarked bool) [][]inlineButton {
	if isBookmarked {
		return [][]inlineButton{{
			{Text: "💫 Bookmarked", CallbackData: "bookmark:rm:" + word},
		}}
	}
	return [][]inlineButton{{
		{Text: "⭐ Bookmark", CallbackData: "bookmark:add:" + word},
	}}
}

// ---------------------------------------------------------------------------
// Command handlers
// ---------------------------------------------------------------------------

// handleMyWords handles /mywords [bookmarks]. Sends a paginated list.
func handleMyWords(store *Store, notifier Notifier, chatID int64, args []string) {
	bookmarksOnly := len(args) > 0 && strings.EqualFold(args[0], "bookmarks")

	total := store.LearnedWordsCount(chatID, bookmarksOnly)
	totalPages := int(math.Ceil(float64(total) / float64(myWordsPageSize)))
	if totalPages < 1 {
		totalPages = 1
	}

	words, err := store.LearnedWords(chatID, 0, myWordsPageSize, bookmarksOnly)
	if err != nil {
		log.Printf("❌ [MYWORDS] Could not fetch words for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't load your words. Please try again.")
		return
	}

	text := formatMyWordsPage(words, 1, totalPages, total, bookmarksOnly)
	kb := myWordsKeyboard(1, totalPages, bookmarksOnly)
	if kb != nil {
		_ = notifier.SendKeyboard(chatID, text, kb)
	} else {
		_ = notifier.Send(chatID, text)
	}
}

// handleMyWordsCallback handles pagination callbacks for mywords:<page> and mybm:<page>.
func handleMyWordsCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	var rest string
	var bookmarksOnly bool
	if strings.HasPrefix(cb.Data, "mybm:") {
		rest = strings.TrimPrefix(cb.Data, "mybm:")
		bookmarksOnly = true
	} else {
		rest = strings.TrimPrefix(cb.Data, "mywords:")
	}

	if rest == "nop" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	page, err := strconv.Atoi(rest)
	if err != nil || page < 1 {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}

	total := store.LearnedWordsCount(chatID, bookmarksOnly)
	totalPages := int(math.Ceil(float64(total) / float64(myWordsPageSize)))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * myWordsPageSize
	words, err := store.LearnedWords(chatID, offset, myWordsPageSize, bookmarksOnly)
	if err != nil {
		log.Printf("❌ [MYWORDS] Pagination error for chat %d: %v", chatID, err)
		_ = notifier.AnswerCallback(cb.ID, "Error loading page")
		return
	}

	text := formatMyWordsPage(words, page, totalPages, total, bookmarksOnly)
	kb := myWordsKeyboard(page, totalPages, bookmarksOnly)

	_ = notifier.AnswerCallback(cb.ID, "")
	_ = notifier.EditMessage(chatID, cb.Message.MessageID, text, kb)
}

// handleBookmarkCommand handles /bookmark <word>.
func handleBookmarkCommand(store *Store, notifier Notifier, chatID int64, args []string) {
	if len(args) == 0 {
		// No argument: show bookmarks (alias for /mywords bookmarks)
		handleMyWords(store, notifier, chatID, []string{"bookmarks"})
		return
	}

	word := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))

	// Check if the user has actually learned this word.
	vocab, _ := store.SentVocab(chatID)
	found := false
	for _, v := range vocab {
		if v == word {
			found = true
			break
		}
	}
	if !found {
		_ = notifier.Send(chatID, fmt.Sprintf("❌ You haven't learned the word <b>%s</b> yet. Learn it first with /word or by typing it!", word))
		return
	}

	// Toggle bookmark.
	if store.IsBookmarked(chatID, word) {
		if err := store.RemoveBookmark(chatID, word); err != nil {
			log.Printf("❌ [BOOKMARK] Remove failed for chat %d word %q: %v", chatID, word, err)
			_ = notifier.Send(chatID, "❌ Could not remove bookmark. Please try again.")
			return
		}
		_ = notifier.Send(chatID, fmt.Sprintf("Bookmark removed for <b>%s</b>.", word))
	} else {
		if err := store.AddBookmark(chatID, word); err != nil {
			log.Printf("❌ [BOOKMARK] Add failed for chat %d word %q: %v", chatID, word, err)
			_ = notifier.Send(chatID, "❌ Could not add bookmark. Please try again.")
			return
		}
		_ = notifier.Send(chatID, fmt.Sprintf("⭐ Bookmarked <b>%s</b>! View all with /mywords bookmarks.", word))
	}
}

// handleBookmarkCallback handles bookmark:add:<word> and bookmark:rm:<word> taps.
func handleBookmarkCallback(store *Store, notifier Notifier, cb *TelegramCallbackQuery, chatID int64) {
	rest := strings.TrimPrefix(cb.Data, "bookmark:")
	action, word, found := strings.Cut(rest, ":")
	if !found || word == "" {
		_ = notifier.AnswerCallback(cb.ID, "")
		return
	}
	word = strings.ToLower(strings.TrimSpace(word))

	switch action {
	case "add":
		if err := store.AddBookmark(chatID, word); err != nil {
			log.Printf("⚠️  [BOOKMARK] Add failed for chat %d word %q: %v", chatID, word, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not bookmark")
			return
		}
		_ = notifier.AnswerCallback(cb.ID, "⭐ Bookmarked!")
		// Update the button to show "Bookmarked".
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				cb.Message.Text, bookmarkButton(word, true))
		}
	case "rm":
		if err := store.RemoveBookmark(chatID, word); err != nil {
			log.Printf("⚠️  [BOOKMARK] Remove failed for chat %d word %q: %v", chatID, word, err)
			_ = notifier.AnswerCallback(cb.ID, "Could not remove bookmark")
			return
		}
		_ = notifier.AnswerCallback(cb.ID, "Bookmark removed")
		// Update the button to show "Bookmark".
		if cb.Message != nil {
			_ = notifier.EditMessage(chatID, cb.Message.MessageID,
				cb.Message.Text, bookmarkButton(word, false))
		}
	default:
		_ = notifier.AnswerCallback(cb.ID, "")
	}
}
