package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// reviewItem is a single word + meaning pair used by the daily review.
type reviewItem struct {
	term    string
	meaning string
}

// ---------------------------------------------------------------------------
// content_pool store methods (Change B)
// ---------------------------------------------------------------------------

// PoolCount returns how many pooled items exist for a kind.
func (s *Store) PoolCount(kind string) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM content_pool WHERE kind = ?", kind).Scan(&n)
	return n, err
}

// PoolTerms returns every term currently in the pool for a kind (for de-duping
// during generation).
func (s *Store) PoolTerms(kind string) ([]string, error) {
	rows, err := s.db.Query("SELECT term FROM content_pool WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	return terms, rows.Err()
}

// AddToPool inserts a generated item into the content pool. Duplicate terms for
// the same kind are ignored.
func (s *Store) AddToPool(kind, term, meaning, text string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO content_pool (kind, term, meaning, text) VALUES (?, ?, ?, ?)",
		kind, strings.ToLower(strings.TrimSpace(term)), meaning, text,
	)
	return err
}

// PooledUnseen returns the oldest pooled item for kind whose term the user has
// not seen yet. Returns ok=false if none are available.
func (s *Store) PooledUnseen(kind string, chatID int64) (term, meaning, text string, ok bool, err error) {
	sentTable := sentTableFor(kind)
	query := fmt.Sprintf(`
		SELECT term, meaning, text FROM content_pool
		WHERE kind = ?
		  AND term NOT IN (SELECT word FROM %s WHERE chat_id = ?)
		ORDER BY created_at ASC, id ASC
		LIMIT 1`, sentTable)

	row := s.db.QueryRow(query, kind, chatID)
	if err = row.Scan(&term, &meaning, &text); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}
	return term, meaning, text, true, nil
}

// PooledOldest returns the oldest pooled item for kind regardless of whether the
// user has seen it (fallback for broadcasts when the pool can't personalize).
func (s *Store) PooledOldest(kind string) (term, meaning, text string, ok bool, err error) {
	row := s.db.QueryRow(
		"SELECT term, meaning, text FROM content_pool WHERE kind = ? ORDER BY created_at ASC, id ASC LIMIT 1",
		kind,
	)
	if err = row.Scan(&term, &meaning, &text); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}
	return term, meaning, text, true, nil
}

// recordSentFor records a sent term in the appropriate per-user history table.
func (s *Store) recordSentFor(kind string, chatID int64, term string) error {
	if kind == kindWord {
		return s.RecordSentVocab(chatID, term)
	}
	return s.RecordSentWord(chatID, term)
}

// sentTableFor maps a content kind to its per-user history table name.
func sentTableFor(kind string) string {
	if kind == kindWord {
		return "sent_vocab"
	}
	return "sent_words"
}

