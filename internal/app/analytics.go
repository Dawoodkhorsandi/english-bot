package app

import (
	"sort"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// LearningAnalytics holds detailed learning data for the analytics dashboard.
type LearningAnalytics struct {
	WordBreakdown     []CategoryCount `json:"word_breakdown"`
	QuizAccuracyTrend []DayAccuracy   `json:"quiz_accuracy_trend"`
	ActivityByHour    []HourActivity  `json:"activity_by_hour"`
	WeeklyVelocity    []WeekCount     `json:"weekly_velocity"`
	ContentDiversity  []CategoryCount `json:"content_diversity"`
}

// CategoryCount is a label + count pair for bar charts.
type CategoryCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// DayAccuracy is a date with quiz accuracy for trend charts.
type DayAccuracy struct {
	Date    string `json:"date"`
	Correct int    `json:"correct"`
	Total   int    `json:"total"`
	Pct     int    `json:"pct"`
}

// HourActivity is an hour-of-day bucket (0-23) with activity count.
type HourActivity struct {
	Hour  int `json:"hour"`
	Count int `json:"count"`
}

// WeekCount is a week start date with activity count.
type WeekCount struct {
	Week  string `json:"week"`
	Count int    `json:"count"`
}

// ComputeAnalytics assembles detailed learning analytics for a user.
func (s *Store) ComputeAnalytics(chatID int64) (*LearningAnalytics, error) {
	la := &LearningAnalytics{}

	la.WordBreakdown = s.wordBreakdownByLevel(chatID)
	la.QuizAccuracyTrend = s.quizAccuracyTrend(chatID)
	la.ActivityByHour = s.activityByHour(chatID)
	la.WeeklyVelocity = s.weeklyVelocity(chatID)
	la.ContentDiversity = s.contentDiversity(chatID)

	return la, nil
}

// wordBreakdownByLevel returns vocabulary counts grouped by difficulty level.
func (s *Store) wordBreakdownByLevel(chatID int64) []CategoryCount {
	rows, err := s.db.Query(`
		SELECT COALESCE(c.level, 'unknown') AS lvl, COUNT(*)
		FROM sent_vocab v
		LEFT JOIN content_pool c ON c.term = v.word AND c.kind = 'word'
		WHERE v.chat_id = ?
		GROUP BY lvl
		ORDER BY COUNT(*) DESC`, chatID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []CategoryCount
	for rows.Next() {
		var cc CategoryCount
		if err := rows.Scan(&cc.Label, &cc.Count); err != nil {
			return out
		}
		out = append(out, cc)
	}
	return out
}

// quizAccuracyTrend returns per-day quiz accuracy for the last 30 days.
func (s *Store) quizAccuracyTrend(chatID int64) []DayAccuracy {
	rows, err := s.db.Query(`
		SELECT DATE(answered_at) AS day, SUM(correct), COUNT(*)
		FROM quiz_results
		WHERE chat_id = ? AND answered_at >= DATE('now', '-30 days')
		GROUP BY day
		ORDER BY day`, chatID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []DayAccuracy
	for rows.Next() {
		var da DayAccuracy
		if err := rows.Scan(&da.Date, &da.Correct, &da.Total); err != nil {
			return out
		}
		if da.Total > 0 {
			da.Pct = da.Correct * 100 / da.Total
		}
		out = append(out, da)
	}
	return out
}

// activityByHour returns a 24-bucket histogram of activity by hour-of-day in the user's timezone.
func (s *Store) activityByHour(chatID int64) []HourActivity {
	buckets := make([]int, 24)

	// Sent items: words + drills
	for _, table := range []string{"sent_words", "sent_vocab"} {
		rows, err := s.db.Query(`SELECT sent_at FROM `+table+` WHERE chat_id = ?`, chatID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var raw any
			if err := rows.Scan(&raw); err != nil {
				continue
			}
			if ts, ok := parseStoredUTC(raw); ok {
				hour := ts.In(config.AppLocation).Hour()
				buckets[hour]++
			}
		}
		rows.Close()
	}

	// Activity log entries
	logRows, err := s.db.Query(`SELECT day FROM activity_log WHERE chat_id = ?`, chatID)
	if err == nil {
		defer logRows.Close()
		for logRows.Next() {
			var day string
			if err := logRows.Scan(&day); err != nil {
				continue
			}
			// activity_log only stores the day, not the hour. We count it as noon (12)
			// for a rough distribution — the sent_* tables have the real timestamps.
			buckets[12]++
		}
	}

	out := make([]HourActivity, 24)
	for h := 0; h < 24; h++ {
		out[h] = HourActivity{Hour: h, Count: buckets[h]}
	}
	return out
}

// weeklyVelocity returns the number of new words learned per week for the last 8 weeks.
func (s *Store) weeklyVelocity(chatID int64) []WeekCount {
	rows, err := s.db.Query(`
		SELECT strftime('%Y-%W', sent_at) AS wk, COUNT(*)
		FROM sent_vocab
		WHERE chat_id = ? AND sent_at >= DATE('now', '-56 days')
		GROUP BY wk
		ORDER BY wk`, chatID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []WeekCount
	for rows.Next() {
		var wc WeekCount
		if err := rows.Scan(&wc.Week, &wc.Count); err != nil {
			return out
		}
		out = append(out, wc)
	}
	return out
}

// contentDiversity returns the count of each content type received.
func (s *Store) contentDiversity(chatID int64) []CategoryCount {
	type entry struct {
		table, label string
	}
	tables := []entry{
		{"sent_vocab", "Words"},
		{"sent_words", "Drills"},
		{"sent_idioms", "Idioms"},
		{"sent_collocations", "Collocations"},
		{"sent_stories", "Stories"},
		{"sent_tips", "Tips"},
	}
	var out []CategoryCount
	for _, t := range tables {
		var n int
		_ = s.db.QueryRow("SELECT COUNT(*) FROM "+t.table+" WHERE chat_id = ?", chatID).Scan(&n)
		if n > 0 {
			out = append(out, CategoryCount{Label: t.label, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}
