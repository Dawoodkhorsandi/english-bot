package app

import "time"

// Achievement describes one unlockable badge.
type Achievement struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Unlocked    bool   `json:"unlocked"`
	Progress    int    `json:"progress"`
	Target      int    `json:"target"`
}

type achievementRule struct {
	id, name, icon, desc, cat string
	target                    int
	check                     func(st UserStats, extra achievementExtra) (int, bool)
}

type achievementExtra struct {
	kudosReceived    int
	memberDays       int
	contentTypes     int // distinct content types used
	bookmarksCount   int
	deckCardsStudied int
	bestDayActivity  int // max activity in a single day
	levelsUsed       int // distinct difficulty levels seen in vocab
}

var achievementDefs = []achievementRule{
	// Getting Started
	{id: "first_steps", name: "First Steps", icon: "🌱", desc: "Learn your first word", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 1 }},
	{id: "first_drill", name: "First Drill", icon: "🔤", desc: "Complete your first grammar drill", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Verbs, st.Verbs >= 1 }},
	{id: "first_quiz", name: "First Quiz", icon: "❓", desc: "Answer your first quiz question", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.QuizAnswered, st.QuizAnswered >= 1 }},
	{id: "first_idiom", name: "First Idiom", icon: "💬", desc: "Learn your first idiom", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Idioms, st.Idioms >= 1 }},
	{id: "first_story", name: "First Story", icon: "📖", desc: "Read your first mini story", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Stories, st.Stories >= 1 }},

	// Vocabulary
	{id: "word_collector", name: "Word Collector", icon: "📘", desc: "Learn 10 words", cat: "Vocabulary", target: 10,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 10 }},
	{id: "vocab_master", name: "Vocabulary Master", icon: "📚", desc: "Learn 50 words", cat: "Vocabulary", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 50 }},
	{id: "word_wizard", name: "Word Wizard", icon: "🧙", desc: "Learn 100 words", cat: "Vocabulary", target: 100,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 100 }},
	{id: "wordsmith", name: "Wordsmith", icon: "👑", desc: "Learn 500 words", cat: "Vocabulary", target: 500,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 500 }},
	{id: "vocabulary_legend", name: "Vocabulary Legend", icon: "💎", desc: "Learn 1000 words", cat: "Vocabulary", target: 1000,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 1000 }},

	// Grammar
	{id: "grammar_guru", name: "Grammar Guru", icon: "🏅", desc: "Complete 50 drills", cat: "Grammar", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Verbs, st.Verbs >= 50 }},
	{id: "grammar_legend", name: "Grammar Legend", icon: "🏆", desc: "Complete 200 drills", cat: "Grammar", target: 200,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Verbs, st.Verbs >= 200 }},

	// Streaks
	{id: "fresh_streak", name: "Fresh Streak", icon: "🔥", desc: "3-day streak", cat: "Streaks", target: 3,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.CurrentStreak, st.CurrentStreak >= 3 }},
	{id: "on_fire", name: "On Fire", icon: "🔥", desc: "7-day streak", cat: "Streaks", target: 7,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.CurrentStreak, st.CurrentStreak >= 7 }},
	{id: "unstoppable", name: "Unstoppable", icon: "🔥", desc: "30-day streak", cat: "Streaks", target: 30,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.CurrentStreak, st.CurrentStreak >= 30 }},
	{id: "legendary", name: "Legendary", icon: "⭐", desc: "60-day streak", cat: "Streaks", target: 60,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.CurrentStreak, st.CurrentStreak >= 60 }},
	{id: "century_streak", name: "Century Streak", icon: "💯", desc: "100-day streak", cat: "Streaks", target: 100,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.CurrentStreak, st.CurrentStreak >= 100 }},

	// Quiz
	{id: "quiz_pro", name: "Quiz Pro", icon: "🧠", desc: "80%+ quiz accuracy (min 10)", cat: "Quiz", target: 10,
		check: func(st UserStats, _ achievementExtra) (int, bool) {
			if st.QuizAnswered < 10 {
				return st.QuizAnswered, false
			}
			return st.QuizAnswered, st.QuizCorrect*100/st.QuizAnswered >= 80
		}},
	{id: "quiz_master", name: "Quiz Master", icon: "🎓", desc: "95%+ quiz accuracy (min 20)", cat: "Quiz", target: 20,
		check: func(st UserStats, _ achievementExtra) (int, bool) {
			if st.QuizAnswered < 20 {
				return st.QuizAnswered, false
			}
			return st.QuizAnswered, st.QuizCorrect*100/st.QuizAnswered >= 95
		}},
	{id: "quiz_warrior", name: "Quiz Warrior", icon: "⚔️", desc: "Answer 100 quiz questions", cat: "Quiz", target: 100,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.QuizAnswered, st.QuizAnswered >= 100 }},
	{id: "quiz_perfectionist", name: "Quiz Perfectionist", icon: "💎", desc: "100% accuracy with 50+ answers", cat: "Quiz", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) {
			if st.QuizAnswered < 50 {
				return st.QuizAnswered, false
			}
			return st.QuizAnswered, st.QuizCorrect == st.QuizAnswered
		}},

	// SRS
	{id: "srs_starter", name: "SRS Starter", icon: "🧠", desc: "Complete your first review", cat: "SRS", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Words > 0 }},
	{id: "memory_champion", name: "Memory Champion", icon: "🏅", desc: "Master 10 words", cat: "SRS", target: 10,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Mastered >= 10 }},
	{id: "memory_grandmaster", name: "Memory Grandmaster", icon: "👑", desc: "Master 50 words", cat: "SRS", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Mastered >= 50 }},

	// Library
	{id: "idiom_explorer", name: "Idiom Explorer", icon: "💬", desc: "Learn 5 idioms", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Idioms, st.Idioms >= 5 }},
	{id: "idiom_collector", name: "Idiom Collector", icon: "📝", desc: "Learn 20 idioms", cat: "Library", target: 20,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Idioms, st.Idioms >= 20 }},
	{id: "collocation_king", name: "Collocation King", icon: "🔗", desc: "Learn 5 collocations", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Collocations, st.Collocations >= 5 }},
	{id: "bookworm", name: "Bookworm", icon: "📖", desc: "Read 5 stories", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Stories, st.Stories >= 5 }},
	{id: "prolific_reader", name: "Prolific Reader", icon: "📚", desc: "Read 20 stories", cat: "Library", target: 20,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Stories, st.Stories >= 20 }},
	{id: "wise_owl", name: "Wise Owl", icon: "💡", desc: "Read 5 tips", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Tips, st.Tips >= 5 }},

	// Social
	{id: "social_butterfly", name: "Social Butterfly", icon: "🌟", desc: "Receive kudos from someone", cat: "Social", target: 1,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.kudosReceived, ex.kudosReceived >= 1 }},
	{id: "popular", name: "Popular", icon: "🎉", desc: "Receive 5 kudos", cat: "Social", target: 5,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.kudosReceived, ex.kudosReceived >= 5 }},

	// Dedication
	{id: "dedicated_learner", name: "Dedicated Learner", icon: "📅", desc: "30 active days", cat: "Dedication", target: 30,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.ActiveDays, st.ActiveDays >= 30 }},
	{id: "veteran", name: "Veteran", icon: "🗓️", desc: "Member for 90 days", cat: "Dedication", target: 90,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.memberDays, ex.memberDays >= 90 }},
	{id: "century_active", name: "Century Active", icon: "🗓️", desc: "100 active days", cat: "Dedication", target: 100,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.ActiveDays, st.ActiveDays >= 100 }},

	// Mastery
	{id: "all_rounder", name: "All-rounder", icon: "🎓", desc: "Try every content type", cat: "Mastery", target: 7,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.contentTypes, ex.contentTypes >= 7 }},
	{id: "speed_learner", name: "Speed Learner", icon: "⚡", desc: "Learn 10+ items in one day", cat: "Mastery", target: 10,
		check: func(_ UserStats, ex achievementExtra) (int, bool) {
			return ex.bestDayActivity, ex.bestDayActivity >= 10
		}},
	{id: "bookworm_supreme", name: "Bookworm Supreme", icon: "⭐", desc: "Bookmark 25 words", cat: "Mastery", target: 25,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.bookmarksCount, ex.bookmarksCount >= 25 }},
	{id: "deck_explorer", name: "Deck Explorer", icon: "📚", desc: "Study 50 deck cards", cat: "Mastery", target: 50,
		check: func(_ UserStats, ex achievementExtra) (int, bool) {
			return ex.deckCardsStudied, ex.deckCardsStudied >= 50
		}},
}