// WordsSentBetween returns the distinct vocabulary words sent to a chat within
// the [startUTC, endUTC) window, joined with their meaning from the pool.
func (s *Store) WordsSentBetween(chatID int64, startUTC, endUTC string) ([]reviewItem, error) {
	rows, err := s.db.Query(`
		SELECT sv.word, COALESCE(cp.meaning, '')
		FROM sent_vocab sv
		LEFT JOIN content_pool cp ON cp.kind = 'word' AND cp.term = sv.word
		WHERE sv.chat_id = ? AND sv.sent_at >= ? AND sv.sent_at < ?
		ORDER BY sv.sent_at ASC`,
		chatID, startUTC, endUTC,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []reviewItem
	for rows.Next() {
		var it reviewItem
		if err := rows.Scan(&it.term, &it.meaning); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ReviewDelivered reports whether the daily review for reviewDate has already
// been delivered to chatID.
func (s *Store) ReviewDelivered(chatID int64, reviewDate string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM daily_review_delivery WHERE chat_id = ? AND review_date = ?",
		chatID, reviewDate,
	).Scan(&one)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkReviewDelivered records that the daily review for reviewDate was sent.
func (s *Store) MarkReviewDelivered(chatID int64, reviewDate string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO daily_review_delivery (chat_id, review_date) VALUES (?, ?)",
		chatID, reviewDate,
	)
	return err
}

// ---------------------------------------------------------------------------
// serveContent: pool-first delivery (Change B)
// ---------------------------------------------------------------------------

// serveContent returns ready-to-send text of the given kind for a user, recording
// the chosen term in the user's history.
//
//   - It first tries an unseen pooled item.
//   - On a miss with allowGenerate=true (on-demand commands) it generates inline,
//     adds the result to the pool, and serves it.
//   - On a miss with allowGenerate=false (broadcasts) it falls back to the oldest
//     pooled item so broadcasts never call the AI directly.
func serveContent(ctx context.Context, chain *ProviderChain, store *Store, chatID int64, kind string, allowGenerate bool) (string, error) {
	term, _, text, ok, err := store.PooledUnseen(kind, chatID)
	if err != nil {
		log.Printf("⚠️  [POOL] Unseen lookup failed for kind=%s chat=%d: %v", kind, chatID, err)
	}
	if ok {
		log.Printf("📦 [POOL] Serving unseen pooled %s %q to chat %d.", kind, term, chatID)
		if err := store.recordSentFor(kind, chatID, term); err != nil {
			log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
		}
		return text, nil
	}

	if allowGenerate {
		log.Printf("📦 [POOL] Miss for kind=%s chat=%d; generating inline.", kind, chatID)
		exclude, _ := store.PoolTerms(kind)
		genText, genTerm, meaning, provider, gErr := generateContent(ctx, chain, kind, exclude)
		if gErr != nil {
			return "", gErr
		}
		log.Printf("🧠 [POOL] Generated %s %q via %s (inline).", kind, genTerm, provider)
		if genTerm != "" {
			if err := store.AddToPool(kind, genTerm, meaning, genText); err != nil {
				log.Printf("⚠️  [POOL] Could not add %s %q to pool: %v", kind, genTerm, err)
			}
			if err := store.recordSentFor(kind, chatID, genTerm); err != nil {
				log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, genTerm, chatID, err)
			}
		}
		return genText, nil
	}

	// Broadcast fallback: pool couldn't personalize, reuse the oldest item.
	term, _, text, ok, err = store.PooledOldest(kind)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("content pool empty for kind %s", kind)
	}
	log.Printf("📦 [POOL] No unseen %s for chat %d; reusing oldest pooled %q.", kind, chatID, term)
	if err := store.recordSentFor(kind, chatID, term); err != nil {
		log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
	}
	return text, nil
}

// ---------------------------------------------------------------------------
// poolFiller: background generator that keeps the pool topped up (Change B)
// ---------------------------------------------------------------------------

// poolFiller periodically tops up the content pool for each kind until it reaches
// poolTarget, generating at most one item per refill tick (spaced by genSpacing).
func poolFiller(ctx context.Context, chain *ProviderChain, store *Store) {
	if !chain.HasAny() {
		log.Println("📦 [POOL_FILLER] No providers enabled; pool filler disabled.")
		return
	}
	log.Printf("📦 [POOL_FILLER] Started (target=%d, min=%d, interval=%s).", poolTarget, poolMin, refillInterval)

	ticker := time.NewTicker(refillInterval)
	defer ticker.Stop()

	// Prime once at startup without waiting for the first tick.
	refillKind(ctx, chain, store, kindDrill)
	refillKind(ctx, chain, store, kindWord)

	for {
		select {
		case <-ctx.Done():
			log.Println("📦 [POOL_FILLER] Context cancelled; exiting.")
			return
		case <-ticker.C:
			refillKind(ctx, chain, store, kindDrill)
			select {
			case <-time.After(genSpacing):
			case <-ctx.Done():
				return
			}
			refillKind(ctx, chain, store, kindWord)
		}
	}
}

// refillKind generates and pools a single new item for kind if the pool is below
// target. It de-duplicates against terms already in the pool.
func refillKind(ctx context.Context, chain *ProviderChain, store *Store, kind string) {
	count, err := store.PoolCount(kind)
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Count failed for kind=%s: %v", kind, err)
		return
	}
	if count >= poolTarget {
		return
	}

	exclude, _ := store.PoolTerms(kind)
	text, term, meaning, provider, err := generateContent(ctx, chain, kind, exclude)
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Generation failed for kind=%s: %v", kind, err)
		return
	}
	if term == "" {
		log.Printf("⚠️  [POOL_FILLER] Empty term parsed for kind=%s; skipping insert.", kind)
		return
	}
	if err := store.AddToPool(kind, term, meaning, text); err != nil {
		log.Printf("⚠️  [POOL_FILLER] Insert failed for kind=%s term=%q: %v", kind, term, err)
		return
	}
	log.Printf("📦 [POOL_FILLER] Added %s %q via %s (pool %d→%d/%d).", kind, term, provider, count, count+1, poolTarget)
}
