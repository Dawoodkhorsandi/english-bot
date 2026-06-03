package main

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestDueAndKind(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 6, 3, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		name     string
		t        time.Time
		interval int
		wantDue  bool
		wantKind string
	}{
		// 30-min interval alternates every base slot.
		{"30m @00:00 drill", at(0, 0), 30, true, kindDrill},
		{"30m @00:30 word", at(0, 30), 30, true, kindWord},
		{"30m @01:00 drill", at(1, 0), 30, true, kindDrill},
		// 60-min interval: due on the hour only, alternating each hour.
		{"60m @00:00 drill", at(0, 0), 60, true, kindDrill},
		{"60m @00:30 not due", at(0, 30), 60, false, ""},
		{"60m @01:00 word", at(1, 0), 60, true, kindWord},
		{"60m @02:00 drill", at(2, 0), 60, true, kindDrill},
		// 120-min interval: due every 2h, alternating each due slot.
		{"120m @00:00 drill", at(0, 0), 120, true, kindDrill},
		{"120m @01:00 not due", at(1, 0), 120, false, ""},
		{"120m @02:00 word", at(2, 0), 120, true, kindWord},
		{"120m @04:00 drill", at(4, 0), 120, true, kindDrill},
		// Zero/invalid interval falls back to default (30).
		{"invalid falls back @00:30 word", at(0, 30), 0, true, kindWord},
	}
	for _, c := range cases {
		due, kind := dueAndKind(c.t, c.interval)
		if due != c.wantDue || (due && kind != c.wantKind) {
			t.Errorf("%s: got due=%v kind=%q, want due=%v kind=%q",
				c.name, due, kind, c.wantDue, c.wantKind)
		}
	}
}

func TestNormalizeInterval(t *testing.T) {
	if v, ok := normalizeInterval(60); !ok || v != 60 {
		t.Errorf("normalizeInterval(60) = %d,%v; want 60,true", v, ok)
	}
	if v, ok := normalizeInterval(45); ok || v != defaultInterval {
		t.Errorf("normalizeInterval(45) = %d,%v; want %d,false", v, ok, defaultInterval)
	}
}

