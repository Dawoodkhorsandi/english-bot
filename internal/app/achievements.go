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
	kudosReceived int
	memberDays    int
	contentTypes  int // distinct content types used
}

var achievementDefs = []achievementRule{
	// Getting Started
	{id: "first_steps", name: "First Steps", icon: "🌱", desc: "Learn your first word", cat: "Getting Started", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 1 }},

	// Vocabulary
	{id: "word_collector", name: "Word Collector", icon: "📘", desc: "Learn 10 words", cat: "Vocabulary", target: 10,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 10 }},
	{id: "vocab_master", name: "Vocabulary Master", icon: "📚", desc: "Learn 50 words", cat: "Vocabulary", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 50 }},
	{id: "word_wizard", name: "Word Wizard", icon: "🧙", desc: "Learn 100 words", cat: "Vocabulary", target: 100,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 100 }},
	{id: "wordsmith", name: "Wordsmith", icon: "👑", desc: "Learn 500 words", cat: "Vocabulary", target: 500,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Words, st.Words >= 500 }},

	// Grammar
	{id: "grammar_novice", name: "Grammar Novice", icon: "🎯", desc: "Complete your first drill", cat: "Grammar", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Verbs, st.Verbs >= 1 }},
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

	// Quiz
	{id: "quiz_rookie", name: "Quiz Rookie", icon: "🧩", desc: "Answer your first quiz", cat: "Quiz", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.QuizAnswered, st.QuizAnswered >= 1 }},
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

	// SRS
	{id: "srs_starter", name: "SRS Starter", icon: "🧠", desc: "Complete your first review", cat: "SRS", target: 1,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Words > 0 }}, // any word means SRS started
	{id: "memory_champion", name: "Memory Champion", icon: "🏅", desc: "Master 10 words", cat: "SRS", target: 10,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Mastered >= 10 }},
	{id: "memory_grandmaster", name: "Memory Grandmaster", icon: "👑", desc: "Master 50 words", cat: "SRS", target: 50,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Mastered, st.Mastered >= 50 }},

	// Library
	{id: "idiom_explorer", name: "Idiom Explorer", icon: "💬", desc: "Learn 5 idioms", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Idioms, st.Idioms >= 5 }},
	{id: "collocation_king", name: "Collocation King", icon: "🔗", desc: "Learn 5 collocations", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Collocations, st.Collocations >= 5 }},
	{id: "bookworm", name: "Bookworm", icon: "📖", desc: "Read 5 stories", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Stories, st.Stories >= 5 }},
	{id: "wise_owl", name: "Wise Owl", icon: "💡", desc: "Read 5 tips", cat: "Library", target: 5,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.Tips, st.Tips >= 5 }},

	// Social
	{id: "social_butterfly", name: "Social Butterfly", icon: "🌟", desc: "Receive kudos from someone", cat: "Social", target: 1,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.kudosReceived, ex.kudosReceived >= 1 }},

	// Dedication
	{id: "dedicated_learner", name: "Dedicated Learner", icon: "📅", desc: "30 active days", cat: "Dedication", target: 30,
		check: func(st UserStats, _ achievementExtra) (int, bool) { return st.ActiveDays, st.ActiveDays >= 30 }},
	{id: "veteran", name: "Veteran", icon: "🗓️", desc: "Member for 90 days", cat: "Dedication", target: 90,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.memberDays, ex.memberDays >= 90 }},

	// Mastery
	{id: "all_rounder", name: "All-rounder", icon: "🎓", desc: "Try every content type", cat: "Mastery", target: 7,
		check: func(_ UserStats, ex achievementExtra) (int, bool) { return ex.contentTypes, ex.contentTypes >= 7 }},
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
