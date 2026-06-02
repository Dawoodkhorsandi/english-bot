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
func (s *Store) PoolCount(kind, level string) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM content_pool WHERE kind = ? AND level = ?", kind, level).Scan(&n)
	return n, err
}

// PoolTerms returns every term currently in the pool for a kind, across all
// levels (the UNIQUE(kind, term) constraint is global, so de-dup must be too).
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

// AddToPool inserts a generated item into the content pool at the given level.
// Duplicate terms for the same kind are ignored (UNIQUE(kind, term)).
func (s *Store) AddToPool(kind, level, term, meaning, text string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO content_pool (kind, level, term, meaning, text) VALUES (?, ?, ?, ?, ?)",
		kind, level, strings.ToLower(strings.TrimSpace(term)), meaning, text,
	)
	return err
}

// PooledUnseen returns the oldest pooled item for kind+level whose term the user
// has not seen yet. Returns ok=false if none are available.
func (s *Store) PooledUnseen(kind, level string, chatID int64) (term, meaning, text string, ok bool, err error) {
	sentTable := sentTableFor(kind)
	query := fmt.Sprintf(`
		SELECT term, meaning, text FROM content_pool
		WHERE kind = ? AND level = ?
		  AND term NOT IN (SELECT word FROM %s WHERE chat_id = ?)
		ORDER BY created_at ASC, id ASC
		LIMIT 1`, sentTable)

	row := s.db.QueryRow(query, kind, level, chatID)
	if err = row.Scan(&term, &meaning, &text); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}
	return term, meaning, text, true, nil
}

// PooledOldest returns the oldest pooled item for kind+level regardless of whether
// the user has seen it (fallback for broadcasts when the pool can't personalize).
func (s *Store) PooledOldest(kind, level string) (term, meaning, text string, ok bool, err error) {
	row := s.db.QueryRow(
		"SELECT term, meaning, text FROM content_pool WHERE kind = ? AND level = ? ORDER BY created_at ASC, id ASC LIMIT 1",
		kind, level,
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
func serveContent(ctx context.Context, chain *ProviderChain, store *Store, chatID int64, kind, level string, allowGenerate bool) (string, error) {
	term, _, text, ok, err := store.PooledUnseen(kind, level, chatID)
	if err != nil {
		log.Printf("⚠️  [POOL] Unseen lookup failed for kind=%s level=%s chat=%d: %v", kind, level, chatID, err)
	}
	if ok {
		log.Printf("📦 [POOL] Serving unseen pooled %s/%s %q to chat %d.", kind, level, term, chatID)
		if err := store.recordSentFor(kind, chatID, term); err != nil {
			log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
		}
		return text, nil
	}

	if allowGenerate {
		log.Printf("📦 [POOL] Miss for kind=%s level=%s chat=%d; generating inline.", kind, level, chatID)
		exclude, _ := store.PoolTerms(kind)
		genText, genTerm, meaning, provider, gErr := generateContent(ctx, chain, kind, level, exclude)
		if gErr != nil {
			return "", gErr
		}
		log.Printf("🧠 [POOL] Generated %s/%s %q via %s (inline).", kind, level, genTerm, provider)
		if genTerm != "" {
			if err := store.AddToPool(kind, level, genTerm, meaning, genText); err != nil {
				log.Printf("⚠️  [POOL] Could not add %s %q to pool: %v", kind, genTerm, err)
			}
			if err := store.recordSentFor(kind, chatID, genTerm); err != nil {
				log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, genTerm, chatID, err)
			}
		}
		return genText, nil
	}

	// Broadcast fallback: pool couldn't personalize at this level, reuse the
	// oldest item for the level.
	term, _, text, ok, err = store.PooledOldest(kind, level)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("content pool empty for kind %s level %s", kind, level)
	}
	log.Printf("📦 [POOL] No unseen %s/%s for chat %d; reusing oldest pooled %q.", kind, level, chatID, term)
	if err := store.recordSentFor(kind, chatID, term); err != nil {
		log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
	}
	return text, nil
}

// poolTargetFor returns how many items to keep stocked for a level. The default
// level keeps the full pool; non-default levels keep a smaller pool.
func poolTargetFor(level string) int {
	if level == defaultLevel {
		return poolTarget
	}
	return poolMin
}

// ---------------------------------------------------------------------------
// poolFiller: background generator that keeps the pool topped up (Change B)
// ---------------------------------------------------------------------------

// poolFiller periodically tops up the content pool for each (kind, active level)
// until it reaches the level's target, generating one item per step (spaced by
// genSpacing).
func poolFiller(ctx context.Context, chain *ProviderChain, store *Store) {
	if !chain.HasAny() {
		log.Println("📦 [POOL_FILLER] No providers enabled; pool filler disabled.")
		return
	}
	log.Printf("📦 [POOL_FILLER] Started (target=%d, min=%d, interval=%s).", poolTarget, poolMin, refillInterval)

	ticker := time.NewTicker(refillInterval)
	defer ticker.Stop()

	// Prime once at startup without waiting for the first tick.
	runRefillCycle(ctx, chain, store)

	for {
		select {
		case <-ctx.Done():
			log.Println("📦 [POOL_FILLER] Context cancelled; exiting.")
			return
		case <-ticker.C:
			runRefillCycle(ctx, chain, store)
		}
	}
}

// runRefillCycle attempts one refill step for every (kind, active level) pair,
// spacing generations by genSpacing and honouring context cancellation.
func runRefillCycle(ctx context.Context, chain *ProviderChain, store *Store) {
	levels, err := store.ActiveLevels()
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Could not load active levels: %v (using default only)", err)
		levels = []string{defaultLevel}
	}
	for _, kind := range []string{kindDrill, kindWord} {
		for _, level := range levels {
			if ctx.Err() != nil {
				return
			}
			if refillKind(ctx, chain, store, kind, level) {
				select {
				case <-time.After(genSpacing):
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// refillKind generates and pools a single new item for kind+level if that level's
// pool is below target. It de-duplicates against all terms already in the pool.
// Returns true if a generation was attempted (so the caller can space them out).
func refillKind(ctx context.Context, chain *ProviderChain, store *Store, kind, level string) bool {
	target := poolTargetFor(level)
	count, err := store.PoolCount(kind, level)
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Count failed for kind=%s level=%s: %v", kind, level, err)
		return false
	}
	if count >= target {
		return false
	}

	exclude, _ := store.PoolTerms(kind)
	text, term, meaning, provider, err := generateContent(ctx, chain, kind, level, exclude)
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Generation failed for kind=%s level=%s: %v", kind, level, err)
		return true
	}
	if term == "" {
		log.Printf("⚠️  [POOL_FILLER] Empty term parsed for kind=%s level=%s; skipping insert.", kind, level)
		return true
	}
	if err := store.AddToPool(kind, level, term, meaning, text); err != nil {
		log.Printf("⚠️  [POOL_FILLER] Insert failed for kind=%s level=%s term=%q: %v", kind, level, term, err)
		return true
	}
	log.Printf("📦 [POOL_FILLER] Added %s/%s %q via %s (pool %d→%d/%d).", kind, level, term, provider, count, count+1, target)
	return true
}
