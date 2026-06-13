package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Dictionary store tests
// ---------------------------------------------------------------------------

func TestDictIngestAndLookup(t *testing.T) {
	store := testStore(t)

	// Build a small fake JSONL stream with two entries, one having Persian
	// translations and one without.
	lines := []string{
		`{"word":"abandon","pos":"verb","lang":"English","senses":[{"glosses":["To give up completely"],"examples":[{"text":"He abandoned the project."}]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0631\u0647\u0627 \u06a9\u0631\u062f\u0646","roman":"rah\u00e2 kardan","sense":"to give up"}],"sounds":[{"ipa":"/\u0259\u02c8b\u00e6n.d\u0259n/"}]}`,
		`{"word":"apple","pos":"noun","lang":"English","senses":[{"glosses":["A round fruit"]}],"translations":[{"lang":"Spanish","code":"es","word":"manzana"}]}`,
		`{"word":"book","pos":"noun","lang":"English","senses":[{"glosses":["A set of written pages"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u06a9\u062a\u0627\u0628","roman":"ket\u00e2b","sense":"written pages"},{"lang":"Persian","code":"fa","word":"\u0646\u0633\u062e\u0647","roman":"nosxe","sense":"copy"}]}`,
		`{"word":"chat","pos":"noun","lang":"French","senses":[{"glosses":["Cat"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u06af\u0631\u0628\u0647"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))

	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	// Verify counts.
	count := store.DictCount()
	if count != 3 { // abandon(1) + book(2) = 3 Persian entries (apple=Spanish, chat=French lang)
		t.Errorf("DictCount = %d, want 3", count)
	}

	// Exact lookup.
	entries := store.DictLookup("abandon")
	if len(entries) != 1 {
		t.Fatalf("DictLookup(abandon) = %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Word != "abandon" {
		t.Errorf("Word = %q, want abandon", e.Word)
	}
	if e.Pos != "verb" {
		t.Errorf("Pos = %q, want verb", e.Pos)
	}
	if e.Persian != "رها کردن" {
		t.Errorf("Persian = %q, want رها کردن", e.Persian)
	}
	if e.Romanization != "rahâ kardan" {
		t.Errorf("Romanization = %q, want rahâ kardan", e.Romanization)
	}
	if e.Pronunciation != "/əˈbæn.dən/" {
		t.Errorf("Pronunciation = %q, want /əˈbæn.dən/", e.Pronunciation)
	}
	if e.Definition != "To give up completely" {
		t.Errorf("Definition = %q, want 'To give up completely'", e.Definition)
	}
	if e.Example != "He abandoned the project." {
		t.Errorf("Example = %q, want 'He abandoned the project.'", e.Example)
	}
	if e.Sense != "to give up" {
		t.Errorf("Sense = %q, want 'to give up'", e.Sense)
	}
	if e.Source != "kaikki" {
		t.Errorf("Source = %q, want kaikki", e.Source)
	}

	// Multiple meanings for "book".
	entries = store.DictLookup("book")
	if len(entries) != 2 {
		t.Fatalf("DictLookup(book) = %d entries, want 2", len(entries))
	}

	// No results for words without Persian translations.
	entries = store.DictLookup("apple")
	if len(entries) != 0 {
		t.Errorf("DictLookup(apple) = %d entries, want 0", len(entries))
	}

	// Non-English entries should be skipped.
	entries = store.DictLookup("chat")
	if len(entries) != 0 {
		t.Errorf("DictLookup(chat) = %d entries, want 0 (French, not English)", len(entries))
	}
}

func TestDictSearch(t *testing.T) {
	store := testStore(t)

	lines := []string{
		`{"word":"abandon","pos":"verb","lang":"English","senses":[{"glosses":["To give up"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0631\u0647\u0627 \u06a9\u0631\u062f\u0646"}]}`,
		`{"word":"about","pos":"adv","lang":"English","senses":[{"glosses":["Approximately"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u062a\u0642\u0631\u06cc\u0628\u0627\u064b"}]}`,
		`{"word":"book","pos":"noun","lang":"English","senses":[{"glosses":["Written pages"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u06a9\u062a\u0627\u0628"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))
	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	// Prefix search for "ab" should return abandon and about.
	results := store.DictSearch("ab")
	if len(results) != 2 {
		t.Errorf("DictSearch(ab) = %d entries, want 2", len(results))
	}

	// Prefix search for "bo" should return book.
	results = store.DictSearch("bo")
	if len(results) != 1 {
		t.Errorf("DictSearch(bo) = %d entries, want 1", len(results))
	}

	// Prefix search for "xyz" should return nothing.
	results = store.DictSearch("xyz")
	if len(results) != 0 {
		t.Errorf("DictSearch(xyz) = %d entries, want 0", len(results))
	}
}

func TestDictCaseInsensitiveLookup(t *testing.T) {
	store := testStore(t)

	lines := []string{
		`{"word":"Hello","pos":"interjection","lang":"English","senses":[{"glosses":["A greeting"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0633\u0644\u0627\u0645"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))
	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	// Should find regardless of case.
	for _, q := range []string{"Hello", "hello", "HELLO"} {
		results := store.DictLookup(q)
		if len(results) != 1 {
			t.Errorf("DictLookup(%q) = %d entries, want 1", q, len(results))
		}
	}
}

func TestDictLookupPersian(t *testing.T) {
	store := testStore(t)

	lines := []string{
		`{"word":"run","pos":"verb","lang":"English","senses":[{"glosses":["To move quickly"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u062f\u0648\u06cc\u062f\u0646","roman":"davidan"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))
	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	// LookupPersian should return the first match.
	got := store.LookupPersian(context.Background(), "run")
	if got != "دویدن" {
		t.Errorf("LookupPersian(run) = %q, want دویدن", got)
	}

	// Non-existent word returns empty (no wiktionary in test).
	got = store.LookupPersian(context.Background(), "nonexistentword12345")
	if got != "" {
		t.Errorf("LookupPersian(nonexistent) = %q, want empty", got)
	}
}

func TestDictLookupPronunciation(t *testing.T) {
	store := testStore(t)

	lines := []string{
		`{"word":"test","pos":"noun","lang":"English","senses":[{"glosses":["A trial"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0622\u0632\u0645\u0648\u0646"}],"sounds":[{"ipa":"/t\u025bst/"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))
	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	got := store.LookupPronunciation("test")
	if got != "/tɛst/" {
		t.Errorf("LookupPronunciation(test) = %q, want /tɛst/", got)
	}

	got = store.LookupPronunciation("missing")
	if got != "" {
		t.Errorf("LookupPronunciation(missing) = %q, want empty", got)
	}
}

func TestDictLookupExample(t *testing.T) {
	store := testStore(t)

	lines := []string{
		`{"word":"walk","pos":"verb","lang":"English","senses":[{"glosses":["To move on foot"],"examples":[{"text":"She walks to school every day."}]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0631\u0627\u0647 \u0631\u0641\u062a\u0646"}]}`,
	}
	reader := strings.NewReader(strings.Join(lines, "\n"))
	if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
		t.Fatalf("ingestKaikkiStream: %v", err)
	}

	got := store.LookupExample("walk")
	if got != "She walks to school every day." {
		t.Errorf("LookupExample(walk) = %q, want 'She walks to school every day.'", got)
	}

	got = store.LookupExample("missing")
	if got != "" {
		t.Errorf("LookupExample(missing) = %q, want empty", got)
	}
}

func TestDictUniqueConstraint(t *testing.T) {
	store := testStore(t)

	// Ingest the same data twice — UNIQUE(word, pos, persian) should prevent duplicates.
	line := `{"word":"test","pos":"noun","lang":"English","senses":[{"glosses":["A trial"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0622\u0632\u0645\u0648\u0646"}]}`
	for i := 0; i < 2; i++ {
		reader := strings.NewReader(line)
		if err := store.ingestKaikkiStream(context.Background(), reader); err != nil {
			t.Fatalf("ingestKaikkiStream pass %d: %v", i+1, err)
		}
	}

	count := store.DictCount()
	if count != 1 {
		t.Errorf("DictCount after double ingest = %d, want 1", count)
	}
}

func TestDictSeedingFlag(t *testing.T) {
	// The seeding flag should be 0 by default.
	if dictSeeding.Load() != 0 {
		t.Errorf("dictSeeding default = %d, want 0", dictSeeding.Load())
	}
}

// ---------------------------------------------------------------------------
// Wikitext parser tests
// ---------------------------------------------------------------------------

func TestExtractPersianFromWikitext(t *testing.T) {
	tests := []struct {
		name     string
		wikitext string
		want     string
	}{
		{
			name:     "simple definition",
			wikitext: "==انگلیسی==\n===اسم===\n# کتاب\n# نسخه",
			want:     "کتاب",
		},
		{
			name:     "English header",
			wikitext: "==English==\n===Noun===\n# [[book|کتاب]]",
			want:     "کتاب",
		},
		{
			name:     "no English section",
			wikitext: "==فرانسوی==\n# گربه",
			want:     "",
		},
		{
			name:     "wiki markup stripped",
			wikitext: "==انگلیسی==\n# {{trans}} [[رها کردن]]",
			want:     "رها کردن",
		},
		{
			name:     "empty",
			wikitext: "",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPersianFromWikitext(tt.wikitext)
			if got != tt.want {
				t.Errorf("extractPersianFromWikitext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripWikiMarkup(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"[[book]]", "book"},
		{"[[link|display]]", "display"},
		{"{{template}}", ""},
		{"plain text", "plain text"},
		{"a [[b|c]] d {{e}} f", "a c d  f"},
	}
	for _, tt := range tests {
		got := stripWikiMarkup(tt.in)
		if got != tt.want {
			t.Errorf("stripWikiMarkup(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Dictionary API tests
// ---------------------------------------------------------------------------

func TestAPIDictionaryExactLookup(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	// Seed dictionary with test data.
	lines := `{"word":"house","pos":"noun","lang":"English","senses":[{"glosses":["A building"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u062e\u0627\u0646\u0647","roman":"xune","sense":"building"}],"sounds":[{"ipa":"/ha\u028as/"}]}`
	if err := store.ingestKaikkiStream(context.Background(), strings.NewReader(lines)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := apiCall(store, handleAPIDictionary, http.MethodGet,
		"/api/dictionary?q=house", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Results []DictEntry `json:"results"`
		Seeding bool        `json:"seeding"`
		Total   int         `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Persian != "خانه" {
		t.Errorf("persian = %q, want خانه", resp.Results[0].Persian)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	if resp.Seeding {
		t.Errorf("seeding = true, want false")
	}
}

func TestAPIDictionaryPrefixSearch(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	lines := strings.Join([]string{
		`{"word":"happy","pos":"adj","lang":"English","senses":[{"glosses":["Feeling joy"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u062e\u0648\u0634\u062d\u0627\u0644"}]}`,
		`{"word":"hard","pos":"adj","lang":"English","senses":[{"glosses":["Not soft"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0633\u062e\u062a"}]}`,
		`{"word":"kind","pos":"adj","lang":"English","senses":[{"glosses":["Generous"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0645\u0647\u0631\u0628\u0627\u0646"}]}`,
	}, "\n")
	if err := store.ingestKaikkiStream(context.Background(), strings.NewReader(lines)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := apiCall(store, handleAPIDictionary, http.MethodGet,
		"/api/dictionary?q=ha&prefix=1", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Results []DictEntry `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Errorf("prefix 'ha' results = %d, want 2", len(resp.Results))
	}
}

func TestAPIDictionaryEmptyQuery(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	w := apiCall(store, handleAPIDictionary, http.MethodGet,
		"/api/dictionary?q=", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Results []DictEntry `json:"results"`
		Total   int         `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("empty query results = %d, want 0", len(resp.Results))
	}
}

func TestAPIDictionaryNoResults(t *testing.T) {
	saveToken(t)
	store := testStoreHelper(t)

	w := apiCall(store, handleAPIDictionary, http.MethodGet,
		"/api/dictionary?q=zzzznotaword", 100, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Results []DictEntry `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Wiktionary fallback will also fail for a nonsense word, so should be empty.
	// (In test env, the wiktionary HTTP call may time out or fail — that's fine.)
	// We just check it doesn't panic or return an error status.
}

// ---------------------------------------------------------------------------
// runDictionarySeeder skips when already populated
// ---------------------------------------------------------------------------

func TestDictSeederSkipsWhenPopulated(t *testing.T) {
	store := testStore(t)

	// Seed one entry.
	lines := `{"word":"test","pos":"noun","lang":"English","senses":[{"glosses":["A trial"]}],"translations":[{"lang":"Persian","code":"fa","word":"\u0622\u0632\u0645\u0648\u0646"}]}`
	if err := store.ingestKaikkiStream(context.Background(), strings.NewReader(lines)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// runDictionarySeeder should detect the existing data and return without
	// attempting a download. We can't easily test the "download" path without a
	// real network, but we can verify it doesn't panic and exits immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runDictionarySeeder(ctx, store)

	// Count should still be 1 (no duplicates added).
	if n := store.DictCount(); n != 1 {
		t.Errorf("DictCount after seeder = %d, want 1", n)
	}
}