// ComputeAchievements returns all achievements with unlock status for a user.
func (s *Store) ComputeAchievements(chatID int64, stats UserStats) []Achievement {
	extra := s.achievementExtras(chatID, stats)
	out := make([]Achievement, 0, len(achievementDefs))
	for _, def := range achievementDefs {
		progress, unlocked := def.check(stats, extra)
		if progress > def.target {
			progress = def.target
		}
		out = append(out, Achievement{
			ID:          def.id,
			Name:        def.name,
			Icon:        def.icon,
			Description: def.desc,
			Category:    def.cat,
			Unlocked:    unlocked,
			Progress:    progress,
			Target:      def.target,
		})
	}
	return out
}

func (s *Store) achievementExtras(chatID int64, stats UserStats) achievementExtra {
	ex := achievementExtra{}

	// Kudos received
	k, _ := s.kudosCount(chatID)
	ex.kudosReceived = k

	// Member days
	if stats.HasMemberSince {
		ex.memberDays = int(time.Since(stats.MemberSince).Hours() / 24)
	}

	// Content types: count distinct non-zero sent_* categories
	n := 0
	if stats.Words > 0 {
		n++
	}
	if stats.Verbs > 0 {
		n++
	}
	if stats.Idioms > 0 {
		n++
	}
	if stats.Collocations > 0 {
		n++
	}
	if stats.Stories > 0 {
		n++
	}
	if stats.Tips > 0 {
		n++
	}
	if stats.QuizAnswered > 0 {
		n++
	}
	ex.contentTypes = n

	// Bookmarks count
	_ = s.db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE chat_id = ?", chatID).Scan(&ex.bookmarksCount)

	// Deck cards studied (from leitner_progress)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM leitner_progress WHERE chat_id = ?", chatID).Scan(&ex.deckCardsStudied)

	// Best day activity (max items in a single day)
	if stats.ActiveDays > 0 {
		best := 0
		for _, cnt := range stats.ActivityCounts {
			if cnt > best {
				best = cnt
			}
		}
		ex.bestDayActivity = best
	}

	// Levels used (distinct levels in content_pool for user's vocab)
	_ = s.db.QueryRow(`
		SELECT COUNT(DISTINCT COALESCE(c.level, 'unknown'))
		FROM sent_vocab v
		LEFT JOIN content_pool c ON c.term = v.word AND c.kind = 'word'
		WHERE v.chat_id = ?`, chatID).Scan(&ex.levelsUsed)

	return ex
}

// AchievementStats returns the total unlocked count and total count.
func AchievementStats(achs []Achievement) (unlocked, total int) {
	for _, a := range achs {
		if a.Unlocked {
			unlocked++
		}
	}
	return unlocked, len(achs)
}
