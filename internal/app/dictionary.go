package app

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Offline English-Persian dictionary (kaikki.org bulk + fa.wiktionary.org live)
//
// On first start (dictionary table empty) a background goroutine downloads the
// kaikki.org wiktextract English JSONL, extracts every Persian translation, and
// populates the dictionary table. Subsequent starts skip this.
//
// At query time, DictLookup reads from the local table. If no rows match,
// wiktionaryLookup fetches fa.wiktionary.org and caches the result.
// ---------------------------------------------------------------------------

// kaikkiURL is the kaikki.org wiktextract English JSONL (gzipped).
const kaikkiURL = "https://kaikki.org/dictionary/raw-wiktextract-data.jsonl.gz"

// dictSeeding is 1 while the background seeder is running.
var dictSeeding atomic.Int32

// DictEntry is one row from the dictionary table.
type DictEntry struct {
	ID            int64  `json:"id,omitempty"`
	Word          string `json:"word"`
	Pos           string `json:"pos,omitempty"`
	Definition    string `json:"definition,omitempty"`
	Example       string `json:"example,omitempty"`
	Persian       string `json:"persian"`
	Romanization  string `json:"romanization,omitempty"`
	Pronunciation string `json:"pronunciation,omitempty"`
	Sense         string `json:"sense,omitempty"`
	Tags          string `json:"tags,omitempty"`
	Source        string `json:"source,omitempty"`
}

// ---------------------------------------------------------------------------
// Background seeder
// ---------------------------------------------------------------------------

// runDictionarySeeder checks whether the dictionary table is populated; if
// empty it downloads the kaikki.org English JSONL, extracts Persian
// translations, and inserts them. Runs once per deploy, in the background.
func runDictionarySeeder(ctx context.Context, store *Store) {
	var count int
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM dictionary WHERE source = 'kaikki'",
	).Scan(&count); err != nil {
		log.Printf("📖 [DICT] Could not check dictionary table: %v", err)
		return
	}
	if count > 0 {
		log.Printf("📖 [DICT] Dictionary already seeded (%d entries), skipping.", count)
		return
	}
	log.Println("📖 [DICT] Dictionary empty — downloading kaikki.org data...")
	dictSeeding.Store(1)
	defer dictSeeding.Store(0)
	if err := store.seedFromKaikki(ctx); err != nil {
		log.Printf("📖 [DICT] Seeding failed: %v (will retry on next restart)", err)
	}
}

// kaikkiEntry is the minimal subset of a kaikki.org JSONL line we parse.
type kaikkiEntry struct {
	Word         string              `json:"word"`
	Pos          string              `json:"pos"`
	Lang         string              `json:"lang"`
	Senses       []kaikkiSense       `json:"senses"`
	Translations []kaikkiTranslation `json:"translations"`
	Sounds       []kaikkiSound       `json:"sounds"`
}

type kaikkiSense struct {
	Glosses  []string        `json:"glosses"`
	Tags     []string        `json:"tags"`
	Examples []kaikkiExample `json:"examples"`
}

type kaikkiExample struct {
	Text string `json:"text"`
}

type kaikkiTranslation struct {
	Lang  string   `json:"lang"`
	Code  string   `json:"code"`
	Word  string   `json:"word"`
	Roman string   `json:"roman"`
	Sense string   `json:"sense"`
	Tags  []string `json:"tags"`
}

type kaikkiSound struct {
	IPA string `json:"ipa"`
}

// seedFromKaikki downloads the gzipped JSONL, extracts Persian translations,
// and batch-inserts them into the dictionary table.
func (s *Store) seedFromKaikki(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kaikkiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Minute} // large file
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	return s.ingestKaikkiStream(ctx, gz)
}

