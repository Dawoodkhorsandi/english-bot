package app

import (
	"strconv"
	"testing"
)

// TestExamEstimate covers the accuracy→band/score mapping bounds and monotonicity.
func TestExamEstimate(t *testing.T) {
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("non-numeric estimate %q: %v", s, err)
		}
		return v
	}

	// IELTS: higher accuracy → higher band, capped at 8.5.
	lo, scale := examEstimate("ielts", 20)
	hi, _ := examEstimate("ielts", 90)
	if scale != "band" {
		t.Fatalf("ielts scale = %q, want band", scale)
	}
	if num(lo) >= num(hi) {
		t.Fatalf("ielts not monotonic: 20%%=%s 90%%=%s", lo, hi)
	}
	if cap, _ := examEstimate("ielts", 100); cap != "8.5" {
		t.Fatalf("ielts cap = %s, want 8.5", cap)
	}

	// TOEFL: higher accuracy → higher score, capped at 120.
	tlo, tscale := examEstimate("toefl", 20)
	thi, _ := examEstimate("toefl", 90)
	if tscale != "score" {
		t.Fatalf("toefl scale = %q, want score", tscale)
	}
	if num(tlo) >= num(thi) {
		t.Fatalf("toefl not monotonic: 20%%=%s 90%%=%s", tlo, thi)
	}
	if cap, _ := examEstimate("toefl", 100); cap != "120" {
		t.Fatalf("toefl cap = %s, want 120", cap)
	}
}

// TestExamDeckFor maps an exam target to its bundled deck.
func TestExamDeckFor(t *testing.T) {
	if id, _ := examDeckFor("ielts"); id != "ielts" {
		t.Errorf("ielts → %q, want ielts", id)
	}
	if id, _ := examDeckFor("toefl"); id != "toefl" {
		t.Errorf("toefl → %q, want toefl", id)
	}
	if id, _ := examDeckFor(""); id != "" {
		t.Errorf("none → %q, want empty", id)
	}
}
