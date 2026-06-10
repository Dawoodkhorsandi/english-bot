package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Per-user hourly rate limiter
// ---------------------------------------------------------------------------

// hourlyLimiter enforces at most one scheduled message per user per clock-hour.
// It is shared by all background schedulers (broadcasts, SRS, quiz, idiom, tip,
// daily review) so they never pile up on the same user in the same hour.
// State is in-memory; a restart resets it, which is acceptable (at most one
// extra message at the hour boundary immediately after a restart).
type hourlyLimiter struct {
	mu   sync.Mutex
	seen map[int64]string // chatID → "YYYY-MM-DD-HH" (local hour key)
}

// claimSlot returns true and records the slot if no other scheduler has already
// sent to this user in the current interval-aligned window. The window size is
// the user's broadcast interval so a 30-min user still gets two slots per hour
// while a 60-min user gets one. Returns false if the slot is taken.
func (l *hourlyLimiter) claimSlot(chatID int64, now time.Time, intervalMinutes int) bool {
	if intervalMinutes <= 0 {
		intervalMinutes = defaultInterval
	}
	m := minutesSinceMidnight(now)
	slot := m / intervalMinutes
	key := fmt.Sprintf("%s:%d", now.In(appLocation).Format("2006-01-02"), slot)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[chatID] == key {
		return false
	}
	l.seen[chatID] = key
	return true
}

// reset clears all recorded slots. Used in tests to isolate test cases.
func (l *hourlyLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = make(map[int64]string)
}

// globalHourlyLimiter is the single shared rate-limiter instance used by all
// background schedulers. One message per user per clock-hour (all sources combined).
var globalHourlyLimiter = &hourlyLimiter{seen: make(map[int64]string)}

// ---------------------------------------------------------------------------
// Quiet hours & slot helpers (Change C)
// ---------------------------------------------------------------------------

// parseHourMinute parses a "HH:MM" string into hour and minute. On error it
// returns 0,0.
func parseHourMinute(s string) (hour, min int) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	fmt.Sscanf(parts[0], "%d", &hour)
	fmt.Sscanf(parts[1], "%d", &min)
	return hour, min
}

// minutesOfDay returns t's wall-clock time as minutes since midnight.
func minutesOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// isQuietHours reports whether t (in appLocation) falls within the configured
// quiet window. Handles windows that wrap past midnight.
func isQuietHours(t time.Time) bool {
	local := t.In(appLocation)
	sh, sm := parseHourMinute(quietStart)
	eh, em := parseHourMinute(quietEnd)
	start := sh*60 + sm
	end := eh*60 + em
	now := minutesOfDay(local)

	if start == end {
		return false
	}
	if start < end {
		return now >= start && now < end
	}
	// Wraps midnight (e.g. 23:00–06:00).
	return now >= start || now < end
}

// minutesSinceMidnight returns t's wall-clock position (in appLocation) as
// minutes since local midnight.
func minutesSinceMidnight(t time.Time) int {
	local := t.In(appLocation)
	return local.Hour()*60 + local.Minute()
}

// dueAndKind reports whether a user with the given send interval (minutes) should
// receive content at time t, and which kind. Due-ness is wall-clock aligned
// (minutesSinceMidnight % interval == 0) so restarts and quiet-hour skips never
// desync delivery. The drill/word alternation is per-user: even interval-slots
// send a drill, odd send a word.
func dueAndKind(t time.Time, interval int) (due bool, kind string) {
	if interval <= 0 {
		interval = defaultInterval
	}
	m := minutesSinceMidnight(t)
	if m%interval != 0 {
		return false, ""
	}
	if (m/interval)%2 == 0 {
		return true, kindDrill
	}
	return true, kindWord
}

// nextHalfHour returns the next :00 or :30 boundary after t.
func nextHalfHour(t time.Time) time.Time {
	local := t.In(appLocation)
	truncated := local.Truncate(30 * time.Minute)
	next := truncated.Add(30 * time.Minute)
	if !next.After(local) {
		next = next.Add(30 * time.Minute)
	}
	return next
}

