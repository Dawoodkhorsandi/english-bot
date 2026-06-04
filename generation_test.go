package main

import (
	"fmt"
	"strings"
	"testing"
)

// sampleDrill builds a drill fixture with n numbered forms in the canonical
// format (header + numbered forms + 💡 footer) for pagination tests.
func sampleDrill(n int) string {
	var b strings.Builder
	b.WriteString("🎯 <b>Verb of the Hour: walk</b>\n")
	b.WriteString("————————————————————\n\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "<b>%d. Form %d</b> · desc\n→ She <b>walks</b> sentence %d.\n\n", i, i, i)
	}
	b.WriteString("💡 <i>Say each sentence out loud — build the muscle memory!</i>")
	return b.String()
}

// ---------------------------------------------------------------------------
// parseVerb
// ---------------------------------------------------------------------------

func TestParseVerb(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "real drill output",
			input: `🎯 <b>Verb of the Hour: walk</b>
————————————————————

<b>1. Simple Present</b> · Routine / Habit
→ She <b>walks</b> to school every morning.`,
			want: "walk",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "malformed input no colon",
			input: "Verb of the Hour walk",
			want:  "",
		},
		{
			name:  "malformed input no label",
			input: "Some random text: walk",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVerb(tt.input); got != tt.want {
				t.Errorf("parseVerb() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseWord
// ---------------------------------------------------------------------------

func TestParseWord(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "real vocab card",
			input: `📘 <b>Word of the Hour: tedious</b>
————————————————————

💬 <b>Meaning</b>
Too long, slow, or dull; tiresome or monotonous.`,
			want: "tedious",
		},
		{
			name:  "word with closing HTML tag after colon",
			input: "📘 <b>Word of the Hour: resilient</b>",
			want:  "resilient",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseWord(tt.input); got != tt.want {
				t.Errorf("parseWord() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseLabeledTerm
// ---------------------------------------------------------------------------

func TestParseLabeledTerm(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		label string
		want  string
	}{
		{
			name:  "term ending in b (grab)",
			text:  "🎯 <b>Verb of the Hour: grab</b>",
			label: "verb of the hour",
			want:  "grab",
		},
		{
			name:  "term with HTML closing tag",
			text:  "<b>Word of the Hour: bright</b>",
			label: "word of the hour",
			want:  "bright",
		},
		{
			name:  "multiple words after colon takes first",
			text:  "Word of the Hour: get along",
			label: "word of the hour",
			want:  "get",
		},
		{
			name:  "extra whitespace and markdown",
			text:  "Word of the Hour:  **bold**  ",
			label: "word of the hour",
			want:  "bold",
		},
		{
			name:  "no match returns empty",
			text:  "nothing here",
			label: "verb of the hour",
			want:  "",
		},
		{
			name:  "label present but no colon",
			text:  "Verb of the Hour walk",
			label: "verb of the hour",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLabeledTerm(tt.text, tt.label); got != tt.want {
				t.Errorf("parseLabeledTerm() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseMeaning
// ---------------------------------------------------------------------------

func TestParseMeaning(t *testing.T) {
	tests := []struct {
		name string
		card string
		want string
	}{
		{
			name: "real vocab card",
			card: `📘 <b>Word of the Hour: tedious</b>
————————————————————

💬 <b>Meaning</b>
Too long, slow, or dull; tiresome or monotonous.

🔊 <b>Pronunciation</b>`,
			want: "Too long, slow, or dull; tiresome or monotonous.",
		},
		{
			name: "meaning with HTML in value",
			card: `💬 <b>Meaning</b>
<i>Showing great attention</i> to detail.`,
			want: "Showing great attention to detail.",
		},
		{
			name: "empty input",
			card: "",
			want: "",
		},
		{
			name: "missing meaning section",
			card: "📘 <b>Word of the Hour: tedious</b>\nNo meaning here.",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseMeaning(tt.card); got != tt.want {
				t.Errorf("parseMeaning() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// stripHTMLTags
// ---------------------------------------------------------------------------

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple bold", "<b>hello</b>", "hello"},
		{"nested tags", "<div><b>hello</b></div>", "hello"},
		{"empty string", "", ""},
		{"no tags", "just plain text", "just plain text"},
		{"self-closing", "before<br/>after", "beforeafter"},
		{"multiple tags", "<i>a</i> and <b>b</b>", "a and b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHTMLTags(tt.input); got != tt.want {
				t.Errorf("stripHTMLTags() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// levelInstruction
// ---------------------------------------------------------------------------

func TestLevelInstruction(t *testing.T) {
	results := map[string]string{
		levelBeginner:     levelInstruction(levelBeginner),
		levelIntermediate: levelInstruction(levelIntermediate),
		levelAdvanced:     levelInstruction(levelAdvanced),
	}

	// All should be non-empty and distinct.
	for level, result := range results {
		if result == "" {
			t.Errorf("levelInstruction(%q) returned empty string", level)
		}
	}
	if results[levelBeginner] == results[levelIntermediate] ||
		results[levelBeginner] == results[levelAdvanced] ||
		results[levelIntermediate] == results[levelAdvanced] {
		t.Error("levelInstruction should return different strings for each level")
	}
}

// ---------------------------------------------------------------------------
// buildDrillPrompt
// ---------------------------------------------------------------------------

func TestBuildDrillPrompt(t *testing.T) {
	t.Run("no exclusions", func(t *testing.T) {
		prompt := buildDrillPrompt(levelIntermediate, nil)
		if strings.Contains(prompt, "Do NOT use any of these verbs") {
			t.Error("should not contain exclusion clause when exclude list is empty")
		}
	})

	t.Run("with exclusions", func(t *testing.T) {
		prompt := buildDrillPrompt(levelIntermediate, []string{"walk", "run"})
		if !strings.Contains(prompt, "walk, run") {
			t.Error("should contain the excluded verbs")
		}
		if !strings.Contains(prompt, "Do NOT use any of these verbs") {
			t.Error("should contain exclusion clause")
		}
	})
}

// ---------------------------------------------------------------------------
// buildWordPrompt
// ---------------------------------------------------------------------------

func TestBuildWordPrompt(t *testing.T) {
	t.Run("no exclusions", func(t *testing.T) {
		prompt := buildWordPrompt(levelBeginner, nil)
		if strings.Contains(prompt, "Do NOT use any of these words") {
			t.Error("should not contain exclusion clause when exclude list is empty")
		}
	})

	t.Run("with exclusions", func(t *testing.T) {
		prompt := buildWordPrompt(levelBeginner, []string{"tedious", "bright"})
		if !strings.Contains(prompt, "tedious, bright") {
			t.Error("should contain the excluded words")
		}
		if !strings.Contains(prompt, "Do NOT use any of these words") {
			t.Error("should contain exclusion clause")
		}
	})
}

// ---------------------------------------------------------------------------
// buildIdiomPrompt / parseIdiom
// ---------------------------------------------------------------------------

func TestBuildIdiomPrompt(t *testing.T) {
	t.Run("no exclusions", func(t *testing.T) {
		prompt := buildIdiomPrompt(levelIntermediate, nil)
		if strings.Contains(prompt, "Do NOT use any of these idioms") {
			t.Error("should not contain exclusion clause when exclude list is empty")
		}
		if !strings.Contains(prompt, "Idiom of the Day") {
			t.Error("prompt should contain the idiom card header")
		}
	})

	t.Run("with exclusions", func(t *testing.T) {
		prompt := buildIdiomPrompt(levelIntermediate, []string{"break the ice", "piece of cake"})
		if !strings.Contains(prompt, "break the ice, piece of cake") {
			t.Error("should contain the excluded idioms")
		}
		if !strings.Contains(prompt, "Do NOT use any of these idioms") {
			t.Error("should contain exclusion clause")
		}
	})
}

func TestParseIdiom(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "real idiom card keeps full phrase",
			input: `🗣️ <b>Idiom of the Day: break the ice</b>
————————————————————

💬 <b>Meaning</b>
To start a conversation in a relaxed way.`,
			want: "break the ice",
		},
		{
			name:  "phrase with closing tag and no newline",
			input: "🗣️ <b>Idiom of the Day: under the weather</b>",
			want:  "under the weather",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "missing label",
			input: "Word of the Hour: tedious",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIdiom(tt.input); got != tt.want {
				t.Errorf("parseIdiom() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseTipTopic
// ---------------------------------------------------------------------------

func TestParseTipTopic(t *testing.T) {
	tip := `💡 <b>Grammar Tip of the Day</b>
————————————————————
📌 <b>Topic:</b> Using "used to" vs "would"

<b>Rule:</b> Example`
	if got := parseTipTopic(tip); got != `using "used to" vs "would"` {
		t.Errorf("parseTipTopic() = %q, want %q", got, `using "used to" vs "would"`)
	}

	if got := parseTipTopic("no topic here"); got != "" {
		t.Errorf("parseTipTopic(no-topic) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// buildTipPrompt
// ---------------------------------------------------------------------------

func TestBuildTipPrompt(t *testing.T) {
	t.Run("no exclusions", func(t *testing.T) {
		prompt := buildTipPrompt(nil)
		if strings.Contains(prompt, "Avoid these already-pooled grammar topics") {
			t.Error("should not contain exclusion clause when list is empty")
		}
	})

	t.Run("with exclusions", func(t *testing.T) {
		prompt := buildTipPrompt([]string{"used to vs would", "since vs for"})
		if !strings.Contains(prompt, "used to vs would, since vs for") {
			t.Error("should contain excluded topics")
		}
	})
}

// ---------------------------------------------------------------------------
// buildWordLookupPrompt
// ---------------------------------------------------------------------------

func TestBuildWordLookupPrompt(t *testing.T) {
	prompt := buildWordLookupPrompt(levelIntermediate, "apple")

	if !strings.Contains(prompt, "apple") {
		t.Error("prompt should contain the lookup term 'apple'")
	}
	if !strings.Contains(prompt, "LOOKUP MODE") {
		t.Error("prompt should contain 'LOOKUP MODE'")
	}
	if !strings.Contains(prompt, levelInstruction(levelIntermediate)) {
		t.Error("prompt should contain the level instruction text")
	}
	if !strings.Contains(prompt, wordPromptBase) {
		t.Error("prompt should contain the word prompt base pattern")
	}
}

// ---------------------------------------------------------------------------
// Drill pagination
// ---------------------------------------------------------------------------

func TestDrillPromptCoversAllGroups(t *testing.T) {
	// The base prompt must emit at least as many forms as the page groups expect,
	// so every themed page has content.
	want := 0
	for _, g := range drillPageGroups {
		want += g.Size
	}
	got := strings.Count(drillPromptBase, "→ {sentence}")
	if got != want {
		t.Errorf("drillPromptBase has %d forms, drillPageGroups expects %d", got, want)
	}
}

func TestParseDrillBody(t *testing.T) {
	header, items, footer := parseDrillBody(sampleDrill(21))
	if !strings.Contains(header, "Verb of the Hour") {
		t.Errorf("header missing title, got %q", header)
	}
	if len(items) != 21 {
		t.Fatalf("got %d items, want 21", len(items))
	}
	if !strings.Contains(items[0], "1. Form 1") {
		t.Errorf("first item wrong, got %q", items[0])
	}
	if !strings.Contains(footer, "💡") {
		t.Errorf("footer missing tip, got %q", footer)
	}
}

func TestParseDrillBodyUnparseable(t *testing.T) {
	header, items, _ := parseDrillBody("just some plain text, no numbered forms")
	if len(items) != 0 {
		t.Errorf("expected 0 items for unparseable text, got %d", len(items))
	}
	if header == "" {
		t.Error("expected the plain text to become the header")
	}
}

func TestPaginateDrillItems(t *testing.T) {
	tests := []struct {
		n          int
		wantPages  int
		wantTitles []string
	}{
		{21, 5, []string{"Everyday Essentials", "Perfect Tenses", "More Past and Future", "Conditionals", "Modals and More"}},
		{8, 2, []string{"Everyday Essentials", "Perfect Tenses"}},
		{3, 1, []string{"Everyday Essentials"}},
	}
	for _, tt := range tests {
		items := make([]string, tt.n)
		for i := range items {
			items[i] = fmt.Sprintf("item%d", i+1)
		}
		pages := paginateDrillItems(items)
		if len(pages) != tt.wantPages {
			t.Errorf("n=%d: got %d pages, want %d", tt.n, len(pages), tt.wantPages)
			continue
		}
		// No form is ever dropped.
		total := 0
		for _, p := range pages {
			total += len(p.items)
		}
		if total != tt.n {
			t.Errorf("n=%d: pages hold %d items, want %d", tt.n, total, tt.n)
		}
		for i, want := range tt.wantTitles {
			if pages[i].title != want {
				t.Errorf("n=%d page %d title = %q, want %q", tt.n, i+1, pages[i].title, want)
			}
		}
	}
}

func TestPaginateDrillItemsOverflow(t *testing.T) {
	items := make([]string, 25) // 4 more than the 21 the groups define
	for i := range items {
		items[i] = fmt.Sprintf("item%d", i+1)
	}
	pages := paginateDrillItems(items)
	if len(pages) != 5 {
		t.Fatalf("got %d pages, want 5 (last absorbs overflow)", len(pages))
	}
	if got := len(pages[4].items); got != 8 {
		t.Errorf("last page holds %d items, want 8 (4 + 4 overflow)", got)
	}
}

func TestRenderDrillPage(t *testing.T) {
	drill := sampleDrill(21)

	page1, total := renderDrillPage(drill, 1)
	if total != 5 {
		t.Errorf("total pages = %d, want 5", total)
	}
	if !strings.Contains(page1, "Page 1/5") || !strings.Contains(page1, "Everyday Essentials") {
		t.Errorf("page 1 missing indicator/title, got %q", page1)
	}
	if !strings.Contains(page1, "Verb of the Hour") {
		t.Error("page 1 should repeat the header")
	}
	if !strings.Contains(page1, "1. Form 1") || strings.Contains(page1, "5. Form 5") {
		t.Error("page 1 should hold forms 1-4 only")
	}
	if !strings.Contains(page1, "💡") {
		t.Error("page 1 should repeat the footer tip")
	}

	page3, _ := renderDrillPage(drill, 3)
	if !strings.Contains(page3, "More Past and Future") || !strings.Contains(page3, "9. Form 9") {
		t.Errorf("page 3 wrong, got %q", page3)
	}

	// Out-of-range page clamps to the last page.
	clamped, _ := renderDrillPage(drill, 99)
	if !strings.Contains(clamped, "Page 5/5") || !strings.Contains(clamped, "21. Form 21") {
		t.Errorf("out-of-range page should clamp to last, got %q", clamped)
	}
}

func TestRenderDrillPageUnparseable(t *testing.T) {
	text, total := renderDrillPage("no forms here", 2)
	if total != 1 || text != "no forms here" {
		t.Errorf("unparseable drill should be a single verbatim page, got total=%d text=%q", total, text)
	}
}

func TestDrillNavKeyboard(t *testing.T) {
	if kb := drillNavKeyboard("walk", 1, 1); kb != nil {
		t.Error("single page should have no navigation keyboard")
	}

	first := drillNavKeyboard("walk", 1, 5)
	if len(first[0]) != 2 || first[0][1].CallbackData != "drill:2:walk" {
		t.Errorf("page 1/5 should have indicator + Next(drill:2:walk), got %+v", first[0])
	}

	mid := drillNavKeyboard("walk", 3, 5)
	if len(mid[0]) != 3 {
		t.Fatalf("middle page should have Back + indicator + Next, got %d buttons", len(mid[0]))
	}
	if mid[0][0].CallbackData != "drill:2:walk" || mid[0][2].CallbackData != "drill:4:walk" {
		t.Errorf("middle nav callbacks wrong, got %+v", mid[0])
	}

	last := drillNavKeyboard("walk", 5, 5)
	if len(last[0]) != 2 || last[0][0].CallbackData != "drill:4:walk" {
		t.Errorf("page 5/5 should have Back(drill:4:walk) + indicator, got %+v", last[0])
	}
}
