package app

import (
	"testing"
	"time"
)

// TestMineDeckTerms verifies common/short words are dropped and rarer words kept.
func TestMineDeckTerms(t *testing.T) {
	text := "The cat sat on the comfortable mat near the extraordinary windowsill."
	terms := mineDeckTerms(text)
	got := map[string]bool{}
	for _, w := range terms {
		got[w] = true
	}
	if got["the"] || got["on"] || got["cat"] || got["sat"] {
		t.Fatalf("kept a common/short word: %v", terms)
	}
	if !got["comfortable"] || !got["extraordinary"] || !got["windowsill"] {
		t.Fatalf("dropped a worthwhile word: %v", terms)
	}
	// Longest-first ordering (rarity proxy).
	if terms[0] != "extraordinary" {
		t.Fatalf("not longest-first: %v", terms)
	}
}

// TestCreateUserDeck verifies a user deck is created, listed, and studyable.
func TestCreateUserDeck(t *testing.T) {
	store, err := openStore(t.TempDir() + "/userdeck.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer store.Close()

	const chatID = int64(3)
	cards := []deckCard{
		{Term: "ubiquitous", Definition: "present everywhere", Persian: "همه‌جا حاضر"},
		{Term: "ephemeral", Definition: "short-lived", Persian: "زودگذر"},
	}
	deckID, n, err := store.CreateUserDeck(chatID, "My deck #1", cards, time.Now())
	if err != nil || n != 2 {
		t.Fatalf("CreateUserDeck: id=%q n=%d err=%v", deckID, n, err)
	}

	// deckMeta resolves the user deck; another user can't see it.
	if _, ok := store.deckMeta(chatID, deckID); !ok {
		t.Fatal("deckMeta did not resolve the user deck")
	}
	if _, ok := store.deckMeta(int64(999), deckID); ok {
		t.Fatal("user deck leaked to another user")
	}

	// It appears in the user's deck list with the right card count.
	decks, err := store.Decks(chatID)
	if err != nil {
		t.Fatalf("Decks: %v", err)
	}
	var found *DeckProgress
	for i := range decks {
		if decks[i].ID == deckID {
			found = &decks[i]
		}
	}
	if found == nil || found.Total != 2 {
		t.Fatalf("user deck missing/wrong in Decks(): %+v", decks)
	}

	// Its cards are studyable.
	study, err := store.DeckStudy(chatID, deckID, 10)
	if err != nil || len(study) != 2 {
		t.Fatalf("DeckStudy: n=%d err=%v", len(study), err)
	}
}
