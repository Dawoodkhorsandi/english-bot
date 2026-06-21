package app

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Forward-to-deck (#4)
//
// When a user forwards or pastes a chunk of English (an article, a paragraph,
// a song), the bot mines its less-common words, looks each up in the offline
// English–Persian dictionary, and builds a personal Leitner deck — turning
// material the learner actually cares about into spaced-repetition cards. User
// decks live in user_decks; their cards reuse the deck_cards/leitner_progress
// machinery under a per-user deck_id ("u<chatID>_<n>").
// ---------------------------------------------------------------------------

const (
	// forwardDeckMinWords is the word count above which a plain message is treated
	// as deck material rather than a single-word lookup.
	forwardDeckMinWords = 12
	// forwardDeckMaxCards caps how many cards one forward produces.
	forwardDeckMaxCards = 25
)

// userDeckMetas returns DeckMeta for each of a user's created decks.
func (s *Store) userDeckMetas(chatID int64) []DeckMeta {
	rows, err := s.db.Query("SELECT deck_id, name FROM user_decks WHERE chat_id = ? ORDER BY created_at ASC", chatID)
	if err != nil {
		log.Printf("⚠️  [USERDECK] Could not list decks for chat %d: %v", chatID, err)
		return nil
	}
	defer rows.Close()
	var out []DeckMeta
	for rows.Next() {
		var m DeckMeta
		if err := rows.Scan(&m.ID, &m.Name); err != nil {
			return out
		}
		m.Description = "Your deck — built from text you sent."
		out = append(out, m)
	}
	return out
}

// allDeckMetas returns the curated decks followed by the user's own decks.
func (s *Store) allDeckMetas(chatID int64) []DeckMeta {
	metas := make([]DeckMeta, 0, len(deckRegistry)+2)
	metas = append(metas, deckRegistry...)
	metas = append(metas, s.userDeckMetas(chatID)...)
	return metas
}

// deckMeta resolves a deck id to its metadata, checking curated decks first and
// then the user's own decks.
func (s *Store) deckMeta(chatID int64, deckID string) (DeckMeta, bool) {
	for _, d := range deckRegistry {
		if d.ID == deckID {
			return d, true
		}
	}
	for _, d := range s.userDeckMetas(chatID) {
		if d.ID == deckID {
			return d, true
		}
	}
	return DeckMeta{}, false
}

// CreateUserDeck stores a new user deck and its cards, returning the new deck id
// and the number of cards added.
func (s *Store) CreateUserDeck(chatID int64, name string, cards []deckCard, now time.Time) (string, int, error) {
	if len(cards) == 0 {
		return "", 0, fmt.Errorf("no cards")
	}
	var seq int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM user_decks WHERE chat_id = ?", chatID).Scan(&seq)
	deckID := fmt.Sprintf("u%d_%d", chatID, seq+1)

	if _, err := s.db.Exec(
		"INSERT INTO user_decks (chat_id, deck_id, name) VALUES (?, ?, ?)",
		chatID, deckID, name,
	); err != nil {
		return "", 0, err
	}
	n := 0
	for i, c := range cards {
		if _, err := s.db.Exec(`
			INSERT OR IGNORE INTO deck_cards
				(deck_id, term, definition, example, group_label, ordering, persian, pronunciation, frequency_rank)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			deckID, c.Term, c.Definition, c.Example, name, i, c.Persian, c.Pronunciation, i+1,
		); err != nil {
			return deckID, n, err
		}
		n++
	}
	return deckID, n, nil
}

// commonWords are high-frequency words skipped when mining a deck — learners
// already know these, so they'd only dilute the deck.
var commonWords = func() map[string]bool {
	list := strings.Fields(`a an the and or but if then else of to in on at by for with from as is are was were be been being
		this that these those it its he she they we you i him her them his hers their our your my me us do does did done
		have has had having will would shall should can could may might must not no yes so than too very just about into out
		up down over under again more most some any all each every other such own same not only also then once here there
		when where why how what which who whom whose while because between among through during before after above below
		off near upon per via amid like get got make made go went come came see saw say said know knew think thought take took`)
	m := make(map[string]bool, len(list))
	for _, w := range list {
		m[w] = true
	}
	return m
}()

// mineDeckTerms extracts candidate vocabulary from free text: lowercased alpha
// tokens, 4+ letters, not in the common-word stoplist, de-duplicated. The result
// is sorted longest-first as a rough rarity proxy and capped.
func mineDeckTerms(text string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\'')
	}) {
		w := strings.ToLower(strings.Trim(raw, "'"))
		if len(w) < 4 || seen[w] || commonWords[w] {
			continue
		}
		// Skip tokens with non-letters left over (e.g. contractions' remnants).
		allAlpha := true
		for _, r := range w {
			if r < 'a' || r > 'z' {
				allAlpha = false
				break
			}
		}
		if !allAlpha {
			continue
		}
		seen[w] = true
		terms = append(terms, w)
	}
	// Longer words tend to be rarer / more worth learning — surface them first.
	sort.SliceStable(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
	return terms
}

// handleBuildDeck mines a forwarded/pasted block of text into a personal deck,
// resolving each candidate word against the offline dictionary so cards carry a
// real definition and Persian gloss with no AI call.
func handleBuildDeck(store *Store, notifier telegram.Notifier, chatID int64, text string) {
	terms := mineDeckTerms(text)
	if len(terms) == 0 {
		_ = notifier.Send(chatID, "🤔 I couldn't find words to learn in that. Try sending a longer English passage.")
		return
	}

	var cards []deckCard
	for _, t := range terms {
		entries := store.DictLookup(t)
		if len(entries) == 0 {
			continue
		}
		e := entries[0]
		cards = append(cards, deckCard{
			Term:          t,
			Definition:    e.Definition,
			Example:       e.Example,
			Persian:       e.Persian,
			Pronunciation: e.Pronunciation,
		})
		if len(cards) >= forwardDeckMaxCards {
			break
		}
	}
	if len(cards) == 0 {
		_ = notifier.Send(chatID, "🤔 I found some words but couldn't define them. Try another passage.")
		return
	}

	name := fmt.Sprintf("My deck #%d", store.userDeckCount(chatID)+1)
	deckID, n, err := store.CreateUserDeck(chatID, name, cards, time.Now())
	if err != nil {
		log.Printf("❌ [USERDECK] CreateUserDeck for chat %d failed: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ Sorry, I couldn't build that deck right now. Please try again.")
		return
	}
	log.Printf("📦 [USERDECK] Built deck %s (%d cards) for chat %d.", deckID, n, chatID)

	msg := fmt.Sprintf("📦 <b>%s</b> is ready!\n\nI pulled <b>%d</b> words worth learning from your text and "+
		"added them to a new deck with definitions and Persian meanings. Open the app to study it with spaced repetition.", name, n)
	if config.WebAppURL != "" {
		kb := [][]telegram.InlineButton{{{
			Text:   "📱 Study in the app",
			WebApp: &telegram.WebAppInfo{URL: config.WebAppURL},
		}}}
		_ = notifier.SendKeyboard(chatID, msg, kb)
	} else {
		_ = notifier.Send(chatID, msg)
	}
}

// userDeckCount returns how many decks a user has created.
func (s *Store) userDeckCount(chatID int64) int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM user_decks WHERE chat_id = ?", chatID).Scan(&n)
	return n
}