// nextMidnight returns the next 00:00 boundary (in appLocation) after t.
func nextMidnight(t time.Time) time.Time {
	local := t.In(appLocation)
	y, m, d := local.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, appLocation)
	for !midnight.After(local) {
		midnight = midnight.AddDate(0, 0, 1)
	}
	return midnight
}

// ---------------------------------------------------------------------------
// Broadcast scheduler (Changes B + C)
// ---------------------------------------------------------------------------

// runBroadcastScheduler fires every half hour (the base slot), skipping quiet
// hours, and runs a per-user delivery sweep: each subscriber receives content
// only on slots aligned to their chosen interval, alternating drill/word.
func runBroadcastScheduler(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	log.Println("⏰ [SCHED] Broadcast scheduler started (half-hourly base, per-user interval, quiet-hour aware).")
	for {
		next := nextHalfHour(time.Now())
		wait := time.Until(next)
		log.Printf("⏰ [SCHED] Next broadcast slot at %s (in %s).", next.In(appLocation).Format("15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("⏰ [SCHED] Broadcast scheduler stopped.")
			return
		case <-time.After(wait):
		}

		now := time.Now()
		if isQuietHours(now) {
			log.Printf("🌙 [SCHED] %s is within quiet hours (%s–%s); skipping broadcast.", now.In(appLocation).Format("15:04"), quietStart, quietEnd)
			continue
		}

		broadcastSweep(ctx, chain, store, notifier, now)
	}
}

// broadcastSweep delivers, for the current slot, one pooled item to every
// subscriber who is (a) not paused and (b) due this slot per their interval. The
// content kind is chosen per user (drill/word alternation by interval-slot). It
// never triggers inline generation (pool-only); the poolFiller keeps the pool stocked.
func broadcastSweep(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, now time.Time) {
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [BROADCAST] Could not read subscribers: %v", err)
		return
	}
	if len(chats) == 0 {
		log.Printf("📢 [BROADCAST] No subscribers; nothing to do this slot.")
		return
	}

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [BROADCAST] Could not load prefs for chat %d: %v (using defaults)", chatID, err)
		}
		if prefs.Paused {
			continue
		}

		due, kind := dueAndKind(now, prefs.Interval)
		if !due {
			continue
		}

		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [BROADCAST] Rate-limited: skipping chat %d (already sent this interval slot).", chatID)
			continue
		}

		sendPendingChangelogs(store, notifier, chatID)

		text, term, err := serveContent(ctx, chain, store, chatID, kind, prefs.Level, false)
		if err != nil {
			log.Printf("❌ [BROADCAST] %s for chat %d failed: %v", kind, chatID, err)
			continue
		}
		// Drills go out paged (page 1 + navigation); words are a single message.
		var sendErr error
		if kind == kindDrill {
			sendErr = sendDrill(notifier, chatID, text)
		} else {
			sendErr = sendWordCardWithTTS(ctx, store, notifier, chatID, text, term)
		}
		if sendErr != nil {
			log.Printf("❌ [BROADCAST] Send to chat %d failed: %v", chatID, sendErr)
			continue
		}
		sent++
	}
	log.Printf("✅ [BROADCAST] Slot sweep complete: delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Startup changelog broadcast
// ---------------------------------------------------------------------------

// broadcastChangelogsOnStartup delivers unseen changelog entries to every
// subscriber immediately on boot. This ensures that when a new version is
// deployed (with a new entry in the Changelogs slice), all users receive the
// release notes right away rather than waiting for their next broadcast slot.
func broadcastChangelogsOnStartup(store *Store, notifier Notifier) {
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [CHANGELOG-BOOT] Could not read subscribers: %v", err)
		return
	}
	if len(chats) == 0 {
		log.Println("📣 [CHANGELOG-BOOT] No subscribers; skipping startup changelog broadcast.")
		return
	}

	delivered := 0
	for _, chatID := range chats {
		unseen, err := store.UnseenChangelogs(chatID)
		if err != nil {
			log.Printf("⚠️  [CHANGELOG-BOOT] Could not fetch unseen changelogs for ChatID %d: %v", chatID, err)
			continue
		}
		if len(unseen) == 0 {
			continue
		}
		for _, entry := range unseen {
			if entry.Silent {
				// Maintainer receives a private deploy notice.
				if isMaintainer(chatID) {
					msg := fmt.Sprintf("🔧 <b>[Internal Deploy v%s]</b>\n\n%s", entry.Version, entry.Text)
					_ = notifier.Send(chatID, msg)
				}
				if err := store.MarkChangelogSeen(chatID, entry.Version); err != nil {
					log.Printf("⚠️  [CHANGELOG-BOOT] Could not mark silent v%s seen for ChatID %d: %v", entry.Version, chatID, err)
				}
				continue
			}
			if err := notifier.Send(chatID, entry.Text); err != nil {
				log.Printf("❌ [CHANGELOG-BOOT] Failed to deliver v%s to ChatID %d: %v", entry.Version, chatID, err)
				continue
			}
			if err := store.MarkChangelogSeen(chatID, entry.Version); err != nil {
				log.Printf("⚠️  [CHANGELOG-BOOT] Could not mark v%s seen for ChatID %d: %v", entry.Version, chatID, err)
			}
			delivered++
		}
	}
	log.Printf("📣 [CHANGELOG-BOOT] Startup changelog broadcast complete: %d message(s) to %d subscriber(s).", delivered, len(chats))
}

