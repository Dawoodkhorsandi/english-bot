package main

import (
	"strings"
	"testing"
	"time"
)

func TestChallengeSessionStoreLifecycle(t *testing.T) {
	store, err := openStore(t.TempDir() + "/challenge.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(77)
	if err := store.StartChallenge(chatID, time.Now()); err != nil {
		t.Fatalf("StartChallenge: %v", err)
	}
	idx, correct, active, err := store.GetChallenge(chatID)
	if err != nil {
		t.Fatalf("GetChallenge: %v", err)
	}
	if !active || idx != 0 || correct != 0 {
		t.Fatalf("GetChallenge = active=%v idx=%d correct=%d, want true/0/0", active, idx, correct)
	}

	for i := 0; i < 4; i++ {
		idx, correct, done, err := store.AdvanceChallenge(chatID, true)
		if err != nil {
			t.Fatalf("AdvanceChallenge step %d: %v", i, err)
		}
		if done {
			t.Fatalf("done=true too early at step %d", i)
		}
		if idx != i+1 || correct != i+1 {
			t.Fatalf("step %d => idx=%d correct=%d, want idx=%d correct=%d", i, idx, correct, i+1, i+1)
		}
	}
	idx, correct, done, err := store.AdvanceChallenge(chatID, false)
	if err != nil {
		t.Fatalf("AdvanceChallenge final: %v", err)
	}
	if !done || idx != 5 || correct != 4 {
		t.Fatalf("final => done=%v idx=%d correct=%d, want true/5/4", done, idx, correct)
	}
	if err := store.ClearChallenge(chatID); err != nil {
		t.Fatalf("ClearChallenge: %v", err)
	}
	_, _, active, err = store.GetChallenge(chatID)
	if err != nil {
		t.Fatalf("GetChallenge after clear: %v", err)
	}
	if active {
		t.Fatal("expected no active challenge after clear")
	}
}

func TestHandleChallengeFlowAndStats(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	const chatID = int64(99)
	store.AddSubscriber(chatID)

	words := []struct {
		term, meaning string
	}{
		{"tedious", "boring"},
		{"vivid", "bright"},
		{"ample", "enough"},
		{"serene", "calm"},
		{"brisk", "quick"},
	}
	for _, w := range words {
		store.AddToPool(kindWord, defaultLevel, w.term, w.meaning, "card for "+w.term)
		store.recordSentFor(kindWord, chatID, w.term)
	}

	handleChallenge(store, mock, chatID)
	if len(mock.keyboard) == 0 {
		t.Fatal("expected first challenge question keyboard")
	}
	if !strings.Contains(mock.keyboard[0].text, "Question 1/5") {
		t.Fatalf("first challenge prompt missing question index: %q", mock.keyboard[0].text)
	}

	for i := 0; i < 5; i++ {
		handleCallback(store, mock, &TelegramCallbackQuery{
			ID:      "cb",
			Data:    "chal:c:tedious",
			Message: &TelegramMessage{MessageID: 10 + int64(i), Chat: TelegramChat{ID: chatID}},
		})
	}

	if !strings.Contains(mock.lastSentText(), "Score: <b>5/5</b>") {
		t.Fatalf("final summary missing score, got %q", mock.lastSentText())
	}
	if !strings.Contains(mock.lastSentText(), "Perfect score") {
		t.Fatalf("final summary missing encouragement, got %q", mock.lastSentText())
	}
	completed, best, err := store.ChallengeStats(chatID)
	if err != nil {
		t.Fatalf("ChallengeStats: %v", err)
	}
	if completed != 1 || best != 5 {
		t.Fatalf("ChallengeStats = completed=%d best=%d, want 1/5", completed, best)
	}
}
