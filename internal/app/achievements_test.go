package app

import (
	"testing"
	"time"
)

func TestComputeAchievements_Unlocked(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(1001)

	// Seed data: 55 words, 55 drills, 5 idioms, 5 collocations, 5 stories, 5 tips,
	// 2 quiz correct / 2 total, 5 active days.
	for i := 0; i < 55; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, ?)", chatID, "word"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	for i := 0; i < 55; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_words (chat_id, word) VALUES (?, ?)", chatID, "drill"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	for i := 0; i < 5; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_idioms (chat_id, word) VALUES (?, ?)", chatID, "idiom"+string(rune('1'+i)))
	}
	for i := 0; i < 5; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_collocations (chat_id, word) VALUES (?, ?)", chatID, "col"+string(rune('1'+i)))
	}
	for i := 0; i < 5; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_stories (chat_id, word) VALUES (?, ?)", chatID, "story"+string(rune('1'+i)))
	}
	for i := 0; i < 5; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_tips (chat_id, word) VALUES (?, ?)", chatID, "tip"+string(rune('1'+i)))
	}
	_, _ = store.db.Exec("INSERT INTO quiz_results (chat_id, word, correct) VALUES (?, 'w1', 1)", chatID)
	_, _ = store.db.Exec("INSERT INTO quiz_results (chat_id, word, correct) VALUES (?, 'w2', 1)", chatID)
	for d := 0; d < 5; d++ {
		day := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
		_, _ = store.db.Exec("INSERT OR IGNORE INTO activity_log (chat_id, day, cnt) VALUES (?, ?, 3)", chatID, day)
	}

	stats, err := store.UserStats(chatID)
	if err != nil {
		t.Fatal(err)
	}

	achs := store.ComputeAchievements(chatID, stats)

	// Verify known unlocks
	unlocked := map[string]bool{}
	for _, a := range achs {
		unlocked[a.ID] = a.Unlocked
	}

	// Should be unlocked
	for _, id := range []string{
		"first_steps", "first_drill", "first_quiz", "first_idiom", "first_story",
		"word_collector", "vocab_master",
		"grammar_guru",
		"idiom_explorer", "collocation_king", "bookworm", "wise_owl",
		"all_rounder",
	} {
		if !unlocked[id] {
			t.Errorf("expected achievement %q to be unlocked", id)
		}
	}

	// Should NOT be unlocked (threshold not met)
	for _, id := range []string{
		"word_wizard",    // needs 100 words
		"grammar_legend", // needs 200 drills
		"unstoppable",    // needs 30-day streak
	} {
		if unlocked[id] {
			t.Errorf("expected achievement %q to be locked", id)
		}
	}
}

func TestComputeAchievements_FreshUser(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(2001)

	stats, _ := store.UserStats(chatID)
	achs := store.ComputeAchievements(chatID, stats)

	unlocked := 0
	for _, a := range achs {
		if a.Unlocked {
			unlocked++
		}
	}
	// A fresh user should have zero unlocked achievements.
	if unlocked != 0 {
		t.Errorf("fresh user should have 0 unlocked achievements, got %d", unlocked)
	}
}

func TestAchievementStats(t *testing.T) {
	achs := []Achievement{
		{Unlocked: true},
		{Unlocked: false},
		{Unlocked: true},
	}
	unlocked, total := AchievementStats(achs)
	if unlocked != 2 || total != 3 {
		t.Errorf("got (%d, %d), want (2, 3)", unlocked, total)
	}
}

func TestComputeAchievements_ProgressCapped(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(3001)

	// Insert 200 words — beyond word_wizard (100) target.
	for i := 0; i < 200; i++ {
		_, _ = store.db.Exec("INSERT OR IGNORE INTO sent_vocab (chat_id, word) VALUES (?, ?)", chatID, "w"+string(rune(i)))
	}

	stats, _ := store.UserStats(chatID)
	achs := store.ComputeAchievements(chatID, stats)

	for _, a := range achs {
		if a.Progress > a.Target {
			t.Errorf("achievement %q progress %d exceeds target %d", a.ID, a.Progress, a.Target)
		}
	}
}
