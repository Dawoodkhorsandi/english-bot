package main

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Store — bookmark CRUD
// ---------------------------------------------------------------------------

func TestBookmarkCRUD(t *testing.T) {
	store := testStoreHelper(t)
	const chatID = 42

	// Seed a vocabulary word so the bookmark has something to reference.
	_ = store.RecordSentVocab(chatID, "resilient")
	_ = store.AddToPool(kindWord, levelIntermediate, "resilient", "able to recover", "card text")

	// Initially not bookmarked.
	if store.IsBookmarked(chatID, "resilient") {
		t.Fatal("word should not be bookmarked initially")
	}
	if got := store.BookmarkCount(chatID); got != 0 {
		t.Fatalf("BookmarkCount = %d, want 0", got)
	}

	// Add bookmark.
	if err := store.AddBookmark(chatID, "resilient"); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	if !store.IsBookmarked(chatID, "resilient") {
		t.Fatal("word should be bookmarked after AddBookmark")
	}
	if got := store.BookmarkCount(chatID); got != 1 {
		t.Fatalf("BookmarkCount = %d, want 1", got)
	}

	// Idempotent add.
	if err := store.AddBookmark(chatID, "resilient"); err != nil {
		t.Fatalf("AddBookmark (idempotent): %v", err)
	}
	if got := store.BookmarkCount(chatID); got != 1 {
		t.Fatalf("BookmarkCount after dupe = %d, want 1", got)
	}

	// Remove bookmark.
	if err := store.RemoveBookmark(chatID, "resilient"); err != nil {
		t.Fatalf("RemoveBookmark: %v", err)
	}
	if store.IsBookmarked(chatID, "resilient") {
		t.Fatal("word should not be bookmarked after removal")
	}
	if got := store.BookmarkCount(chatID); got != 0 {
		t.Fatalf("BookmarkCount after removal = %d, want 0", got)
	}

	// Remove non-existent is safe.
	if err := store.RemoveBookmark(chatID, "nonexistent"); err != nil {
		t.Fatalf("RemoveBookmark (nonexistent): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Store — LearnedWords + LearnedWordsCount
// ---------------------------------------------------------------------------

func TestLearnedWords(t *testing.T) {
	store := testStoreHelper(t)
	const chatID = 42

	// Seed several words.
	words := []struct{ term, meaning string }{
		{"apple", "a fruit"},
		{"brave", "showing courage"},
		{"crisp", "firm and crunchy"},
		{"dwell", "to live in a place"},
		{"eager", "keen or enthusiastic"},
	}
	for _, w := range words {
		_ = store.RecordSentVocab(chatID, w.term)
		_ = store.AddToPool(kindWord, levelIntermediate, w.term, w.meaning, "card:"+w.term)
	}
	// Seed SRS for one word so it shows as "learning".
	_ = store.SeedReview(chatID, "apple", time.Now())
	// Mark one word as bookmarked.
	_ = store.AddBookmark(chatID, "brave")

	// Total count.
	if got := store.LearnedWordsCount(chatID, false); got != 5 {
		t.Fatalf("LearnedWordsCount(all) = %d, want 5", got)
	}
	// Bookmarks-only count.
	if got := store.LearnedWordsCount(chatID, true); got != 1 {
		t.Fatalf("LearnedWordsCount(bookmarks) = %d, want 1", got)
	}

	// Fetch all words.
	all, err := store.LearnedWords(chatID, 0, 10, false)
	if err != nil {
		t.Fatalf("LearnedWords: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("LearnedWords returned %d, want 5", len(all))
	}

	// Check that meanings are populated.
	foundApple := false
	for _, w := range all {
		if w.Term == "apple" {
			foundApple = true
			if w.Meaning != "a fruit" {
				t.Errorf("apple meaning = %q, want %q", w.Meaning, "a fruit")
			}
		}
		if w.Term == "brave" && !w.Bookmarked {
			t.Error("brave should be bookmarked")
		}
	}
	if !foundApple {
		t.Error("apple not found in LearnedWords")
	}

	// Fetch bookmarks only.
	bm, err := store.LearnedWords(chatID, 0, 10, true)
	if err != nil {
		t.Fatalf("LearnedWords(bookmarks): %v", err)
	}
	if len(bm) != 1 {
		t.Fatalf("LearnedWords(bookmarks) returned %d, want 1", len(bm))
	}
	if bm[0].Term != "brave" {
		t.Errorf("bookmarked word = %q, want %q", bm[0].Term, "brave")
	}

	// Pagination: fetch page 2 with limit 3.
	page2, err := store.LearnedWords(chatID, 3, 3, false)
	if err != nil {
		t.Fatalf("LearnedWords(page2): %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2 returned %d, want 2", len(page2))
	}
}

// ---------------------------------------------------------------------------
// formatMyWordsPage
// ---------------------------------------------------------------------------

func TestFormatMyWordsPage(t *testing.T) {
	words := []LearnedWord{
		{Term: "resilient", Meaning: "able to recover quickly", Mastery: "mastered", Bookmarked: true},
		{Term: "tedious", Meaning: "tiresome or monotonous", Mastery: "learning", Bookmarked: false},
		{Term: "vivid", Meaning: "", Mastery: "new", Bookmarked: false},
	}

	t.Run("all words page", func(t *testing.T) {
		text := formatMyWordsPage(words, 1, 2, 12, false)
		if !strings.Contains(text, "Your Words") {
			t.Error("should contain 'Your Words' header")
		}
		if !strings.Contains(text, "Page 1/2") {
			t.Error("should show page indicator")
		}
		if !strings.Contains(text, "12 learned") {
			t.Error("should show total count")
		}
		if !strings.Contains(text, "⭐ ") {
			t.Error("bookmarked word should have ⭐ prefix")
		}
		if !strings.Contains(text, "✅") {
			t.Error("mastered word should show ✅")
		}
		if !strings.Contains(text, "📖") {
			t.Error("learning word should show 📖")
		}
		if !strings.Contains(text, "🆕") {
			t.Error("new word should show 🆕")
		}
		if !strings.Contains(text, "(no definition)") {
			t.Error("word without meaning should show (no definition)")
		}
	})

	t.Run("bookmarks page", func(t *testing.T) {
		text := formatMyWordsPage(words, 1, 1, 3, true)
		if !strings.Contains(text, "Your Bookmarks") {
			t.Error("should contain 'Your Bookmarks' header")
		}
	})

	t.Run("empty page", func(t *testing.T) {
		text := formatMyWordsPage(nil, 1, 1, 0, false)
		if !strings.Contains(text, "No words learned yet") {
			t.Error("empty all-words should show guidance message")
		}
		if strings.Contains(text, "Page 1/1") {
			t.Error("empty page should not show misleading 'Page 1/1'")
		}
	})

	t.Run("empty bookmarks", func(t *testing.T) {
		text := formatMyWordsPage(nil, 1, 1, 0, true)
		if !strings.Contains(text, "No bookmarks yet") {
			t.Error("empty bookmarks should show guidance message")
		}
		if strings.Contains(text, "Page 1/1") {
			t.Error("empty bookmarks should not show misleading 'Page 1/1'")
		}
	})
}

// ---------------------------------------------------------------------------
// myWordsKeyboard
// ---------------------------------------------------------------------------

func TestMyWordsKeyboard(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		kb := myWordsKeyboard(1, 1, false)
		if kb != nil {
			t.Error("single page should return nil keyboard")
		}
	})

	t.Run("first page", func(t *testing.T) {
		kb := myWordsKeyboard(1, 3, false)
		if len(kb) != 1 {
			t.Fatalf("expected 1 row, got %d", len(kb))
		}
		// Should have indicator + Next (no Prev on page 1).
		if len(kb[0]) != 2 {
			t.Fatalf("first page should have 2 buttons, got %d", len(kb[0]))
		}
		if kb[0][1].CallbackData != "mywords:2" {
			t.Errorf("Next button data = %q, want mywords:2", kb[0][1].CallbackData)
		}
	})

	t.Run("middle page", func(t *testing.T) {
		kb := myWordsKeyboard(2, 3, false)
		if len(kb[0]) != 3 {
			t.Fatalf("middle page should have 3 buttons, got %d", len(kb[0]))
		}
		if kb[0][0].CallbackData != "mywords:1" {
			t.Errorf("Prev button data = %q, want mywords:1", kb[0][0].CallbackData)
		}
		if kb[0][2].CallbackData != "mywords:3" {
			t.Errorf("Next button data = %q, want mywords:3", kb[0][2].CallbackData)
		}
	})

	t.Run("last page", func(t *testing.T) {
		kb := myWordsKeyboard(3, 3, false)
		if len(kb[0]) != 2 {
			t.Fatalf("last page should have 2 buttons, got %d", len(kb[0]))
		}
		if kb[0][0].CallbackData != "mywords:2" {
			t.Errorf("Prev button data = %q, want mywords:2", kb[0][0].CallbackData)
		}
	})

	t.Run("bookmarks prefix", func(t *testing.T) {
		kb := myWordsKeyboard(1, 2, true)
		if !strings.HasPrefix(kb[0][1].CallbackData, "mybm:") {
			t.Errorf("bookmark keyboard should use mybm: prefix, got %q", kb[0][1].CallbackData)
		}
	})
}

// ---------------------------------------------------------------------------
// bookmarkButton
// ---------------------------------------------------------------------------

func TestBookmarkButton(t *testing.T) {
	t.Run("not bookmarked", func(t *testing.T) {
		kb := bookmarkButton("apple", false)
		if len(kb) != 1 || len(kb[0]) != 1 {
			t.Fatal("expected 1 row, 1 button")
		}
		if kb[0][0].CallbackData != "bookmark:add:apple" {
			t.Errorf("callback = %q, want bookmark:add:apple", kb[0][0].CallbackData)
		}
		if !strings.Contains(kb[0][0].Text, "Bookmark") {
			t.Error("button text should say Bookmark")
		}
	})

	t.Run("bookmarked", func(t *testing.T) {
		kb := bookmarkButton("apple", true)
		if kb[0][0].CallbackData != "bookmark:rm:apple" {
			t.Errorf("callback = %q, want bookmark:rm:apple", kb[0][0].CallbackData)
		}
		if !strings.Contains(kb[0][0].Text, "Bookmarked") {
			t.Error("button text should say Bookmarked")
		}
	})
}

// ---------------------------------------------------------------------------
// handleMyWords (integration-ish)
// ---------------------------------------------------------------------------

func TestHandleMyWords(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	const chatID int64 = 42

	// No words yet.
	handleMyWords(store, mock, chatID, nil)
	if mock.sentCount() == 0 {
		t.Fatal("expected a message")
	}
	if !strings.Contains(mock.lastSentText(), "No words learned yet") {
		t.Errorf("empty state should say 'No words learned yet', got %q", mock.lastSentText())
	}
}

// ---------------------------------------------------------------------------
// handleBookmarkCommand (integration-ish)
// ---------------------------------------------------------------------------

func TestHandleBookmarkCommand(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	const chatID int64 = 42

	// Bookmark a word we haven't learned.
	handleBookmarkCommand(store, mock, chatID, []string{"unknown"})
	if !strings.Contains(mock.lastSentText(), "haven't learned") {
		t.Errorf("should reject unknown word, got %q", mock.lastSentText())
	}

	// Learn a word, then bookmark it.
	_ = store.RecordSentVocab(chatID, "vivid")
	handleBookmarkCommand(store, mock, chatID, []string{"vivid"})
	if !strings.Contains(mock.lastSentText(), "Bookmarked") {
		t.Errorf("should confirm bookmark, got %q", mock.lastSentText())
	}
	if !store.IsBookmarked(chatID, "vivid") {
		t.Error("word should be bookmarked after command")
	}

	// Toggle off.
	handleBookmarkCommand(store, mock, chatID, []string{"vivid"})
	if !strings.Contains(mock.lastSentText(), "removed") {
		t.Errorf("should confirm removal, got %q", mock.lastSentText())
	}
	if store.IsBookmarked(chatID, "vivid") {
		t.Error("word should be un-bookmarked after toggle")
	}
}

// ---------------------------------------------------------------------------
// masteryIcon
// ---------------------------------------------------------------------------

func TestMasteryIcon(t *testing.T) {
	tests := []struct {
		mastery string
		want    string
	}{
		{"mastered", "✅"},
		{"learning", "📖"},
		{"new", "🆕"},
		{"", "🆕"},
	}
	for _, tt := range tests {
		if got := masteryIcon(tt.mastery); got != tt.want {
			t.Errorf("masteryIcon(%q) = %q, want %q", tt.mastery, got, tt.want)
		}
	}
}
