package config

import (
	"testing"
	"time"
)

func TestGetEnvInt(t *testing.T) {
	t.Setenv("TEST_INT_VALID", "42")
	if got := GetEnvInt("TEST_INT_VALID", 10); got != 42 {
		t.Errorf("valid: got %d, want 42", got)
	}
	t.Setenv("TEST_INT_BAD", "notanint")
	if got := GetEnvInt("TEST_INT_BAD", 10); got != 10 {
		t.Errorf("invalid: got %d, want fallback 10", got)
	}
	if got := GetEnvInt("TEST_INT_MISSING_XYZ", 99); got != 99 {
		t.Errorf("missing: got %d, want fallback 99", got)
	}
}

func TestGetEnvDuration(t *testing.T) {
	t.Setenv("TEST_DUR", "5m")
	if got := GetEnvDuration("TEST_DUR", time.Second); got != 5*time.Minute {
		t.Errorf("got %v, want 5m", got)
	}
	t.Setenv("TEST_DUR_BAD", "five")
	if got := GetEnvDuration("TEST_DUR_BAD", 30*time.Second); got != 30*time.Second {
		t.Errorf("invalid: got %v, want fallback 30s", got)
	}
}

func TestGetEnvBool(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on", "ON", "True"} {
		t.Setenv("TEST_BOOL", v)
		if !GetEnvBool("TEST_BOOL", false) {
			t.Errorf("%q should parse true", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off"} {
		t.Setenv("TEST_BOOL", v)
		if GetEnvBool("TEST_BOOL", true) {
			t.Errorf("%q should parse false", v)
		}
	}
	t.Setenv("TEST_BOOL", "maybe")
	if !GetEnvBool("TEST_BOOL", true) {
		t.Error("unrecognized should fall back to default (true)")
	}
}

func TestGetEnvWeekday(t *testing.T) {
	cases := map[string]int{"sunday": 0, "Mon": 1, "tuesday": 2, "sat": 6, "3": 3}
	for in, want := range cases {
		t.Setenv("TEST_WD", in)
		if got := GetEnvWeekday("TEST_WD", -99); got != want {
			t.Errorf("GetEnvWeekday(%q) = %d, want %d", in, got, want)
		}
	}
	for _, off := range []string{"off", "none", "disabled"} {
		t.Setenv("TEST_WD", off)
		if got := GetEnvWeekday("TEST_WD", 5); got != -1 {
			t.Errorf("GetEnvWeekday(%q) = %d, want -1 (disabled)", off, got)
		}
	}
	t.Setenv("TEST_WD", "funday")
	if got := GetEnvWeekday("TEST_WD", 4); got != 4 {
		t.Errorf("invalid weekday: got %d, want fallback 4", got)
	}
}

func TestIsConfigurableKind(t *testing.T) {
	if !IsConfigurableKind(KindWord) {
		t.Error("word should be configurable")
	}
	if IsConfigurableKind("bogus") {
		t.Error("bogus should not be configurable")
	}
}

func TestResolvePoolTargetPrecedence(t *testing.T) {
	// Save and restore both the globals we mutate and the override maps.
	origTarget, origMin := PoolTarget, PoolMin
	kl, k, l := SnapshotOverrides()
	t.Cleanup(func() {
		PoolTarget, PoolMin = origTarget, origMin
		RestoreOverrides(kl, k, l)
	})
	PoolTarget, PoolMin = 300, 100
	RestoreOverrides(map[string]int{}, map[string]int{}, map[string]int{})

	// Global rule: default level -> PoolTarget, other level -> PoolMin.
	if got := ResolvePoolTarget(KindWord, DefaultLevel); got != 300 {
		t.Errorf("default-level global: got %d, want 300", got)
	}
	if got := ResolvePoolTarget(KindWord, LevelAdvanced); got != 100 {
		t.Errorf("non-default global: got %d, want 100", got)
	}

	// per-level overrides the global.
	SetPoolLevelOverride(LevelAdvanced, 40)
	if got := ResolvePoolTarget(KindWord, LevelAdvanced); got != 40 {
		t.Errorf("per-level: got %d, want 40", got)
	}
	// per-kind overrides per-level.
	SetPoolKindOverride(KindWord, 55)
	if got := ResolvePoolTarget(KindWord, LevelAdvanced); got != 55 {
		t.Errorf("per-kind beats per-level: got %d, want 55", got)
	}
	// per-(kind,level) is the most specific and wins.
	SetPoolKindLevelOverride(KindWord, LevelAdvanced, 7)
	if got := ResolvePoolTarget(KindWord, LevelAdvanced); got != 7 {
		t.Errorf("per-(kind,level) is most specific: got %d, want 7", got)
	}
	// Clearing it falls back to the next tier (per-kind).
	SetPoolKindLevelOverride(KindWord, LevelAdvanced, 0)
	if got := ResolvePoolTarget(KindWord, LevelAdvanced); got != 55 {
		t.Errorf("after clear falls back to per-kind: got %d, want 55", got)
	}
}
