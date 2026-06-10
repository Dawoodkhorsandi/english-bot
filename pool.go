package main

import (
	"context"
	"database/sql"
	"errors"
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

// PoolTerms returns every term currently in the pool for a kind at a given level.
// De-dup is per-level (the UNIQUE(kind, level, term) constraint is per-level), so
// the same term may legitimately exist at other levels.
func (s *Store) PoolTerms(kind, level string) ([]string, error) {
	rows, err := s.db.Query("SELECT term FROM content_pool WHERE kind = ? AND level = ?", kind, level)
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

// UpdatePoolText overwrites the text (and optionally meaning) of an existing
// pool entry. Used by the lazy card refresh to update stale cards in place.
func (s *Store) UpdatePoolText(kind, level, term, meaning, text string) error {
	_, err := s.db.Exec(
		"UPDATE content_pool SET text = ?, meaning = ? WHERE kind = ? AND level = ? AND term = ?",
		text, meaning, kind, level, strings.ToLower(strings.TrimSpace(term)),
	)
	return err
}

// cardNeedsRefresh reports whether a word card is missing sections that
// current prompts produce. This drives lazy backfill: stale cards are served
// immediately and regenerated in the background for next time.
func cardNeedsRefresh(text string) bool {
	// Persian definition added in v1.20.0.
	if !strings.Contains(text, "🇮🇷") {
		return true
	}
	return false
}

// PooledUnseen returns a random pooled item for kind+level whose term the user has
// not seen yet. Selection is randomized (rather than oldest-first) so subscribers
// don't all receive pooled content in the same lock-step order. Returns ok=false if
// none are available.
func (s *Store) PooledUnseen(kind, level string, chatID int64) (term, meaning, text string, ok bool, err error) {
	sentTable := sentTableFor(kind)
	query := fmt.Sprintf(`
		SELECT term, meaning, text FROM content_pool
		WHERE kind = ? AND level = ?
		  AND term NOT IN (SELECT word FROM %s WHERE chat_id = ?)
		ORDER BY RANDOM()
		LIMIT 1`, sentTable)

	row := s.db.QueryRow(query, kind, level, chatID)
	if err = row.Scan(&term, &meaning, &text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}
	return term, meaning, text, true, nil
}

// PooledRecycled returns a pooled item for kind+level for a user who has already
// seen everything at that level. It picks at random among all pooled items EXCEPT
// the one most recently served to that user, so a broadcast can never repeat the
// same item back-to-back (the bug that pinned the single oldest item forever).
// Returns ok=false only if the pool for that level is empty.
func (s *Store) PooledRecycled(kind, level string, chatID int64) (term, meaning, text string, ok bool, err error) {
	sentTable := sentTableFor(kind)
	// Exclude the user's most-recently-served term (by last_sent_at) so we never
	// serve it twice in a row. COALESCE guards the no-history case (empty string
	// excludes nothing). When the pool holds a single item the exclusion yields no
	// row and the caller falls back to PooledOldest.
	query := fmt.Sprintf(`
		SELECT term, meaning, text FROM content_pool
		WHERE kind = ? AND level = ?
		  AND term <> COALESCE((
		        SELECT word FROM %s
		        WHERE chat_id = ?
		        ORDER BY last_sent_at DESC, word DESC
		        LIMIT 1), '')
		ORDER BY RANDOM()
		LIMIT 1`, sentTable)

	row := s.db.QueryRow(query, kind, level, chatID)
	if err = row.Scan(&term, &meaning, &text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}
	return term, meaning, text, true, nil
}

// DrillText returns the full stored drill text for a verb (kind=drill). It is used
// to re-render a different page when a user taps a drill navigation button, so the
// whole drill never has to travel in the button's callback data.
func (s *Store) DrillText(term string) (string, bool, error) {
	var text string
	err := s.db.QueryRow(
		"SELECT text FROM content_pool WHERE kind = ? AND term = ? LIMIT 1",
		kindDrill, strings.ToLower(strings.TrimSpace(term)),
	).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// WordCard returns the stored meaning and full vocabulary-card text for a word
// (kind=word) from the content pool, across any level. Used to reveal a word's
// meaning/details after the user answers a spaced-repetition memory check.
func (s *Store) WordCard(term string) (meaning, text string, ok bool, err error) {
	row := s.db.QueryRow(
		"SELECT COALESCE(meaning,''), text FROM content_pool WHERE kind = ? AND term = ? LIMIT 1",
		kindWord, strings.ToLower(strings.TrimSpace(term)),
	)
	if err = row.Scan(&meaning, &text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return meaning, text, true, nil
}

// recordSentFor records a sent term in the appropriate per-user history table.
// For vocabulary words it also seeds the spaced-repetition schedule (Change D),
// so every word a user receives is enrolled for future review.
func (s *Store) recordSentFor(kind string, chatID int64, term string) error {
	switch kind {
	case kindWord:
		if err := s.RecordSentVocab(chatID, term); err != nil {
			return err
		}
		if err := s.SeedReview(chatID, term, time.Now()); err != nil {
			log.Printf("⚠️  [SRS] Could not seed review for %q (chat %d): %v", term, chatID, err)
		}
		return nil
	case kindIdiom:
		return s.RecordSentIdiom(chatID, term)
	case kindTip:
		return s.RecordSentTip(chatID, term)
	case kindCollocation:
		return s.RecordSentCollocation(chatID, term)
	case kindStory:
		return s.RecordSentStory(chatID, term)
	default:
		return s.RecordSentWord(chatID, term)
	}
}

// sentTableFor maps a content kind to its per-user history table name.
func sentTableFor(kind string) string {
	switch kind {
	case kindWord:
		return "sent_vocab"
	case kindIdiom:
		return "sent_idioms"
	case kindTip:
		return "sent_tips"
	case kindCollocation:
		return "sent_collocations"
	case kindStory:
		return "sent_stories"
	default:
		return "sent_words"
	}
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
		if errors.Is(err, sql.ErrNoRows) {
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

// IdiomDelivered reports whether the idiom of the day for idiomDate has already
// been delivered to chatID.
func (s *Store) IdiomDelivered(chatID int64, idiomDate string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM idiom_delivery WHERE chat_id = ? AND idiom_date = ?",
		chatID, idiomDate,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkIdiomDelivered records that the idiom of the day for idiomDate was sent.
func (s *Store) MarkIdiomDelivered(chatID int64, idiomDate string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO idiom_delivery (chat_id, idiom_date) VALUES (?, ?)",
		chatID, idiomDate,
	)
	return err
}

// TipDelivered reports whether the daily tip for tipDate has already been
// delivered to chatID.
func (s *Store) TipDelivered(chatID int64, tipDate string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM daily_tip_delivery WHERE chat_id = ? AND tip_date = ?",
		chatID, tipDate,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkTipDelivered records that the daily tip for tipDate was sent.
func (s *Store) MarkTipDelivered(chatID int64, tipDate string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO daily_tip_delivery (chat_id, tip_date) VALUES (?, ?)",
		chatID, tipDate,
	)
	return err
}

// CollocationDelivered reports whether the collocation of the day for
// collocationDate has already been delivered to chatID.
func (s *Store) CollocationDelivered(chatID int64, collocationDate string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM collocation_delivery WHERE chat_id = ? AND collocation_date = ?",
		chatID, collocationDate,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkCollocationDelivered records that the collocation of the day was sent.
func (s *Store) MarkCollocationDelivered(chatID int64, collocationDate string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO collocation_delivery (chat_id, collocation_date) VALUES (?, ?)",
		chatID, collocationDate,
	)
	return err
}

// StoryDelivered reports whether the daily mini story for storyDate has already
// been delivered to chatID.
func (s *Store) StoryDelivered(chatID int64, storyDate string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM story_delivery WHERE chat_id = ? AND story_date = ?",
		chatID, storyDate,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkStoryDelivered records that the daily mini story for storyDate was sent.
func (s *Store) MarkStoryDelivered(chatID int64, storyDate string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO story_delivery (chat_id, story_date) VALUES (?, ?)",
		chatID, storyDate,
	)
	return err
}

// ---------------------------------------------------------------------------
// weekly_digest_delivery store methods (Change K)
// ---------------------------------------------------------------------------

// WeeklyDigestDelivered reports whether the weekly digest for weekStart has
// already been delivered to chatID.
func (s *Store) WeeklyDigestDelivered(chatID int64, weekStart string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		"SELECT 1 FROM weekly_digest_delivery WHERE chat_id = ? AND week_start = ?",
		chatID, weekStart,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkWeeklyDigestDelivered records that the weekly digest for weekStart was sent.
func (s *Store) MarkWeeklyDigestDelivered(chatID int64, weekStart string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO weekly_digest_delivery (chat_id, week_start) VALUES (?, ?)",
		chatID, weekStart,
	)
	return err
}

// WeeklyQuizStats returns the total answered and correct quiz counts for a user
// within the given UTC time range.
func (s *Store) WeeklyQuizStats(chatID int64, startUTC, endUTC string) (answered, correct int, err error) {
	err = s.db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(correct), 0) FROM quiz_results WHERE chat_id = ? AND answered_at >= ? AND answered_at < ?",
		chatID, startUTC, endUTC,
	).Scan(&answered, &correct)
	return answered, correct, err
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
func serveContent(ctx context.Context, chain *ProviderChain, store *Store, chatID int64, kind, level string, allowGenerate bool) (string, string, error) {
	term, _, text, ok, err := store.PooledUnseen(kind, level, chatID)
	if err != nil {
		log.Printf("⚠️  [POOL] Unseen lookup failed for kind=%s level=%s chat=%d: %v", kind, level, chatID, err)
	}
	if ok {
		log.Printf("📦 [POOL] Serving unseen pooled %s/%s %q to chat %d.", kind, level, term, chatID)
		if err := store.recordSentFor(kind, chatID, term); err != nil {
			log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
		}
		maybeRefreshCard(chain, store, kind, level, term, text)
		return text, term, nil
	}

	if allowGenerate {
		log.Printf("📦 [POOL] Miss for kind=%s level=%s chat=%d; generating inline.", kind, level, chatID)
		exclude, _ := store.PoolTerms(kind, level)
		genText, genTerm, meaning, provider, gErr := generateContent(ctx, chain, kind, level, exclude)
		if gErr != nil {
			return "", "", gErr
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
		return genText, genTerm, nil
	}

	// Broadcast fallback: the user has seen every pooled item at this level. Rotate
	// through their history by re-serving a random item other than the one served
	// most recently, instead of pinning the single oldest pooled item forever.
	term, _, text, ok, err = store.PooledRecycled(kind, level, chatID)
	if err != nil {
		log.Printf("⚠️  [POOL] Recycle lookup failed for kind=%s level=%s chat=%d: %v", kind, level, chatID, err)
	}
	if !ok {
		// Final safety net (e.g. a single-item pool, where the "not most-recent"
		// filter excludes everything): re-serve the oldest pooled item.
		term, _, text, ok, err = store.PooledOldest(kind, level)
		if err != nil {
			return "", "", err
		}
	}
	if !ok {
		return "", "", fmt.Errorf("content pool empty for kind %s level %s", kind, level)
	}
	log.Printf("📦 [POOL] No unseen %s/%s for chat %d; re-serving pooled %q (rotation).", kind, level, chatID, term)
	if err := store.recordSentFor(kind, chatID, term); err != nil {
		log.Printf("⚠️  [POOL] Could not record %s %q for chat %d: %v", kind, term, chatID, err)
	}
	maybeRefreshCard(chain, store, kind, level, term, text)
	return text, term, nil
}

// maybeRefreshCard checks if a pooled word card is stale (missing sections that
// current prompts produce, e.g. Persian definition). If so it spawns a
// background goroutine to regenerate the card and update the pool entry.
// The caller serves the old card immediately — the refreshed version will be
// used on the next request.
func maybeRefreshCard(chain *ProviderChain, store *Store, kind, level, term, text string) {
	if kind != kindWord || !cardNeedsRefresh(text) {
		return
	}
	log.Printf("🔄 [POOL] Card %q (%s/%s) is stale; scheduling background refresh.", term, kind, level)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		newText, _, meaning, provider, err := generateWordFor(ctx, chain, level, term)
		if err != nil {
			log.Printf("⚠️  [POOL] Background refresh failed for %q: %v", term, err)
			return
		}
		if err := store.UpdatePoolText(kind, level, term, meaning, newText); err != nil {
			log.Printf("⚠️  [POOL] Could not update pool for %q: %v", term, err)
			return
		}
		log.Printf("✅ [POOL] Refreshed card %q (%s/%s) via %s.", term, kind, level, provider)
	}()
}

// poolTargetFor returns how many items to keep stocked for a (kind, level) pair.
// It honours per-kind and per-level admin overrides (set via /config), falling
// back to the global rule: the default level keeps the full pool, non-default
// levels keep a smaller pool. See resolvePoolTarget for precedence.
func poolTargetFor(kind, level string) int {
	return resolvePoolTarget(kind, level)
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
	for _, kind := range []string{kindDrill, kindWord, kindIdiom, kindCollocation, kindStory} {
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
	// Tips are universal (level-independent), so keep one shared tip pool.
	if ctx.Err() != nil {
		return
	}
	_ = refillKind(ctx, chain, store, kindTip, defaultLevel)
}
	}
}

// refillKind generates and pools a single new item for kind+level if that level's
// pool is below target. It de-duplicates against all terms already in the pool.
// Returns true if a generation was attempted (so the caller can space them out).
func refillKind(ctx context.Context, chain *ProviderChain, store *Store, kind, level string) bool {
	target := poolTargetFor(kind, level)
	count, err := store.PoolCount(kind, level)
	if err != nil {
		log.Printf("⚠️  [POOL_FILLER] Count failed for kind=%s level=%s: %v", kind, level, err)
		return false
	}
	if count >= target {
		return false
	}

	exclude, _ := store.PoolTerms(kind, level)
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
