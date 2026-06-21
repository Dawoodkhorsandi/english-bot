package app

import "testing"

// TestScorePronunciation covers the match-scoring tiers and tolerance to filler.
func TestScorePronunciation(t *testing.T) {
	// Exact match → excellent.
	if s, _ := scorePronunciation("thorough", "thorough"); s != 100 {
		t.Fatalf("exact match = %d, want 100", s)
	}
	// Leading filler around the target still scores high (best-word match).
	if s, _ := scorePronunciation("thorough", "the word is thorough"); s < 90 {
		t.Fatalf("filler-wrapped match = %d, want >= 90", s)
	}
	// A near miss scores in the middle, not at the top.
	if s, _ := scorePronunciation("thorough", "thurow"); s >= 90 || s < 40 {
		t.Fatalf("near miss = %d, want 40..89", s)
	}
	// A totally different word scores low.
	if s, _ := scorePronunciation("thorough", "banana"); s >= 40 {
		t.Fatalf("wrong word = %d, want < 40", s)
	}
}

// TestNormalizeSpeech strips punctuation/case and treats hyphens as spaces.
func TestNormalizeSpeech(t *testing.T) {
	if got := normalizeSpeech("  Well-Being!  "); got != "well being" {
		t.Fatalf("normalizeSpeech = %q, want \"well being\"", got)
	}
}
