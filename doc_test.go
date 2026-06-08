package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Documentation sync tests
//
// These tests ensure that README.md and DOCS.md stay in sync with the actual
// codebase. They run in CI on every push/PR and will fail if a new command,
// source file, or callback prefix is added without updating the docs.
// ---------------------------------------------------------------------------

// readFile reads a file relative to the module root (the working directory
// when `go test` runs).
func readDocFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	return string(data)
}

// TestDocCommands_README verifies that every slash command handled in the
// router switch is mentioned in README.md's command tables.
func TestDocCommands_README(t *testing.T) {
	readme := readDocFile(t, "README.md")
	mainGo := readDocFile(t, "main.go")

	// Extract all `case "/xyz":` lines from the router switch.
	re := regexp.MustCompile(`(?m)^\s*case "(/\w+)"`)
	matches := re.FindAllStringSubmatch(mainGo, -1)
	if len(matches) == 0 {
		t.Fatal("found zero command cases in main.go — regex may be wrong")
	}

	for _, m := range matches {
		cmd := m[1] // e.g. "/start"
		// The README uses backtick-wrapped `/cmd` in markdown table rows.
		// Check for the command appearing anywhere (with or without backticks).
		if !strings.Contains(readme, cmd) {
			t.Errorf("command %s is handled in main.go but not mentioned in README.md", cmd)
		}
	}
}

// TestDocCommands_DOCS verifies that every slash command handled in the
// router switch is mentioned in DOCS.md's command handler table.
func TestDocCommands_DOCS(t *testing.T) {
	docs := readDocFile(t, "DOCS.md")
	mainGo := readDocFile(t, "main.go")

	re := regexp.MustCompile(`(?m)^\s*case "(/\w+)"`)
	matches := re.FindAllStringSubmatch(mainGo, -1)
	if len(matches) == 0 {
		t.Fatal("found zero command cases in main.go — regex may be wrong")
	}

	for _, m := range matches {
		cmd := m[1]
		if !strings.Contains(docs, cmd) {
			t.Errorf("command %s is handled in main.go but not mentioned in DOCS.md", cmd)
		}
	}
}

// TestDocSourceFiles_README verifies that every .go source file (non-test) is
// listed in the README.md architecture section.
func TestDocSourceFiles_README(t *testing.T) {
	readme := readDocFile(t, "README.md")

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob *.go: %v", err)
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		// The architecture block lists files like "+-- main.go" or "├── main.go".
		if !strings.Contains(readme, f) {
			t.Errorf("source file %s exists but is not listed in README.md architecture section", f)
		}
	}
}

// TestDocSourceFiles_DOCS verifies that every .go source file (non-test) is
// listed in the DOCS.md architecture section.
func TestDocSourceFiles_DOCS(t *testing.T) {
	docs := readDocFile(t, "DOCS.md")

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob *.go: %v", err)
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if !strings.Contains(docs, f) {
			t.Errorf("source file %s exists but is not listed in DOCS.md architecture section", f)
		}
	}
}

// TestDocCallbackPrefixes_DOCS verifies that every top-level callback prefix
// routed in handleCallback is documented in DOCS.md.
func TestDocCallbackPrefixes_DOCS(t *testing.T) {
	docs := readDocFile(t, "DOCS.md")
	mainGo := readDocFile(t, "main.go")

	// Extract the handleCallback function body from main.go.
	// We look for top-level routing: HasPrefix(cb.Data, "prefix:")
	start := strings.Index(mainGo, "func handleCallback(")
	if start == -1 {
		t.Fatal("could not find handleCallback in main.go")
	}
	// Take a generous slice — the function is ~100 lines.
	end := start + 3000
	if end > len(mainGo) {
		end = len(mainGo)
	}
	body := mainGo[start:end]

	// Match: HasPrefix(cb.Data, "xxx:")
	re := regexp.MustCompile(`HasPrefix\(cb\.Data,\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("found zero callback prefixes in handleCallback — regex may be wrong")
	}

	for _, m := range matches {
		prefix := m[1] // e.g. "level:", "admin:"
		if !strings.Contains(docs, prefix) {
			t.Errorf("callback prefix %q is routed in handleCallback but not documented in DOCS.md", prefix)
		}
	}
}

// TestDocChangelogVersionFormat checks that every changelog entry has a
// semver-style version string and non-empty text (silent entries may omit text).
func TestDocChangelogVersionFormat(t *testing.T) {
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for i, entry := range Changelogs {
		if !semver.MatchString(entry.Version) {
			t.Errorf("Changelogs[%d].Version = %q — expected semver format (X.Y.Z)", i, entry.Version)
		}
		if !entry.Silent && strings.TrimSpace(entry.Text) == "" {
			t.Errorf("Changelogs[%d] (v%s) has empty Text (set Silent=true for internal releases)", i, entry.Version)
		}
	}
}

// TestDocChangelogVersionsUnique checks that no two changelog entries share
// the same version string.
func TestDocChangelogVersionsUnique(t *testing.T) {
	seen := make(map[string]int)
	for i, entry := range Changelogs {
		if prev, ok := seen[entry.Version]; ok {
			t.Errorf("Changelogs[%d] and [%d] both have version %q", prev, i, entry.Version)
		}
		seen[entry.Version] = i
	}
}