// TestIntervalMigration verifies a pre-v1.6.0 user_prefs table (no
// interval_minutes column) is migrated additively without data loss.
func TestIntervalMigration(t *testing.T) {
	path := t.TempDir() + "/legacy.db"

	// Seed a pre-v1.6.0 user_prefs table (level + paused only) with a row.
	pre, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := pre.Exec(`CREATE TABLE user_prefs (
		chat_id INTEGER PRIMARY KEY,
		level TEXT NOT NULL DEFAULT 'intermediate',
		paused INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := pre.Exec("INSERT INTO user_prefs (chat_id, level, paused) VALUES (?, 'advanced', 1)", 7); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	pre.Close()

	// openStore should run the migration and add interval_minutes.
	store, err := openStore(path)
	if err != nil {
		t.Fatalf("openStore (migration): %v", err)
	}
	defer store.Close()

	prefs, err := store.GetPrefs(7)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if prefs.Level != levelAdvanced {
		t.Errorf("level = %q, want advanced (data lost?)", prefs.Level)
	}
	if !prefs.Paused {
		t.Errorf("paused = false, want true (data lost?)")
	}
	if prefs.Interval != defaultInterval {
		t.Errorf("interval = %d, want default %d", prefs.Interval, defaultInterval)
	}
}

func TestNextWeekdayTime(t *testing.T) {
	// Wednesday 2026-06-03 14:00 Tehran; next Sunday at 20:00 should be 2026-06-07.
	loc, _ := time.LoadLocation("Asia/Tehran")
	appLocation = loc
	now := time.Date(2026, 6, 3, 14, 0, 0, 0, loc) // Wednesday

	next := nextWeekdayTime(now, time.Sunday, "20:00")
	if next.Weekday() != time.Sunday {
		t.Errorf("weekday = %s, want Sunday", next.Weekday())
	}
	if !next.After(now) {
		t.Error("next must be after now")
	}
	if next.Hour() != 20 || next.Minute() != 0 {
		t.Errorf("time = %02d:%02d, want 20:00", next.Hour(), next.Minute())
	}
	// Should be June 7 (the coming Sunday)
	if next.Day() != 7 {
		t.Errorf("day = %d, want 7", next.Day())
	}
}

func TestNextWeekdayTimeSameDay(t *testing.T) {
	// If today is Sunday at 10:00 and digest is Sunday 20:00, it should be today.
	loc, _ := time.LoadLocation("Asia/Tehran")
	appLocation = loc
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, loc) // Sunday 10:00

	next := nextWeekdayTime(now, time.Sunday, "20:00")
	if next.Day() != 7 {
		t.Errorf("day = %d, want 7 (same day, later time)", next.Day())
	}
}

func TestNextWeekdayTimePast(t *testing.T) {
	// If today is Sunday at 21:00 and digest is Sunday 20:00, next should be next Sunday.
	loc, _ := time.LoadLocation("Asia/Tehran")
	appLocation = loc
	now := time.Date(2026, 6, 7, 21, 0, 0, 0, loc) // Sunday 21:00

	next := nextWeekdayTime(now, time.Sunday, "20:00")
	if next.Day() != 14 {
		t.Errorf("day = %d, want 14 (next Sunday)", next.Day())
	}
}

func TestParseHourMinute(t *testing.T) {
	cases := []struct {
		input string
		wantH int
		wantM int
	}{
		{"09:30", 9, 30},
		{"00:00", 0, 0},
		{"23:59", 23, 59},
		{"", 0, 0},
		{"abc", 0, 0},
		{"12", 0, 0},
		{"9:5", 9, 5},
	}
	for _, c := range cases {
		h, m := parseHourMinute(c.input)
		if h != c.wantH || m != c.wantM {
			t.Errorf("parseHourMinute(%q) = (%d,%d), want (%d,%d)",
				c.input, h, m, c.wantH, c.wantM)
		}
	}
}

func TestIsQuietHours(t *testing.T) {
	savedLoc := appLocation
	savedStart := quietStart
	savedEnd := quietEnd
	defer func() {
		appLocation = savedLoc
		quietStart = savedStart
		quietEnd = savedEnd
	}()

	appLocation = time.UTC

	cases := []struct {
		name  string
		start string
		end   string
		hour  int
		min   int
		want  bool
	}{
		{"03:00 within 00:00-09:00", "00:00", "09:00", 3, 0, true},
		{"10:00 outside 00:00-09:00", "00:00", "09:00", 10, 0, false},
		{"00:00 boundary start", "00:00", "09:00", 0, 0, true},
		{"09:00 exclusive end", "00:00", "09:00", 9, 0, false},
		{"same start/end disabled", "05:00", "05:00", 5, 0, false},
		{"wrap midnight 01:00 quiet", "23:00", "06:00", 1, 0, true},
		{"wrap midnight 12:00 not quiet", "23:00", "06:00", 12, 0, false},
		{"wrap midnight 23:30 quiet", "23:00", "06:00", 23, 30, true},
	}
	for _, c := range cases {
		quietStart = c.start
		quietEnd = c.end
		tm := time.Date(2026, 6, 3, c.hour, c.min, 0, 0, time.UTC)
		if got := isQuietHours(tm); got != c.want {
			t.Errorf("%s: isQuietHours = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextHalfHour(t *testing.T) {
	savedLoc := appLocation
	defer func() { appLocation = savedLoc }()
	appLocation = time.UTC

	cases := []struct {
		min      int
		wantHour int
		wantMin  int
	}{
		{0, 0, 30},
		{15, 0, 30},
		{29, 0, 30},
		{30, 1, 0},
		{45, 1, 0},
		{59, 1, 0},
	}
	for _, c := range cases {
		tm := time.Date(2026, 6, 3, 0, c.min, 0, 0, time.UTC)
		got := nextHalfHour(tm)
		if got.Hour() != c.wantHour || got.Minute() != c.wantMin {
			t.Errorf("nextHalfHour(:%02d) = %02d:%02d, want %02d:%02d",
				c.min, got.Hour(), got.Minute(), c.wantHour, c.wantMin)
		}
	}
}

func TestNextMidnight(t *testing.T) {
	savedLoc := appLocation
	defer func() { appLocation = savedLoc }()
	appLocation = time.UTC

	cases := []struct {
		name     string
		input    time.Time
		wantDay  int
		wantHour int
	}{
		{"14:00", time.Date(2026, 6, 3, 14, 0, 0, 0, time.UTC), 4, 0},
		{"23:59", time.Date(2026, 6, 3, 23, 59, 0, 0, time.UTC), 4, 0},
		{"00:00:01", time.Date(2026, 6, 3, 0, 0, 1, 0, time.UTC), 4, 0},
	}
	for _, c := range cases {
		got := nextMidnight(c.input)
		if got.Day() != c.wantDay || got.Hour() != c.wantHour {
			t.Errorf("%s: nextMidnight = day %d %02d:00, want day %d %02d:00",
				c.name, got.Day(), got.Hour(), c.wantDay, c.wantHour)
		}
	}
}

func TestMinutesSinceMidnight(t *testing.T) {
	savedLoc := appLocation
	defer func() { appLocation = savedLoc }()
	appLocation = time.UTC

	cases := []struct {
		hour int
		min  int
		want int
	}{
		{14, 30, 870},
		{0, 0, 0},
	}
	for _, c := range cases {
		tm := time.Date(2026, 6, 3, c.hour, c.min, 0, 0, time.UTC)
		if got := minutesSinceMidnight(tm); got != c.want {
			t.Errorf("minutesSinceMidnight(%02d:%02d) = %d, want %d",
				c.hour, c.min, got, c.want)
		}
	}
}

func TestFormatReview(t *testing.T) {
	items := []reviewItem{
		{term: "hello", meaning: "a greeting"},
		{term: "ephemeral", meaning: ""},
		{term: "ubiquitous", meaning: "present everywhere"},
	}
	got := formatReview(items)

	if !strings.Contains(got, "Today's Words") {
		t.Error("missing 'Today's Words' header")
	}
	for _, it := range items {
		bold := "<b>" + it.term + "</b>"
		if !strings.Contains(got, bold) {
			t.Errorf("missing bolded term %q", it.term)
		}
	}
	if !strings.Contains(got, "a greeting") {
		t.Error("missing meaning for 'hello'")
	}
	if !strings.Contains(got, "present everywhere") {
		t.Error("missing meaning for 'ubiquitous'")
	}
	// "ephemeral" has no meaning — should NOT have a dash after it
	if strings.Contains(got, "ephemeral</b> —") {
		t.Error("ephemeral should not have a meaning separator")
	}
}

func TestFormatReviewReminder(t *testing.T) {
	withMeaning := dueReview{term: "serendipity", meaning: "happy accident", intervalDays: 3, ease: 2.5}
	got := formatReviewReminder(withMeaning)
	if !strings.Contains(got, "Memory check") {
		t.Error("missing 'Memory check' header")
	}
	if !strings.Contains(got, "<b>serendipity</b>") {
		t.Error("missing bolded term")
	}
	if !strings.Contains(got, "happy accident") {
		t.Error("missing meaning")
	}

	without := dueReview{term: "aplomb", meaning: "", intervalDays: 1, ease: 2.5}
	got2 := formatReviewReminder(without)
	if !strings.Contains(got2, "<b>aplomb</b>") {
		t.Error("missing bolded term for no-meaning case")
	}
	if strings.Contains(got2, "💬") {
		t.Error("meaning icon should not appear when meaning is empty")
	}
}

// ---------------------------------------------------------------------------
// reviewKeyboard test
// ---------------------------------------------------------------------------

func TestReviewKeyboard(t *testing.T) {
	kb := reviewKeyboard("ephemeral")
	if len(kb) != 1 {
		t.Fatalf("expected 1 row, got %d", len(kb))
	}
	if len(kb[0]) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(kb[0]))
	}
	if kb[0][0].CallbackData != "srs:known:ephemeral" {
		t.Errorf("known button data = %q, want srs:known:ephemeral", kb[0][0].CallbackData)
	}
	if kb[0][1].CallbackData != "srs:forgot:ephemeral" {
		t.Errorf("forgot button data = %q, want srs:forgot:ephemeral", kb[0][1].CallbackData)
	}
}

// ---------------------------------------------------------------------------
// runReviewSweep tests
// ---------------------------------------------------------------------------

func TestRunReviewSweep_DeliversDueReviews(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "00:00" // no quiet hours

	store.AddSubscriber(100)
	now := time.Date(2026, 6, 3, 14, 0, 0, 0, time.UTC)

	// Seed a review that's already due (seed time 2 days ago).
	store.SeedReview(100, "ephemeral", now.AddDate(0, 0, -2))

	runReviewSweep(store, mock, now)

	if len(mock.keyboard) == 0 {
		t.Fatal("expected at least one review reminder sent")
	}
	if !strings.Contains(mock.keyboard[0].text, "ephemeral") {
		t.Error("expected review text to contain the term")
	}
}

func TestRunReviewSweep_SkipsQuietHours(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "23:59"

	store.AddSubscriber(100)
	store.SeedReview(100, "ephemeral", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 6, 3, 14, 0, 0, 0, time.UTC)

	runReviewSweep(store, mock, now)

	if len(mock.keyboard) != 0 {
		t.Error("expected no reviews during quiet hours")
	}
}

func TestRunReviewSweep_SkipsPausedUsers(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveQuietHours(t)
	saveAppLocation(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "00:00"

	store.AddSubscriber(100)
	store.SetPaused(100, true)
	store.SeedReview(100, "ephemeral", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 6, 3, 14, 0, 0, 0, time.UTC)

	runReviewSweep(store, mock, now)

	if len(mock.keyboard) != 0 {
		t.Error("expected no reviews for paused users")
	}
}

// ---------------------------------------------------------------------------
// sendDailyReview tests
// ---------------------------------------------------------------------------

func TestSendDailyReview_DeliversReview(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)

	// Seed sent_vocab from yesterday so there's content for the review.
	yesterday := "2026-06-02 14:00:00"
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "ephemeral", yesterday)
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "lucid", yesterday)

	// Fire at midnight of June 3 — reviews "yesterday" (June 2).
	midnight := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	sendDailyReview(store, mock, midnight)

	if mock.sentCount() == 0 {
		t.Fatal("expected a daily review to be sent")
	}
	text := mock.lastSentText()
	if !strings.Contains(text, "Today's Words") {
		t.Error("expected review header in message")
	}
	if !strings.Contains(text, "ephemeral") || !strings.Contains(text, "lucid") {
		t.Error("expected reviewed words in message")
	}
}

func TestSendDailyReview_SkipsPausedUsers(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	store.SetPaused(100, true)

	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "word", "2026-06-02 14:00:00")

	midnight := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	sendDailyReview(store, mock, midnight)

	if mock.sentCount() != 0 {
		t.Error("expected no review for paused user")
	}
}

func TestSendDailyReview_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "word", "2026-06-02 14:00:00")

	midnight := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	sendDailyReview(store, mock, midnight)
	count1 := mock.sentCount()

	sendDailyReview(store, mock, midnight)
	if mock.sentCount() != count1 {
		t.Error("expected idempotent: second call should not re-send")
	}
}

func TestSendDailyReview_NoWords(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	// No words sent yesterday.

	midnight := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	sendDailyReview(store, mock, midnight)

	if mock.sentCount() != 0 {
		t.Error("expected no message when there are no words to review")
	}
}

// ---------------------------------------------------------------------------
// sendWeeklyDigest tests
// ---------------------------------------------------------------------------

func TestSendWeeklyDigest_DeliversDigest(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)

	// Seed words across the past week.
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "lucid", "2026-05-28 10:00:00")
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "vivid", "2026-05-29 10:00:00")

	// Fire at Sunday evening June 3.
	digestTime := time.Date(2026, 6, 3, 20, 0, 0, 0, time.UTC)
	sendWeeklyDigest(store, mock, digestTime)

	if mock.sentCount() == 0 {
		t.Fatal("expected a weekly digest to be sent")
	}
	text := mock.lastSentText()
	if !strings.Contains(text, "Weekly Recap") {
		t.Error("expected 'Weekly Recap' header")
	}
}

func TestSendWeeklyDigest_SkipsPausedUsers(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	store.SetPaused(100, true)

	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "word", "2026-05-28 10:00:00")

	digestTime := time.Date(2026, 6, 3, 20, 0, 0, 0, time.UTC)
	sendWeeklyDigest(store, mock, digestTime)

	if mock.sentCount() != 0 {
		t.Error("expected no digest for paused user")
	}
}

func TestSendWeeklyDigest_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	store.db.Exec("INSERT INTO sent_vocab (chat_id, word, sent_at) VALUES (?, ?, ?)", 100, "word", "2026-05-28 10:00:00")

	digestTime := time.Date(2026, 6, 3, 20, 0, 0, 0, time.UTC)
	sendWeeklyDigest(store, mock, digestTime)
	count1 := mock.sentCount()

	sendWeeklyDigest(store, mock, digestTime)
	if mock.sentCount() != count1 {
		t.Error("expected idempotent: second call should not re-send")
	}
}

func TestSendWeeklyDigest_NoActivity(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	store.AddSubscriber(100)
	// No words or quizzes this week.

	digestTime := time.Date(2026, 6, 3, 20, 0, 0, 0, time.UTC)
	sendWeeklyDigest(store, mock, digestTime)

	if mock.sentCount() != 0 {
		t.Error("expected no digest when there's no activity")
	}
}