// ingestKaikkiStream reads a JSONL stream (already decompressed) and inserts
// Persian translations into the dictionary table. Exported via a separate
// method so tests can feed it directly.
func (s *Store) ingestKaikkiStream(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Some kaikki.org lines are very long; bump the buffer.
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 8*1024*1024)

	const batchSize = 1000
	var (
		inserted int
		lines    int
		batch    []DictEntry
	)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO dictionary
				(word, pos, definition, example, persian, romanization, pronunciation, sense, tags, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'kaikki')`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, e := range batch {
			if _, err := stmt.Exec(
				e.Word, e.Pos, e.Definition, e.Example,
				e.Persian, e.Romanization, e.Pronunciation,
				e.Sense, e.Tags,
			); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
		inserted += len(batch)
		batch = batch[:0]
		return nil
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lines++
		if lines%200_000 == 0 {
			log.Printf("📖 [DICT] Processed %dk lines, %d entries so far...", lines/1000, inserted+len(batch))
		}

		var entry kaikkiEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip malformed lines
		}
		if entry.Lang != "English" {
			continue
		}

		// Collect Persian translations from this entry.
		persianTrans := make([]kaikkiTranslation, 0, 4)
		for _, t := range entry.Translations {
			if t.Code == "fa" && t.Word != "" {
				persianTrans = append(persianTrans, t)
			}
		}
		if len(persianTrans) == 0 {
			continue
		}

		// Best IPA pronunciation from sounds.
		ipa := ""
		for _, snd := range entry.Sounds {
			if snd.IPA != "" {
				ipa = snd.IPA
				break
			}
		}

		// Best English definition and example from the first sense.
		definition := ""
		example := ""
		if len(entry.Senses) > 0 {
			if len(entry.Senses[0].Glosses) > 0 {
				definition = entry.Senses[0].Glosses[0]
			}
			if len(entry.Senses[0].Examples) > 0 {
				example = entry.Senses[0].Examples[0].Text
			}
		}

		for _, t := range persianTrans {
			batch = append(batch, DictEntry{
				Word:          entry.Word,
				Pos:           entry.Pos,
				Definition:    definition,
				Example:       example,
				Persian:       t.Word,
				Romanization:  t.Roman,
				Pronunciation: ipa,
				Sense:         t.Sense,
				Tags:          strings.Join(t.Tags, ", "),
			})
		}

		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return fmt.Errorf("flush batch at line %d: %w", lines, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("final flush: %w", err)
	}
	log.Printf("📖 [DICT] Seeding complete: %d entries from %d lines.", inserted, lines)
	return nil
}

// ---------------------------------------------------------------------------
// Local dictionary lookup
// ---------------------------------------------------------------------------

// DictLookup returns all dictionary entries for an exact word match.
func (s *Store) DictLookup(word string) []DictEntry {
	rows, err := s.db.Query(`
		SELECT id, word, pos, definition, example, persian, romanization,
		       pronunciation, sense, tags, source
		FROM dictionary WHERE word = ? COLLATE NOCASE
		ORDER BY id`, word)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanDictRows(rows)
}

// DictSearch returns dictionary entries whose word starts with the given prefix.
// Results are limited to 20 rows to keep autocomplete fast.
func (s *Store) DictSearch(prefix string) []DictEntry {
	rows, err := s.db.Query(`
		SELECT id, word, pos, definition, example, persian, romanization,
		       pronunciation, sense, tags, source
		FROM dictionary WHERE word LIKE ? COLLATE NOCASE
		ORDER BY word, id LIMIT 20`, prefix+"%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanDictRows(rows)
}

// DictCount returns the total number of rows in the dictionary table.
func (s *Store) DictCount() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM dictionary").Scan(&n)
	return n
}

func scanDictRows(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
}) []DictEntry {
	var out []DictEntry
	type scanner interface {
		Next() bool
		Scan(dest ...interface{}) error
	}
	r := rows.(scanner)
	for r.Next() {
		var e DictEntry
		if err := r.Scan(
			&e.ID, &e.Word, &e.Pos, &e.Definition, &e.Example,
			&e.Persian, &e.Romanization, &e.Pronunciation,
			&e.Sense, &e.Tags, &e.Source,
		); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ---------------------------------------------------------------------------
// fa.wiktionary.org live fallback
// ---------------------------------------------------------------------------

// wiktionaryFallback fetches the Persian meaning of a word from
// fa.wiktionary.org, caches the result in the dictionary table, and returns it.
// Returns nil if the word is not found or the API fails.
func (s *Store) wiktionaryFallback(ctx context.Context, word string) []DictEntry {
	apiURL := fmt.Sprintf(
		"https://fa.wiktionary.org/w/api.php?action=parse&page=%s&prop=wikitext&format=json&redirects=1",
		word,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		Parse struct {
			Wikitext struct {
				Body string `json:"*"`
			} `json:"wikitext"`
		} `json:"parse"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	if result.Error.Code != "" {
		return nil // page not found
	}

	persian := extractPersianFromWikitext(result.Parse.Wikitext.Body)
	if persian == "" {
		return nil
	}

	// Cache the result.
	_, _ = s.db.Exec(`
		INSERT OR IGNORE INTO dictionary
			(word, pos, definition, example, persian, romanization, pronunciation, sense, tags, source)
		VALUES (?, '', '', '', ?, '', '', '', '', 'wiktionary')`,
		word, persian,
	)

	return []DictEntry{{
		Word:    word,
		Persian: persian,
		Source:  "wiktionary",
	}}
}

