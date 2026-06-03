package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

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
func runBroadcastScheduler(ctx context.Context, chain *ProviderChain, store *Store) {
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

		broadcastSweep(ctx, chain, store, now)
	}
}

// broadcastSweep delivers, for the current slot, one pooled item to every
// subscriber who is (a) not paused and (b) due this slot per their interval. The
// content kind is chosen per user (drill/word alternation by interval-slot). It
// never triggers inline generation (pool-only); the poolFiller keeps the pool stocked.
func broadcastSweep(ctx context.Context, chain *ProviderChain, store *Store, now time.Time) {
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

		sendPendingChangelogs(store, chatID)

		text, err := serveContent(ctx, chain, store, chatID, kind, prefs.Level, false)
		if err != nil {
			log.Printf("❌ [BROADCAST] %s for chat %d failed: %v", kind, chatID, err)
			continue
		}
		if err := sendToTelegram(chatID, text); err != nil {
			log.Printf("❌ [BROADCAST] Send to chat %d failed: %v", chatID, err)
			continue
		}
		sent++
	}
	log.Printf("✅ [BROADCAST] Slot sweep complete: delivered to %d/%d subscriber(s).", sent, len(chats))
}

// ---------------------------------------------------------------------------
// Daily review scheduler (Change C)
// ---------------------------------------------------------------------------

// runDailyReviewScheduler fires at local midnight and sends each subscriber a
// review of the vocabulary words they received during the day that just ended.
func runDailyReviewScheduler(ctx context.Context, store *Store) {
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

		sendDailyReview(store, time.Now())
	}
}

// sendDailyReview computes the just-ended local day window and sends each
// subscriber the words they saw that day. Idempotent per (chat, day).
func sendDailyReview(store *Store, now time.Time) {
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
		if store.IsPaused(chatID) {
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

		if err := sendToTelegram(chatID, formatReview(items)); err != nil {
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
