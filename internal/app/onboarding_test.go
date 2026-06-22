package app

import (
	"testing"
	"time"
)

// seedOnboardingCards inserts a few frequency-ranked 504 cards for testing.
func seedOnboardingCards(t *testing.T, store *Store) {
	t.Helper()
	cards := []struct {
		term string
		rank int
	}{
		{"abandon", 3},
		{"keen", 1},
		{"wary", 2},
	}
	for i, c := range cards {
		if _, err := store.db.Exec(
			`INSERT INTO deck_cards (deck_id, term, definition, ordering, frequency_rank)
			 VALUES (?, ?, ?, ?, ?)`,
			onboardingDeck, c.term, "def of "+c.term, i, c.rank,
		); err != nil {
			t.Fatalf("seed card %s: %v", c.term, err)
		}
	}
}

// TestOnboardingFlow verifies common words come back frequency-ordered and that
// marking some known files them away so they no longer appear or study.
func TestOnboardingFlow(t *testing.T) {
	store, err := openStore(t.TempDir() + "/onboard.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()
	seedOnboardingCards(t, store)

	const chatID = int64(5)

	words, err := store.OnboardingWords(chatID, 10)
	if err != nil {
		t.Fatalf("OnboardingWords: %v", err)
	}
	if len(words) != 3 {
		t.Fatalf("got %d words, want 3", len(words))
	}
	// Frequency rank 1 ("keen") must come first.
	if words[0].Term != "keen" {
		t.Fatalf("not frequency-ordered: first = %q, want keen", words[0].Term)
	}

	if err := store.MarkDeckKnown(chatID, []string{"keen", "wary"}, time.Now()); err != nil {
		t.Fatalf("MarkDeckKnown: %v", err)
	}
	if !store.IsOnboarded(chatID) {
		// MarkDeckKnown doesn't set the flag; the handler does. Ensure setter works.
		if err := store.SetOnboarded(chatID); err != nil {
			t.Fatal(err)
		}
	}

	// Known words are gone from the onboarding batch.
	left, err := store.OnboardingWords(chatID, 10)
	if err != nil {
		t.Fatalf("OnboardingWords (2nd): %v", err)
	}
	if len(left) != 1 || left[0].Term != "abandon" {
		t.Fatalf("after marking known, got %+v, want [abandon]", left)
	}

	// Known words are mastered (box 5), so study skips them.
	study, err := store.DeckStudy(chatID, onboardingDeck, 10)
	if err != nil {
		t.Fatalf("DeckStudy: %v", err)
	}
	for _, c := range study {
		if c.Term == "keen" || c.Term == "wary" {
			t.Fatalf("known word %q surfaced in study", c.Term)
		}
	}
}
