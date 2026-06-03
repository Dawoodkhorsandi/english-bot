package main

import (
	"strings"
	"testing"
)

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
