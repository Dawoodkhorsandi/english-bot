package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/ai"
	"github.com/Dawoodkhorsandi/english-bot/internal/config"
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
// Exam tags a deck to an exam track ("ielts"/"toefl"); "" is a general deck.
type DeckMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Exam        string `json:"exam,omitempty"`
	file        string
}

// deckRegistry lists the curated decks bundled with the bot. Adding a deck is a
// new embedded JSON file plus one entry here.
var deckRegistry = []DeckMeta{
	{
		ID:          "504",
		Name:        "504 Absolutely Essential Words",
		Description: "The classic 504 high-utility English words, grouped into 42 lessons.",
		file:        "webapp/decks/504.json",
	},
	{
		ID:          "barrons",
		Name:        "Barron's GRE 333",
		Description: "High-frequency GRE vocabulary from Barron's classic list, with memory mnemonics.",
		file:        "webapp/decks/barrons.json",
	},
	{
		ID:          "phrasalverbs",
		Name:        "Common Phrasal Verbs",
		Description: "Everyday English phrasal verbs (get by, look after, turn down…) with examples.",
		file:        "webapp/decks/phrasalverbs.json",
	},
	{
		ID:          "business",
		Name:        "Business & Workplace English",
		Description: "Meetings, email and negotiation vocabulary (stakeholder, deadline, leverage…).",
		file:        "webapp/decks/business.json",
	},
	{
		ID:          "awl",
		Name:        "Academic Word List",
		Description: "Core academic vocabulary for essays, IELTS and TOEFL (analyze, significant…).",
		file:        "webapp/decks/awl.json",
	},
	{
		ID:          "ielts",
		Name:        "IELTS High-Frequency",
		Description: "Common IELTS vocabulary (abundant, deteriorate, prominent…) with examples.",
		Exam:        "ielts",
		file:        "webapp/decks/ielts.json",
	},
	{
		ID:          "toefl",
		Name:        "TOEFL High-Frequency",
		Description: "Common TOEFL vocabulary (hypothesis, fluctuate, comprehensive…) with examples.",
		Exam:        "toefl",
		file:        "webapp/decks/toefl.json",
	},
	{
		ID:          "idioms",
		Name:        "Common English Idioms",
		Description: "Everyday English idioms (break the ice, piece of cake…) with meanings and examples.",
		file:        "webapp/decks/idioms.json",
	},
	{
		ID:          "travel",
		Name:        "Travel & Survival English",
		Description: "Airport, hotel, directions, restaurant and emergency essentials for travellers.",
		file:        "webapp/decks/travel.json",
	},
	{
		ID:          "confusing",
		Name:        "Commonly Confused Words",
		Description: "affect/effect, fewer/less and other classic English mix-ups, side by side.",
		file:        "webapp/decks/confusing.json",
	},
	{
		ID:          "irregular",
		Name:        "Irregular Verbs",
		Description: "The essential English irregular verbs with their past and past participle.",
		file:        "webapp/decks/irregular.json",
	},
	{
		ID:          "everyday",
		Name:        "Everyday Spoken English",
		Description: "High-frequency words and expressions for natural daily conversation.",
		file:        "webapp/decks/everyday.json",
	},
	{
		ID:          "interview",
		Name:        "Job Interview English",
		Description: "Vocabulary and phrases for interviews and the workplace.",
		file:        "webapp/decks/interview.json",
	},
}

// examDeckFor returns the curated deck id for an exam target ("" if none).
func examDeckFor(target string) (id, name string) {
	for _, d := range deckRegistry {
		if d.Exam != "" && d.Exam == normalizeExamTarget(target) {
			return d.ID, d.Name
		}
	}
	return "", ""
}

