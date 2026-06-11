package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Leitner decks — curated vocabulary decks studied with a 5-box Leitner system.
//
// Cards live in deck_cards (seeded once from embedded JSON). Per-user progress
// lives in leitner_progress: each card sits in a box 1–5; a correct recall
// promotes it one box (longer interval before it returns), a miss resets it to
// box 1. A card in box 5 is "mastered". Per-deck progress is surfaced as a
// percentage in the Mini App.
// ---------------------------------------------------------------------------

// leitnerBoxDays maps a box (1–5) to the days until the card is due again.
var leitnerBoxDays = map[int]int{1: 0, 2: 1, 3: 3, 4: 7, 5: 21}

const (
	leitnerMaxBox = 5
	// deckBackfillInterval paces the background example-generation worker.
	deckBackfillInterval = 30 * time.Second
)

// DeckMeta describes a curated deck. Cards are loaded from the embedded `file`.
type DeckMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	file        string
}

// deckRegistry lists the curated decks bundled with the bot. Adding a deck is a
// new embedded JSON file plus one entry here.
//
// NOTE: the famous "504 Absolutely Essential Words" deck is intentionally absent
// until a complete, authentic source is available — the only machine-readable
// copy found online holds just 156 of the 504 words. Drop webapp/decks/504.json
// in and add its entry here to enable it.
var deckRegistry = []DeckMeta{
	{
		ID:          "barrons",
		Name:        "Barron's GRE 333",
		Description: "High-frequency GRE vocabulary from Barron's classic list.",
		file:        "webapp/decks/barrons.json",
	},
}

// deckCard mirrors one entry in an embedded deck JSON file.
type deckCard struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
	Example    string `json:"example"`
	Group      string `json:"group"`
}

// DeckProgress is the per-user summary shown on the Decks tab.
type DeckProgress struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Total       int    `json:"total"`
	Mastered    int    `json:"mastered"`
	Due         int    `json:"due"`
	ProgressPct int    `json:"progressPct"`
}

// DeckStudyCard is one card delivered to a study session.
type DeckStudyCard struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
	Example    string `json:"example"`
	Box        int    `json:"box"`
}

// leitnerNextBox returns the next box after an answer. Correct recall promotes
// one box (capped at leitnerMaxBox); a miss resets to box 1.
func leitnerNextBox(box int, known bool) int {
	if !known {
		return 1
	}
	if box < 1 {
		box = 1
	}
	if box+1 > leitnerMaxBox {
		return leitnerMaxBox
	}
	return box + 1
}

// ---------------------------------------------------------------------------
// Seeding
// ---------------------------------------------------------------------------