// ---------------------------------------------------------------------------
// Daily review scheduler (Change C)
// ---------------------------------------------------------------------------

// runDailyReviewScheduler fires at local midnight and sends each subscriber a
// review of the vocabulary words they received during the day that just ended.
func runDailyReviewScheduler(ctx context.Context, store *Store, notifier Notifier) {
	log.Println("🌙 [REVIEW] Daily review scheduler started (fires at local midnight).")
	for {
		next := nextMidnight(time.Now())
		wait := time.Until(next)
		log.Printf("🌙 [REVIEW] Next review at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("🌙 [REVIEW] Daily review scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendDailyReview(store, notifier, time.Now())
	}
}

// sendDailyReview computes the just-ended local day window and sends each
// subscriber the words they saw that day. Idempotent per (chat, day).
func sendDailyReview(store *Store, notifier Notifier, now time.Time) {
	local := now.In(appLocation)
	// The day that just ended is "yesterday" relative to the midnight boundary.
	endLocal := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, appLocation)
	startLocal := endLocal.AddDate(0, 0, -1)
	reviewDate := startLocal.Format("2006-01-02")

	startUTC := startLocal.UTC().Format("2006-01-02 15:04:05")
	endUTC := endLocal.UTC().Format("2006-01-02 15:04:05")

	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [REVIEW] Could not read subscribers: %v", err)
		return
	}
	log.Printf("🌙 [REVIEW] Building review for %s (%d subscribers).", reviewDate, len(chats))

	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [REVIEW] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.DailyReviewEnabled {
			continue
		}

		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [REVIEW] Rate-limited: skipping daily review for chat %d.", chatID)
			continue
		}

		delivered, err := store.ReviewDelivered(chatID, reviewDate)
		if err != nil {
			log.Printf("⚠️  [REVIEW] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}

		items, err := store.WordsSentBetween(chatID, startUTC, endUTC)
		if err != nil {
			log.Printf("⚠️  [REVIEW] Word lookup failed for chat %d: %v", chatID, err)
			continue
		}
		if len(items) == 0 {
			// Nothing to review; mark done so we don't retry all day.
			_ = store.MarkReviewDelivered(chatID, reviewDate)
			continue
		}

		if err := notifier.Send(chatID, formatReview(items)); err != nil {
			log.Printf("❌ [REVIEW] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkReviewDelivered(chatID, reviewDate); err != nil {
			log.Printf("⚠️  [REVIEW] Could not mark review delivered for chat %d: %v", chatID, err)
		}
		log.Printf("🌙 [REVIEW] Sent %d-word review to chat %d.", len(items), chatID)
	}
}

// formatReview renders the bedtime review message from the day's words.
func formatReview(items []reviewItem) string {
	var b strings.Builder
	b.WriteString("🌙 <b>Today's Words — Review before bed</b>\n\n")
	for _, it := range items {
		if it.meaning != "" {
			b.WriteString(fmt.Sprintf("• <b>%s</b> — %s\n", it.term, it.meaning))
		} else {
			b.WriteString(fmt.Sprintf("• <b>%s</b>\n", it.term))
		}
	}
	b.WriteString("\n😴 Sleep well — say each one aloud once more before you do!")
	return b.String()
}

// ---------------------------------------------------------------------------
// Idiom of the day scheduler (Change Q)
// ---------------------------------------------------------------------------

// nextDailyTime returns the next occurrence of hh:mm (in appLocation) after t.
func nextDailyTime(t time.Time, hh, mm int) time.Time {
	local := t.In(appLocation)
	target := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, appLocation)
	for !target.After(local) {
		target = target.AddDate(0, 0, 1)
	}
	return target
}

