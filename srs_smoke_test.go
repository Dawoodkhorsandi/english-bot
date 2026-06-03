package main

import (
	"testing"
	"time"
)

// TestSrsKnownProgression verifies the interval grows on repeated correct recalls
// and the ease nudges up.
func TestSrsKnownProgression(t *testing.T) {
	interval, ease, reps := 1, srsDefaultEase, 0

	// First "known": reps→1, interval→1.
	interval, ease, reps = srsKnown(interval, ease, reps)
	if reps != 1 || interval != 1 {
		t.Fatalf("after 1st known: interval=%d reps=%d, want 1/1", interval, reps)
	}
	// Second: reps→2, interval→3.
	interval, ease, reps = srsKnown(interval, ease, reps)
	if reps != 2 || interval != 3 {
		t.Fatalf("after 2nd known: interval=%d reps=%d, want 3/2", interval, reps)
	}
	// Third: interval should grow by ~ease (round(3*ease)).
	prev := interval
	interval, ease, reps = srsKnown(interval, ease, reps)
	if interval <= prev {
		t.Fatalf("after 3rd known: interval=%d, want > %d", interval, prev)
	}
	if ease <= srsDefaultEase {
		t.Fatalf("ease did not grow: %v", ease)
	}
}

// TestSrsForgotResets verifies a failed recall resets the interval and floors ease.
func TestSrsForgotResets(t *testing.T) {
	interval, ease, reps := srsForgot(srsMinEase)
	if interval != 1 || reps != 0 {
		t.Fatalf("forgot: interval=%d reps=%d, want 1/0", interval, reps)
	}
	if ease < srsMinEase {
		t.Fatalf("ease %v below floor %v", ease, srsMinEase)
	}
	// Ease never drops below the floor.
	if _, e, _ := srsForgot(srsMinEase); e != srsMinEase {
		t.Fatalf("ease floor not respected: %v", e)
	}
}

// TestReviewScheduleLifecycle exercises seed → due → grade against a real DB.
func TestReviewScheduleLifecycle(t *testing.T) {
	store, err := openStore(t.TempDir() + "/srs.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(42)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	if err := store.SeedReview(chatID, "Serendipity", now); err != nil {
		t.Fatalf("SeedReview: %v", err)
	}
	// Seeding is idempotent — a second seed must not reset or duplicate.
	if err := store.SeedReview(chatID, "serendipity", now); err != nil {
		t.Fatalf("SeedReview (2nd): %v", err)
	}

	// Not due yet (due_at is one day out).
	if due, err := store.DueReviews(chatID, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("DueReviews now: got %d (err %v), want 0", len(due), err)
	}

	// Due a day and a bit later.
	later := now.AddDate(0, 0, 1).Add(time.Minute)
	due, err := store.DueReviews(chatID, later, 10)
	if err != nil {
		t.Fatalf("DueReviews later: %v", err)
	}
	if len(due) != 1 || due[0].term != "serendipity" {
		t.Fatalf("DueReviews later: got %+v, want [serendipity]", due)
	}

	// Grade as known — must promote and push due_at out so it's not due again now.
	ok, err := store.ApplyReviewKnown(chatID, "serendipity", later)
	if err != nil || !ok {
		t.Fatalf("ApplyReviewKnown: ok=%v err=%v", ok, err)
	}
	if due, err := store.DueReviews(chatID, later, 10); err != nil || len(due) != 0 {
		t.Fatalf("DueReviews after known: got %d (err %v), want 0", len(due), err)
	}

	// Grading an unscheduled word reports ok=false (stale button).
	if ok, err := store.ApplyReviewForgot(chatID, "ghostword", later); err != nil || ok {
		t.Fatalf("ApplyReviewForgot unknown: ok=%v err=%v, want false/nil", ok, err)
	}
}

// TestRecordSentForSeedsReview verifies sending a word enrols it for review but
// sending a drill verb does not.
func TestRecordSentForSeedsReview(t *testing.T) {
	store, err := openStore(t.TempDir() + "/seed.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(7)
	if err := store.recordSentFor(kindWord, chatID, "ephemeral"); err != nil {
		t.Fatalf("recordSentFor word: %v", err)
	}
	if err := store.recordSentFor(kindDrill, chatID, "run"); err != nil {
		t.Fatalf("recordSentFor drill: %v", err)
	}

	if _, _, _, ok, err := store.getReview(chatID, "ephemeral"); err != nil || !ok {
		t.Fatalf("word not scheduled: ok=%v err=%v", ok, err)
	}
	if _, _, _, ok, err := store.getReview(chatID, "run"); err != nil || ok {
		t.Fatalf("drill verb should not be scheduled: ok=%v err=%v", ok, err)
	}
}