// SeedDecks loads each registered deck's embedded JSON into deck_cards. It is
// idempotent (INSERT OR IGNORE on the deck_id+term key), so backfilled examples
// and existing rows are preserved across restarts.
func (s *Store) SeedDecks() error {
	for _, d := range deckRegistry {
		raw, err := webappFiles.ReadFile(d.file)
		if err != nil {
			log.Printf("⚠️  [DECKS] Could not read embedded deck %q: %v", d.ID, err)
			continue
		}
		var cards []deckCard
		if err := json.Unmarshal(raw, &cards); err != nil {
			log.Printf("⚠️  [DECKS] Could not parse deck %q: %v", d.ID, err)
			continue
		}
		inserted := 0
		for i, c := range cards {
			term := strings.ToLower(strings.TrimSpace(c.Term))
			if term == "" {
				continue
			}
			res, err := s.db.Exec(`
				INSERT OR IGNORE INTO deck_cards (deck_id, term, definition, example, group_label, ordering)
				VALUES (?, ?, ?, ?, ?, ?)`,
				d.ID, term, c.Definition, c.Example, c.Group, i,
			)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
			}
		}
		if inserted > 0 {
			log.Printf("📚 [DECKS] Seeded %d new card(s) into deck %q.", inserted, d.ID)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Store methods
// ---------------------------------------------------------------------------

// Decks returns the per-user progress summary for every registered deck.
func (s *Store) Decks(chatID int64) ([]DeckProgress, error) {
	now := time.Now().UTC().Format(srsTimeLayout)
	out := make([]DeckProgress, 0, len(deckRegistry))
	for _, d := range deckRegistry {
		var total int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM deck_cards WHERE deck_id = ?", d.ID).Scan(&total); err != nil {
			return nil, err
		}
		if total == 0 {
			continue // deck has no cards seeded
		}

		// Box distribution → progress points (box-1, capped 0..4) and mastered.
		points, mastered := 0, 0
		rows, err := s.db.Query(
			"SELECT box, COUNT(*) FROM leitner_progress WHERE chat_id = ? AND deck_id = ? GROUP BY box",
			chatID, d.ID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var box, cnt int
			if err := rows.Scan(&box, &cnt); err != nil {
				rows.Close()
				return nil, err
			}
			if box > 1 {
				points += (box - 1) * cnt
			}
			if box >= leitnerMaxBox {
				mastered += cnt
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}

		var due int
		_ = s.db.QueryRow(`
			SELECT COUNT(*) FROM deck_cards dc
			LEFT JOIN leitner_progress lp
			  ON lp.chat_id = ? AND lp.deck_id = dc.deck_id AND lp.term = dc.term
			WHERE dc.deck_id = ? AND (lp.term IS NULL OR lp.due_at <= ?)`,
			chatID, d.ID, now,
		).Scan(&due)

		pct := 0
		if total > 0 {
			pct = points * 100 / (total * (leitnerMaxBox - 1))
		}
		out = append(out, DeckProgress{
			ID: d.ID, Name: d.Name, Description: d.Description,
			Total: total, Mastered: mastered, Due: due, ProgressPct: pct,
		})
	}
	return out, nil
}

// DeckStudy returns up to limit cards to study now: due cards first (oldest due),
// then never-seen cards in deck order.
func (s *Store) DeckStudy(chatID int64, deckID string, limit int) ([]DeckStudyCard, error) {
	now := time.Now().UTC().Format(srsTimeLayout)
	rows, err := s.db.Query(`
		SELECT dc.term, dc.definition, dc.example, COALESCE(lp.box, 0)
		FROM deck_cards dc
		LEFT JOIN leitner_progress lp
		  ON lp.chat_id = ? AND lp.deck_id = dc.deck_id AND lp.term = dc.term
		WHERE dc.deck_id = ? AND (lp.term IS NULL OR lp.due_at <= ?)
		ORDER BY (lp.term IS NULL) ASC, lp.due_at ASC, dc.ordering ASC
		LIMIT ?`,
		chatID, deckID, now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DeckStudyCard
	for rows.Next() {
		var c DeckStudyCard
		if err := rows.Scan(&c.Term, &c.Definition, &c.Example, &c.Box); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeckSwipe records a study answer for a card and reschedules it via Leitner.
func (s *Store) DeckSwipe(chatID int64, deckID, term string, known bool, now time.Time) error {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return fmt.Errorf("empty term")
	}
	// Current box (default 1 for a never-seen card).
	box := 1
	_ = s.db.QueryRow(
		"SELECT box FROM leitner_progress WHERE chat_id = ? AND deck_id = ? AND term = ?",
		chatID, deckID, term,
	).Scan(&box)

	next := leitnerNextBox(box, known)
	due := now.UTC().AddDate(0, 0, leitnerBoxDays[next]).Format(srsTimeLayout)
	_, err := s.db.Exec(`
		INSERT INTO leitner_progress (chat_id, deck_id, term, box, due_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id, deck_id, term)
		DO UPDATE SET box = excluded.box, due_at = excluded.due_at, updated_at = CURRENT_TIMESTAMP`,
		chatID, deckID, term, next, due,
	)
	return err
}

// ---------------------------------------------------------------------------
// Background example backfill
// ---------------------------------------------------------------------------

// runDeckBackfill periodically generates a natural example sentence for deck
// cards that don't have one (e.g. Barron's, which ships definitions only),
// caching the result back into deck_cards. It is a no-op when no AI provider is
// configured. Cards that already have an example (e.g. the 504 set) are skipped.
func runDeckBackfill(ctx context.Context, chain *ProviderChain, store *Store) {
	if chain == nil || !chain.HasAny() {
		return
	}
	log.Println("📚 [DECKS] Example backfill worker started.")
	ticker := time.NewTicker(deckBackfillInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.backfillOneDeckExample(ctx, chain)
		}
	}
}

// backfillOneDeckExample fills in a single missing example, keeping AI usage low.
func (s *Store) backfillOneDeckExample(ctx context.Context, chain *ProviderChain) {
	var deckID, term, definition string
	err := s.db.QueryRow(
		"SELECT deck_id, term, definition FROM deck_cards WHERE example = '' LIMIT 1",
	).Scan(&deckID, &term, &definition)
	if err != nil {
		return // none left, or transient error
	}
	prompt := fmt.Sprintf(
		"Write ONE natural English example sentence (max 18 words) that uses the word %q "+
			"in the sense of \"%s\". Return only the sentence, no quotes or labels.",
		term, definition,
	)
	out, _, err := chain.Generate(ctx, prompt)
	if err != nil {
		return
	}
	example := strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "\"'"))
	if example == "" {
		return
	}
	if _, err := s.db.Exec(
		"UPDATE deck_cards SET example = ? WHERE deck_id = ? AND term = ?",
		example, deckID, term,
	); err != nil {
		log.Printf("⚠️  [DECKS] Could not cache example for %q: %v", term, err)
	}
}