// extractPersianFromWikitext parses fa.wiktionary.org wikitext to find Persian
// definitions. The format varies but typically includes lines starting with #
// under an ==English== or ==انگلیسی== section.
func extractPersianFromWikitext(wikitext string) string {
	lines := strings.Split(wikitext, "\n")
	var inEnglish bool
	var meanings []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Top-level section headers: ==English== or ==انگلیسی==
		// (exactly two '=' on each side; subsections use three or more).
		if strings.HasPrefix(trimmed, "==") && !strings.HasPrefix(trimmed, "===") &&
			strings.HasSuffix(trimmed, "==") && !strings.HasSuffix(trimmed, "===") {
			section := strings.Trim(trimmed, "= ")
			inEnglish = section == "English" || section == "انگلیسی" ||
				strings.EqualFold(section, "english")
			continue
		}
		if !inEnglish {
			continue
		}
		// Definition lines start with # (but not ##, which are sub-definitions).
		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "##") {
			def := strings.TrimLeft(trimmed, "#* ")
			// Strip wiki markup: [[text]] → text, {{text}} → text
			def = stripWikiMarkup(def)
			def = strings.TrimSpace(def)
			if def != "" {
				meanings = append(meanings, def)
			}
		}
	}
	if len(meanings) == 0 {
		return ""
	}
	// Return first meaning (most common/primary).
	return meanings[0]
}

// stripWikiMarkup removes basic wikitext formatting: [[link|text]] → text,
// [[text]] → text, {{template}} → empty.
func stripWikiMarkup(s string) string {
	// Remove {{ ... }}
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+2:]
	}
	// [[link|display]] → display; [[plain]] → plain
	for {
		start := strings.Index(s, "[[")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "]]")
		if end < 0 {
			break
		}
		inner := s[start+2 : start+end]
		if pipe := strings.LastIndex(inner, "|"); pipe >= 0 {
			inner = inner[pipe+1:]
		}
		s = s[:start] + inner + s[start+end+2:]
	}
	return s
}

// ---------------------------------------------------------------------------
// Unified lookup
// ---------------------------------------------------------------------------

// LookupPersian returns the best Persian translation for a word, trying the
// local dictionary first and falling back to fa.wiktionary.org. Returns empty
// string if neither source has a translation.
func (s *Store) LookupPersian(ctx context.Context, word string) string {
	entries := s.DictLookup(word)
	if len(entries) > 0 {
		return entries[0].Persian
	}
	// Live fallback.
	entries = s.wiktionaryFallback(ctx, word)
	if len(entries) > 0 {
		return entries[0].Persian
	}
	return ""
}

// LookupPronunciation returns the best IPA pronunciation for a word from the
// local dictionary. Returns empty string if not found.
func (s *Store) LookupPronunciation(word string) string {
	var ipa string
	err := s.db.QueryRow(
		"SELECT pronunciation FROM dictionary WHERE word = ? COLLATE NOCASE AND pronunciation != '' LIMIT 1",
		word,
	).Scan(&ipa)
	if err != nil {
		return ""
	}
	return ipa
}

// LookupExample returns the best example sentence for a word from the local
// dictionary. Returns empty string if not found.
func (s *Store) LookupExample(word string) string {
	var ex string
	err := s.db.QueryRow(
		"SELECT example FROM dictionary WHERE word = ? COLLATE NOCASE AND example != '' LIMIT 1",
		word,
	).Scan(&ex)
	if err != nil {
		return ""
	}
	return ex
}
