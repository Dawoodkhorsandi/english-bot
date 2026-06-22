package app

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Streak savers (#7)
//
// A streak saver is a banked token that automatically rescues a single missed
// day so a streak survives one slip — no action needed from the user. Users earn
// one free saver on each streak milestone and can buy more with Telegram Stars
// (currency "XTR"), the proven low-friction in-app purchase for the Iran market.
// ---------------------------------------------------------------------------

const (
	// starsPerFreeze is the Telegram Stars price of one streak saver.
	starsPerFreeze = 50
	// maxFreezePurchase caps a single purchase quantity.
	maxFreezePurchase = 10
)

// handleStreak shows the user's saver balance and how to earn or buy more.
func handleStreak(store *Store, notifier telegram.Notifier, chatID int64) {
	bal := store.GetStreakFreezes(chatID)
	msg := fmt.Sprintf("🧊 <b>Streak savers: %d</b>\n\n"+
		"A streak saver automatically protects your streak if you miss a day — "+
		"you keep your run without lifting a finger.\n\n"+
		"• Earn one free saver at every streak milestone (3, 7, 14, 30, 60 days).\n"+
		"• Buy more with Telegram Stars below.", bal)
	kb := [][]telegram.InlineButton{{
		{Text: fmt.Sprintf("🧊 Buy 1 (%d ⭐)", starsPerFreeze), CallbackData: "streakbuy:1"},
		{Text: fmt.Sprintf("🧊 Buy 5 (%d ⭐)", starsPerFreeze*5), CallbackData: "streakbuy:5"},
	}}
	_ = notifier.SendKeyboard(chatID, msg, kb)
}

// handleBuyStreak sends a Stars invoice for N savers (default 1) via /buystreak.
func handleBuyStreak(notifier telegram.Notifier, chatID int64, args []string) {
	qty := 1
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil {
			qty = n
		}
	}
	sendStreakInvoice(notifier, chatID, qty)
}

// sendStreakInvoice sends a Telegram Stars invoice for qty savers (clamped).
func sendStreakInvoice(notifier telegram.Notifier, chatID int64, qty int) {
	if qty < 1 {
		qty = 1
	}
	if qty > maxFreezePurchase {
		qty = maxFreezePurchase
	}
	title := fmt.Sprintf("%d Streak Saver%s", qty, plural(qty))
	desc := "Automatically protects your streak if you miss a day."
	payload := "freeze:" + strconv.Itoa(qty)
	if err := telegram.SendInvoice(chatID, title, desc, payload, starsPerFreeze*qty); err != nil {
		log.Printf("⚠️  [STREAK] SendInvoice to chat %d failed: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Couldn't start the purchase right now. Please try again later.")
	}
}

// handlePreCheckout approves a pending streak-saver checkout. Telegram requires
// an answer within ~10s; tokens are granted later on successful_payment.
func handlePreCheckout(notifier telegram.Notifier, q *telegram.PreCheckoutQuery) {
	ok := parseFreezePayload(q.InvoicePayload) > 0
	if err := telegram.AnswerPreCheckoutQuery(q.ID, ok, "Sorry, that purchase is no longer available."); err != nil {
		log.Printf("⚠️  [STREAK] AnswerPreCheckoutQuery failed: %v", err)
	}
}

// handleSuccessfulPayment credits the purchased savers once a payment lands.
func handleSuccessfulPayment(store *Store, notifier telegram.Notifier, chatID int64, p *telegram.SuccessfulPayment) {
	qty := parseFreezePayload(p.InvoicePayload)
	if qty <= 0 {
		return
	}
	bal, err := store.AddStreakFreezes(chatID, qty)
	if err != nil {
		log.Printf("❌ [STREAK] Could not credit %d saver(s) to chat %d: %v", qty, chatID, err)
		return
	}
	_ = notifier.Send(chatID, fmt.Sprintf("🧊 <b>Thank you!</b> %d streak saver%s added — you now have <b>%d</b>. "+
		"They protect your streak automatically.", qty, plural(qty), bal))
}

// parseFreezePayload extracts the quantity from a "freeze:N" invoice payload.
func parseFreezePayload(payload string) int {
	if !strings.HasPrefix(payload, "freeze:") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(payload, "freeze:"))
	if err != nil || n < 1 || n > maxFreezePurchase {
		return 0
	}
	return n
}

// protectStreaks sweeps every subscriber at local midnight, spending one saver to
// fill yesterday for anyone whose live streak would otherwise break.
func protectStreaks(store *Store, notifier telegram.Notifier, now time.Time) {
	chats, err := store.Subscribers()
	if err != nil {
		log.Printf("❌ [STREAK] Could not read subscribers: %v", err)
		return
	}
	saved := 0
	for _, chatID := range chats {
		if protectStreak(store, notifier, chatID, now) {
			saved++
		}
	}
	if saved > 0 {
		log.Printf("🧊 [STREAK] Protected %d streak(s) with savers.", saved)
	}
}

// protectStreak rescues one user's streak when yesterday was missed but the day
// before was active and they hold a saver. Returns true when it acted. It is
// naturally idempotent: once yesterday is filled, a re-run is a no-op.
func protectStreak(store *Store, notifier telegram.Notifier, chatID int64, now time.Time) bool {
	local := now.In(config.AppLocation)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, config.AppLocation)
	yesterday := today.AddDate(0, 0, -1)
	dayBefore := today.AddDate(0, 0, -2)

	counts, err := store.activityDays(chatID)
	if err != nil {
		return false
	}
	const layout = "2006-01-02"
	if counts[yesterday.Format(layout)] > 0 {
		return false // yesterday was active; nothing to rescue
	}
	if counts[dayBefore.Format(layout)] == 0 {
		return false // no live streak to protect
	}
	if store.GetStreakFreezes(chatID) <= 0 {
		return false
	}
	bal, err := store.AddStreakFreezes(chatID, -1)
	if err != nil {
		return false
	}
	if err := store.RecordActivity(chatID, yesterday); err != nil {
		return false
	}
	_ = notifier.Send(chatID, fmt.Sprintf("🧊 <b>Streak saved!</b> You missed yesterday, so I spent one of your "+
		"streak savers to keep your run alive. %d saver%s left.", bal, plural(bal)))
	return true
}