// runIdiomScheduler fires once a day at idiomTime (local) and broadcasts one
// idiom of the day to every active subscriber. Set IDIOM_TIME=off to disable.
func runIdiomScheduler(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	if strings.EqualFold(strings.TrimSpace(idiomTime), "off") {
		log.Println("🗣️  [IDIOM] Idiom-of-the-day scheduler disabled (IDIOM_TIME=off).")
		return
	}
	hh, mm := parseHourMinute(idiomTime)
	log.Printf("🗣️  [IDIOM] Idiom-of-the-day scheduler started (fires daily at %02d:%02d local).", hh, mm)
	for {
		next := nextDailyTime(time.Now(), hh, mm)
		wait := time.Until(next)
		log.Printf("🗣️  [IDIOM] Next idiom at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("🗣️  [IDIOM] Idiom scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendIdiomOfDay(ctx, chain, store, notifier, time.Now())
	}
}

// sendIdiomOfDay sends each non-paused subscriber one pooled idiom, idempotent
// per (chat, local date). Pool-only (never generates inline) so the daily
// fan-out never hammers the AI; the pool filler keeps idioms stocked.
func sendIdiomOfDay(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, now time.Time) {
	idiomDate := now.In(appLocation).Format("2006-01-02")
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [IDIOM] Could not read subscribers: %v", err)
		return
	}
	log.Printf("🗣️  [IDIOM] Sending idiom of the day %s to %d subscriber(s).", idiomDate, len(chats))

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [IDIOM] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.IdiomEnabled {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [IDIOM] Rate-limited: skipping idiom for chat %d.", chatID)
			continue
		}
		delivered, err := store.IdiomDelivered(chatID, idiomDate)
		if err != nil {
			log.Printf("⚠️  [IDIOM] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}
		text, _, err := serveContent(ctx, chain, store, chatID, kindIdiom, store.GetLevel(chatID), false)
		if err != nil {
			log.Printf("⚠️  [IDIOM] No idiom available for chat %d: %v", chatID, err)
			continue
		}
		if err := sendCardWithTTS(ctx, store, notifier, chatID, text); err != nil {
			log.Printf("❌ [IDIOM] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkIdiomDelivered(chatID, idiomDate); err != nil {
			log.Printf("⚠️  [IDIOM] Mark delivered failed for chat %d: %v", chatID, err)
		}
		sent++
	}
	log.Printf("✅ [IDIOM] Idiom of the day delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Daily grammar tip scheduler
// ---------------------------------------------------------------------------

// runDailyTipScheduler fires once per local day at tipTime and sends one grammar
// tip to each non-paused subscriber who has tips enabled.
func runDailyTipScheduler(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	if strings.EqualFold(strings.TrimSpace(tipTime), "off") {
		log.Println("💡 [TIP] Daily tip scheduler disabled (TIP_TIME=off).")
		return
	}
	hh, mm := parseHourMinute(tipTime)
	log.Printf("💡 [TIP] Daily tip scheduler started (fires daily at %02d:%02d local).", hh, mm)
	for {
		next := nextDailyTime(time.Now(), hh, mm)
		wait := time.Until(next)
		log.Printf("💡 [TIP] Next tip sweep at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("💡 [TIP] Daily tip scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendDailyTip(ctx, chain, store, notifier, time.Now())
	}
}

// sendDailyTip sends one pooled grammar tip to each eligible subscriber. It is
// idempotent per (chat, local date), quiet-hour aware, and best-effort.
func sendDailyTip(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, now time.Time) {
	if isQuietHours(now) {
		log.Printf("🌙 [TIP] %s is within quiet hours (%s–%s); skipping tip sweep.", now.In(appLocation).Format("15:04"), quietStart, quietEnd)
		return
	}

	tipDate := now.In(appLocation).Format("2006-01-02")
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [TIP] Could not read subscribers: %v", err)
		return
	}
	log.Printf("💡 [TIP] Running tip sweep for %s (%d subscribers).", tipDate, len(chats))

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [TIP] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.TipsEnabled {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [TIP] Rate-limited: skipping tip for chat %d.", chatID)
			continue
		}

		delivered, err := store.TipDelivered(chatID, tipDate)
		if err != nil {
			log.Printf("⚠️  [TIP] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}

		// Best effort: pool-first, no inline generation on scheduler path.
		tipText, _, err := serveContent(ctx, chain, store, chatID, kindTip, defaultLevel, false)
		if err != nil {
			log.Printf("⚠️  [TIP] Could not get tip for chat %d: %v", chatID, err)
			continue
		}
		if err := notifier.Send(chatID, tipText); err != nil {
			log.Printf("❌ [TIP] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkTipDelivered(chatID, tipDate); err != nil {
			log.Printf("⚠️  [TIP] Could not mark tip delivered for chat %d: %v", chatID, err)
		}
		sent++
	}
	log.Printf("✅ [TIP] Daily tip delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Collocation of the day scheduler
// ---------------------------------------------------------------------------

// runCollocationScheduler fires once per local day at collocationTime and sends
// one collocation card to each non-paused subscriber who has collocations
// enabled. Set COLLOCATION_TIME=off to disable.
func runCollocationScheduler(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	if strings.EqualFold(strings.TrimSpace(collocationTime), "off") {
		log.Println("🔗 [COLLOCATION] Collocation-of-the-day scheduler disabled (COLLOCATION_TIME=off).")
		return
	}
	hh, mm := parseHourMinute(collocationTime)
	log.Printf("🔗 [COLLOCATION] Collocation-of-the-day scheduler started (fires daily at %02d:%02d local).", hh, mm)
	for {
		next := nextDailyTime(time.Now(), hh, mm)
		wait := time.Until(next)
		log.Printf("🔗 [COLLOCATION] Next collocation at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("🔗 [COLLOCATION] Collocation scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendCollocationOfDay(ctx, chain, store, notifier, time.Now())
	}
}

// sendCollocationOfDay sends each eligible subscriber one pooled collocation,
// idempotent per (chat, local date), quiet-hour aware. Pool-only (never
// generates inline) so the daily fan-out never hammers the AI.
func sendCollocationOfDay(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, now time.Time) {
	if isQuietHours(now) {
		log.Printf("🌙 [COLLOCATION] %s is within quiet hours (%s–%s); skipping collocation sweep.", now.In(appLocation).Format("15:04"), quietStart, quietEnd)
		return
	}

	collocationDate := now.In(appLocation).Format("2006-01-02")
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [COLLOCATION] Could not read subscribers: %v", err)
		return
	}
	log.Printf("🔗 [COLLOCATION] Sending collocation of the day %s to %d subscriber(s).", collocationDate, len(chats))

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [COLLOCATION] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.CollocationEnabled {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [COLLOCATION] Rate-limited: skipping collocation for chat %d.", chatID)
			continue
		}
		delivered, err := store.CollocationDelivered(chatID, collocationDate)
		if err != nil {
			log.Printf("⚠️  [COLLOCATION] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}
		text, _, err := serveContent(ctx, chain, store, chatID, kindCollocation, prefs.Level, false)
		if err != nil {
			log.Printf("⚠️  [COLLOCATION] No collocation available for chat %d: %v", chatID, err)
			continue
		}
		if err := sendCardWithTTS(ctx, store, notifier, chatID, text); err != nil {
			log.Printf("❌ [COLLOCATION] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkCollocationDelivered(chatID, collocationDate); err != nil {
			log.Printf("⚠️  [COLLOCATION] Mark delivered failed for chat %d: %v", chatID, err)
		}
		sent++
	}
	log.Printf("✅ [COLLOCATION] Collocation of the day delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Daily mini story scheduler
// ---------------------------------------------------------------------------

// runStoryScheduler fires once per local day at storyTime and sends one mini
// story to each non-paused subscriber who has stories enabled. Set
// STORY_TIME=off to disable.
func runStoryScheduler(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier) {
	if strings.EqualFold(strings.TrimSpace(storyTime), "off") {
		log.Println("📖 [STORY] Daily mini story scheduler disabled (STORY_TIME=off).")
		return
	}
	hh, mm := parseHourMinute(storyTime)
	log.Printf("📖 [STORY] Daily mini story scheduler started (fires daily at %02d:%02d local).", hh, mm)
	for {
		next := nextDailyTime(time.Now(), hh, mm)
		wait := time.Until(next)
		log.Printf("📖 [STORY] Next story at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("📖 [STORY] Story scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendMiniStory(ctx, chain, store, notifier, time.Now())
	}
}

// sendMiniStory sends each eligible subscriber one pooled mini story at their
// level, idempotent per (chat, local date), quiet-hour aware. Pool-only (never
// generates inline) so the daily fan-out never hammers the AI.
func sendMiniStory(ctx context.Context, chain *ProviderChain, store *Store, notifier Notifier, now time.Time) {
	if isQuietHours(now) {
		log.Printf("🌙 [STORY] %s is within quiet hours (%s–%s); skipping story sweep.", now.In(appLocation).Format("15:04"), quietStart, quietEnd)
		return
	}

	storyDate := now.In(appLocation).Format("2006-01-02")
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [STORY] Could not read subscribers: %v", err)
		return
	}
	log.Printf("📖 [STORY] Sending mini story %s to %d subscriber(s).", storyDate, len(chats))

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [STORY] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.StoryEnabled {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [STORY] Rate-limited: skipping story for chat %d.", chatID)
			continue
		}
		delivered, err := store.StoryDelivered(chatID, storyDate)
		if err != nil {
			log.Printf("⚠️  [STORY] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}
		text, _, err := serveContent(ctx, chain, store, chatID, kindStory, prefs.Level, false)
		if err != nil {
			log.Printf("⚠️  [STORY] No story available for chat %d: %v", chatID, err)
			continue
		}
		if err := notifier.Send(chatID, text); err != nil {
			log.Printf("❌ [STORY] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkStoryDelivered(chatID, storyDate); err != nil {
			log.Printf("⚠️  [STORY] Mark delivered failed for chat %d: %v", chatID, err)
		}
		sent++
	}
	log.Printf("✅ [STORY] Mini story delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Weekly digest scheduler (Change K)
// ---------------------------------------------------------------------------

// runWeeklyDigestScheduler fires once a week at the configured day+time and
// sends each subscriber a recap of the week's words, quiz accuracy, streak,
// and a "word of the week" highlight.
func runWeeklyDigestScheduler(ctx context.Context, store *Store, notifier Notifier) {
	if digestDay < 0 {
		log.Println("📅 [DIGEST] Weekly digest scheduler disabled.")
		return
	}
	weekdayName := time.Weekday(digestDay).String()
	log.Printf("📅 [DIGEST] Weekly digest scheduler started (%s at %s).", weekdayName, digestTime)
	for {
		next := nextWeekdayTime(time.Now(), time.Weekday(digestDay), digestTime)
		wait := time.Until(next)
		log.Printf("📅 [DIGEST] Next digest at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("📅 [DIGEST] Weekly digest scheduler stopped.")
			return
		case <-time.After(wait):
		}

		sendWeeklyDigest(store, notifier, time.Now())
	}
}

// ---------------------------------------------------------------------------
// Nightly SQLite backup scheduler (maintainer-only)
// ---------------------------------------------------------------------------

// runDBBackupScheduler sends one SQLite backup per day to the maintainer at
// BACKUP_TIME (local). Set BACKUP_TIME=off to disable.
func runDBBackupScheduler(ctx context.Context, store *Store, notifier Notifier) {
	if strings.EqualFold(strings.TrimSpace(backupTime), "off") {
		log.Println("🗄️  [BACKUP] Nightly backup scheduler disabled (BACKUP_TIME=off).")
		return
	}
	hh, mm := parseHourMinute(backupTime)
	log.Printf("🗄️  [BACKUP] Nightly backup scheduler started (fires daily at %02d:%02d local).", hh, mm)
	for {
		next := nextDailyTime(time.Now(), hh, mm)
		wait := time.Until(next)
		log.Printf("🗄️  [BACKUP] Next backup at %s (in %s).", next.Format("2006-01-02 15:04 MST"), wait.Truncate(time.Second))

		select {
		case <-ctx.Done():
			log.Println("🗄️  [BACKUP] Nightly backup scheduler stopped.")
			return
		case <-time.After(wait):
		}

		runNightlyDBBackup(store, notifier)
	}
}

// runNightlyDBBackup executes one scheduled backup attempt.
func runNightlyDBBackup(store *Store, notifier Notifier) {
	if err := sendMaintainerDBBackup(store, notifier, "nightly schedule"); err != nil {
		log.Printf("❌ [BACKUP] Nightly backup failed: %v", err)
	}
}

// nextWeekdayTime returns the next occurrence of the given weekday at the given
// local time (HH:MM format, parsed via parseHourMinute) after t.
func nextWeekdayTime(t time.Time, weekday time.Weekday, timeStr string) time.Time {
	h, m := parseHourMinute(timeStr)
	local := t.In(appLocation)
	y, mo, d := local.Date()
	target := time.Date(y, mo, d, h, m, 0, 0, appLocation)

	// Advance until we reach the correct weekday AND the time is in the future.
	for target.Weekday() != weekday || !target.After(local) {
		target = target.AddDate(0, 0, 1)
	}
	return target
}

// sendWeeklyDigest computes the past-7-day window and sends each subscriber a
// recap of the week's vocabulary, quiz accuracy, streak and a word highlight.
// Idempotent per (chat, week_start) via weekly_digest_delivery.
func sendWeeklyDigest(store *Store, notifier Notifier, now time.Time) {
	local := now.In(appLocation)
	weekStartLocal := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, appLocation).AddDate(0, 0, -7)
	weekStart := weekStartLocal.Format("2006-01-02")

	startUTC := weekStartLocal.UTC().Format("2006-01-02 15:04:05")
	endUTC := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, appLocation).UTC().Format("2006-01-02 15:04:05")

	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [DIGEST] Could not read subscribers: %v", err)
		return
	}
	log.Printf("📅 [DIGEST] Building weekly digest for week %s (%d subscribers).", weekStart, len(chats))

	sent := 0
	for _, chatID := range chats {
		prefs, err := store.GetPrefs(chatID)
		if err != nil {
			log.Printf("⚠️  [DIGEST] Could not load prefs for chat %d: %v", chatID, err)
			continue
		}
		if prefs.Paused || !prefs.DigestEnabled {
			continue
		}
		if !globalHourlyLimiter.claimSlot(chatID, now, prefs.Interval) {
			log.Printf("⏱️ [DIGEST] Rate-limited: skipping digest for chat %d.", chatID)
			continue
		}

		delivered, err := store.WeeklyDigestDelivered(chatID, weekStart)
		if err != nil {
			log.Printf("⚠️  [DIGEST] Delivery check failed for chat %d: %v", chatID, err)
			continue
		}
		if delivered {
			continue
		}

		words, err := store.WordsSentBetween(chatID, startUTC, endUTC)
		if err != nil {
			log.Printf("⚠️  [DIGEST] Word lookup failed for chat %d: %v", chatID, err)
			continue
		}

		stats, err := store.UserStats(chatID)
		if err != nil {
			log.Printf("⚠️  [DIGEST] Stats failed for chat %d: %v", chatID, err)
			continue
		}

		weekQuizAnswered, weekQuizCorrect, _ := store.WeeklyQuizStats(chatID, startUTC, endUTC)

		msg := formatWeeklyDigest(words, stats, weekQuizAnswered, weekQuizCorrect)
		if msg == "" {
			// Nothing to report; mark done so we don't retry.
			_ = store.MarkWeeklyDigestDelivered(chatID, weekStart)
			continue
		}

		if err := notifier.Send(chatID, msg); err != nil {
			log.Printf("❌ [DIGEST] Send to chat %d failed: %v", chatID, err)
			continue
		}
		if err := store.MarkWeeklyDigestDelivered(chatID, weekStart); err != nil {
			log.Printf("⚠️  [DIGEST] Could not mark digest delivered for chat %d: %v", chatID, err)
		}
		sent++
		log.Printf("📅 [DIGEST] Sent weekly digest to chat %d.", chatID)
	}
	if sent > 0 {
		log.Printf("📅 [DIGEST] Sweep complete: %d digest(s) delivered.", sent)
	}
}

// formatWeeklyDigest renders the weekly recap message. Returns "" if there is
// nothing worth reporting (no words learned and no quizzes taken).
func formatWeeklyDigest(words []reviewItem, stats UserStats, weekQuizAnswered, weekQuizCorrect int) string {
	if len(words) == 0 && weekQuizAnswered == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("📅 <b>Weekly Recap</b>\n\n")

	if len(words) > 0 {
		b.WriteString(fmt.Sprintf("📘 <b>Words learned this week:</b> %d\n", len(words)))
		for _, w := range words {
			if w.meaning != "" {
				b.WriteString(fmt.Sprintf("  • <b>%s</b> — %s\n", w.term, w.meaning))
			} else {
				b.WriteString(fmt.Sprintf("  • <b>%s</b>\n", w.term))
			}
		}
		b.WriteString("\n")
	}

	if weekQuizAnswered > 0 {
		pct := weekQuizCorrect * 100 / weekQuizAnswered
		b.WriteString(fmt.Sprintf("🧩 Quiz accuracy this week: <b>%d%%</b> (%d/%d)\n", pct, weekQuizCorrect, weekQuizAnswered))
	}

	if stats.CurrentStreak > 0 {
		flame := ""
		if stats.CurrentStreak >= 3 {
			flame = " 🔥"
		}
		b.WriteString(fmt.Sprintf("⚡ Current streak: <b>%d</b> day%s%s\n", stats.CurrentStreak, plural(stats.CurrentStreak), flame))
	}

	if stats.Mastered > 0 {
		b.WriteString(fmt.Sprintf("🧠 Total words mastered: <b>%d</b>\n", stats.Mastered))
	}

	// Word of the week: highlight the first word with a meaning.
	if len(words) > 0 {
		var wotw reviewItem
		for _, w := range words {
			if w.meaning != "" {
				wotw = w
				break
			}
		}
		if wotw.term == "" {
			wotw = words[0]
		}
		b.WriteString(fmt.Sprintf("\n⭐ <b>Word of the week:</b> <b>%s</b>", wotw.term))
		if wotw.meaning != "" {
			b.WriteString(fmt.Sprintf(" — %s", wotw.meaning))
		}
		b.WriteString("\n")
	}

	b.WriteString("\nKeep it up — consistency is the key to mastery! 💪")
	return b.String()
}