// deckCard mirrors one entry in an embedded deck JSON file. persian /
// pronunciation / mnemonic are optional in the source JSON; blanks are filled
// later by the backfill worker (persian/pronunciation) or simply omitted.
type deckCard struct {
	Term          string `json:"term"`
	Definition    string `json:"definition"`
	Example       string `json:"example"`
	Group         string `json:"group"`
	Persian       string `json:"persian"`
	Pronunciation string `json:"pronunciation"`
	Mnemonic      string `json:"mnemonic"`
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
	Term          string `json:"term"`
	Definition    string `json:"definition"`
	Example       string `json:"example"`
	Persian       string `json:"persian"`
	Pronunciation string `json:"pronunciation"`
	Mnemonic      string `json:"mnemonic"`
	Box           int    `json:"box"`
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
			// UPSERT that only fills BLANK columns from the JSON: new fields
			// (persian/pronunciation/mnemonic) reach already-seeded rows on
			// existing databases, without ever clobbering AI-backfilled values.
			res, err := s.db.Exec(`
				INSERT INTO deck_cards (deck_id, term, definition, example, group_label, ordering, persian, pronunciation, mnemonic)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(deck_id, term) DO UPDATE SET
					definition    = CASE WHEN deck_cards.definition = ''    THEN excluded.definition    ELSE deck_cards.definition END,
					example       = CASE WHEN deck_cards.example = ''       THEN excluded.example       ELSE deck_cards.example END,
					group_label   = CASE WHEN deck_cards.group_label = ''   THEN excluded.group_label   ELSE deck_cards.group_label END,
					persian       = CASE WHEN deck_cards.persian = ''       THEN excluded.persian       ELSE deck_cards.persian END,
					pronunciation = CASE WHEN deck_cards.pronunciation = '' THEN excluded.pronunciation ELSE deck_cards.pronunciation END,
					mnemonic      = CASE WHEN deck_cards.mnemonic = ''      THEN excluded.mnemonic      ELSE deck_cards.mnemonic END`,
				d.ID, term, c.Definition, c.Example, c.Group, i, c.Persian, c.Pronunciation, c.Mnemonic,
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

// Decks returns the per-user progress summary for every registered deck,
// curated and user-created (forward-to-deck), with cards seeded.
func (s *Store) Decks(chatID int64) ([]DeckProgress, error) {
	now := time.Now().UTC().Format(srsTimeLayout)
	metas := s.allDeckMetas(chatID)
	out := make([]DeckProgress, 0, len(metas))
	for _, d := range metas {
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
		SELECT dc.term, dc.definition, dc.example,
		       COALESCE(dc.persian, ''), COALESCE(dc.pronunciation, ''), COALESCE(dc.mnemonic, ''),
		       COALESCE(lp.box, 0)
		FROM deck_cards dc
		LEFT JOIN leitner_progress lp
		  ON lp.chat_id = ? AND lp.deck_id = dc.deck_id AND lp.term = dc.term
		WHERE dc.deck_id = ? AND (lp.term IS NULL OR lp.due_at <= ?)
		ORDER BY (lp.term IS NULL) ASC, lp.due_at ASC,
		         (CASE WHEN dc.frequency_rank > 0 THEN dc.frequency_rank ELSE 999999 END) ASC,
		         dc.ordering ASC
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
		if err := rows.Scan(&c.Term, &c.Definition, &c.Example, &c.Persian, &c.Pronunciation, &c.Mnemonic, &c.Box); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeckBox is one bucket of the Leitner box distribution for a deck.
type DeckBox struct {
	Box   int    `json:"box"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// DeckDetail is the per-user deep view of a single deck.
type DeckDetail struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Total       int       `json:"total"`
	Mastered    int       `json:"mastered"`
	Due         int       `json:"due"`
	New         int       `json:"new"` // never-studied cards
	ProgressPct int       `json:"progressPct"`
	NextReview  string    `json:"nextReview"` // "2 Jan", or "" if none scheduled
	Boxes       []DeckBox `json:"boxes"`      // box 1..5 distribution (studied cards)
}

// leitnerBoxLabels names the five Leitner boxes for the detail view.
var leitnerBoxLabels = map[int]string{1: "Learning", 2: "Familiar", 3: "Comfortable", 4: "Confident", 5: "Mastered"}

// DeckDetail returns the box distribution + summary stats for one deck and user.
func (s *Store) DeckDetail(chatID int64, deckID string) (DeckDetail, bool, error) {
	meta, ok := s.deckMeta(chatID, deckID)
	if !ok {
		return DeckDetail{}, false, nil
	}
	det := DeckDetail{ID: meta.ID, Name: meta.Name, Description: meta.Description}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM deck_cards WHERE deck_id = ?", deckID).Scan(&det.Total); err != nil {
		return det, false, err
	}
	if det.Total == 0 {
		return det, false, nil
	}

	counts := map[int]int{}
	studied, points := 0, 0
	rows, err := s.db.Query(
		"SELECT box, COUNT(*) FROM leitner_progress WHERE chat_id = ? AND deck_id = ? GROUP BY box",
		chatID, deckID,
	)
	if err != nil {
		return det, false, err
	}
	for rows.Next() {
		var box, cnt int
		if err := rows.Scan(&box, &cnt); err != nil {
			rows.Close()
			return det, false, err
		}
		if box < 1 {
			box = 1
		}
		counts[box] += cnt
		studied += cnt
		if box > 1 {
			points += (box - 1) * cnt
		}
		if box >= leitnerMaxBox {
			det.Mastered += cnt
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return det, false, err
	}

	for b := 1; b <= leitnerMaxBox; b++ {
		det.Boxes = append(det.Boxes, DeckBox{Box: b, Label: leitnerBoxLabels[b], Count: counts[b]})
	}
	det.New = det.Total - studied
	det.ProgressPct = points * 100 / (det.Total * (leitnerMaxBox - 1))

	now := time.Now().UTC().Format(srsTimeLayout)
	_ = s.db.QueryRow(`
		SELECT COUNT(*) FROM deck_cards dc
		LEFT JOIN leitner_progress lp
		  ON lp.chat_id = ? AND lp.deck_id = dc.deck_id AND lp.term = dc.term
		WHERE dc.deck_id = ? AND (lp.term IS NULL OR lp.due_at <= ?)`,
		chatID, deckID, now,
	).Scan(&det.Due)

	var nextRaw any
	if err := s.db.QueryRow(
		"SELECT MIN(due_at) FROM leitner_progress WHERE chat_id = ? AND deck_id = ? AND due_at > ?",
		chatID, deckID, now,
	).Scan(&nextRaw); err == nil {
		if ts, ok := parseStoredUTC(nextRaw); ok {
			det.NextReview = ts.In(config.AppLocation).Format("2 Jan")
		}
	}
	return det, true, nil
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

// onboardingDeck is the deck whose frequency-ordered words drive known-word
// onboarding (the 504 essential words — the highest-utility starter set).
const onboardingDeck = "504"

// OnboardingWords returns the most common words (frequency-ordered) the user has
// not yet progressed, for the "mark what you already know" onboarding step.
func (s *Store) OnboardingWords(chatID int64, limit int) ([]DeckStudyCard, error) {
	rows, err := s.db.Query(`
		SELECT dc.term, dc.definition, COALESCE(dc.persian, ''), COALESCE(dc.pronunciation, '')
		FROM deck_cards dc
		LEFT JOIN leitner_progress lp
		  ON lp.chat_id = ? AND lp.deck_id = dc.deck_id AND lp.term = dc.term
		WHERE dc.deck_id = ? AND lp.term IS NULL
		ORDER BY (CASE WHEN dc.frequency_rank > 0 THEN dc.frequency_rank ELSE 999999 END) ASC,
		         dc.ordering ASC
		LIMIT ?`,
		chatID, onboardingDeck, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DeckStudyCard
	for rows.Next() {
		var c DeckStudyCard
		if err := rows.Scan(&c.Term, &c.Definition, &c.Persian, &c.Pronunciation); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkDeckKnown files the given terms straight into the mastered Leitner box for
// the onboarding deck, so words the user already knows are skipped in study.
func (s *Store) MarkDeckKnown(chatID int64, terms []string, now time.Time) error {
	due := now.UTC().AddDate(0, 0, leitnerBoxDays[leitnerMaxBox]).Format(srsTimeLayout)
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if _, err := s.db.Exec(`
			INSERT INTO leitner_progress (chat_id, deck_id, term, box, due_at, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(chat_id, deck_id, term)
			DO UPDATE SET box = excluded.box, due_at = excluded.due_at, updated_at = CURRENT_TIMESTAMP`,
			chatID, onboardingDeck, term, leitnerMaxBox, due,
		); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Background field backfill (example, Persian, pronunciation)
// ---------------------------------------------------------------------------

// deckBackfillFields lists the deck_cards columns the worker fills, in priority
// order, with a prompt builder for each. Each builder gets (term, definition).
var deckBackfillFields = []struct {
	column string
	prompt func(term, definition string) string
}{
	{"example", func(term, def string) string {
		return fmt.Sprintf("Write ONE natural English example sentence (max 18 words) that uses the word %q "+
			"in the sense of %q. Return only the sentence, no quotes or labels.", term, def)
	}},
	{"persian", func(term, def string) string {
		return fmt.Sprintf("Translate the English word %q (meaning: %q) into Persian/Farsi. "+
			"Reply with ONLY the Persian translation (1–3 words), no English, no transliteration, no quotes.", term, def)
	}},
	{"pronunciation", func(term, def string) string {
		return fmt.Sprintf("Give the IPA phonetic transcription of the English word %q. "+
			"Reply with ONLY the IPA between slashes, e.g. /əˈbændən/ — no other text.", term)
	}},
}

// runDeckBackfill periodically fills a single missing field (example, then
// Persian, then pronunciation) on deck cards that lack one — e.g. Barron's ships
// definitions only, and the new decks ship without Persian/pronunciation. The
// result is cached back into deck_cards. No-op when no AI provider is configured.
func runDeckBackfill(ctx context.Context, chain *ai.ProviderChain, store *Store) {
	if chain == nil || !chain.HasAny() {
		return
	}
	log.Println("📚 [DECKS] Field backfill worker started (example · Persian · pronunciation).")
	ticker := time.NewTicker(deckBackfillInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.backfillOneDeckField(ctx, chain)
		}
	}
}

// backfillOneDeckField fills one missing field on one card per call, keeping AI
// usage low. Fields are tried in deckBackfillFields order. For persian,
// pronunciation, and example fields, the local dictionary is tried first
// (free and instant) before falling back to AI generation.
func (s *Store) backfillOneDeckField(ctx context.Context, chain *ai.ProviderChain) {
	for _, f := range deckBackfillFields {
		var deckID, term, definition string
		err := s.db.QueryRow(
			"SELECT deck_id, term, definition FROM deck_cards WHERE "+f.column+" = '' LIMIT 1",
		).Scan(&deckID, &term, &definition)
		if err != nil {
			continue // none missing for this field (or transient) — try the next
		}

		// Try the local dictionary first (free, instant, no AI cost).
		var val string
		switch f.column {
		case "persian":
			val = s.LookupPersian(ctx, term)
		case "pronunciation":
			val = s.LookupPronunciation(term)
		case "example":
			val = s.LookupExample(term)
		}

		// Fall back to AI if dictionary had nothing.
		if val == "" {
			out, _, aiErr := chain.Generate(ctx, f.prompt(term, definition))
			if aiErr != nil {
				return
			}
			val = strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "\"'"))
		}

		if val == "" {
			return
		}
		if _, err := s.db.Exec(
			"UPDATE deck_cards SET "+f.column+" = ? WHERE deck_id = ? AND term = ?",
			val, deckID, term,
		); err != nil {
			log.Printf("⚠️  [DECKS] Could not cache %s for %q: %v", f.column, term, err)
		}
		return // one field per tick
	}
}
