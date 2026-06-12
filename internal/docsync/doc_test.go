// Package docsync holds CI tests that keep README.md and DOCS.md in sync with
// the actual codebase: every command, source file, and callback prefix must be
// documented, and the changelog must be well-formed. The tests locate the repo
// root via go.mod so they are independent of the working directory.
package docsync

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/app"
)

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod) above the test directory")
		}
		dir = parent
	}
}

// readRoot reads a file relative to the repo root.
func readRoot(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	return string(data)
}

// appSources returns the concatenated contents of every non-test .go file under
// internal/app — the package that holds the command router and callback dispatch.
func appSources(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	dir := filepath.Join(root, "internal", "app")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/app: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// sourceFiles walks cmd/ and internal/ and returns the base names of every
// non-test .go file. These must each be listed in the docs' architecture blocks.
func sourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			files = append(files, name)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	return files
}

func commandCases(t *testing.T, src string) []string {
	re := regexp.MustCompile(`(?m)^\s*case "(/\w+)"`)
	matches := re.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		t.Fatal("found zero command cases in internal/app — regex may be wrong")
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// TestDocCommands_README verifies that every slash command handled in the
// router switch is mentioned in README.md.
func TestDocCommands_README(t *testing.T) {
	root := repoRoot(t)
	readme := readRoot(t, root, "README.md")
	for _, cmd := range commandCases(t, appSources(t, root)) {
		if !strings.Contains(readme, cmd) {
			t.Errorf("command %s is handled in internal/app but not mentioned in README.md", cmd)
		}
	}
}

// TestDocCommands_DOCS verifies that every slash command handled in the router
// switch is mentioned in DOCS.md.
func TestDocCommands_DOCS(t *testing.T) {
	root := repoRoot(t)
	docs := readRoot(t, root, "DOCS.md")
	for _, cmd := range commandCases(t, appSources(t, root)) {
		if !strings.Contains(docs, cmd) {
			t.Errorf("command %s is handled in internal/app but not mentioned in DOCS.md", cmd)
		}
	}
}

// TestDocSourceFiles_README verifies that every non-test .go file under cmd/ and
// internal/ is listed in the README.md architecture section.
func TestDocSourceFiles_README(t *testing.T) {
	root := repoRoot(t)
	readme := readRoot(t, root, "README.md")
	for _, f := range sourceFiles(t, root) {
		if !strings.Contains(readme, f) {
			t.Errorf("source file %s exists but is not listed in README.md architecture section", f)
		}
	}
}

// TestDocSourceFiles_DOCS verifies that every non-test .go file under cmd/ and
// internal/ is listed in the DOCS.md architecture section.
func TestDocSourceFiles_DOCS(t *testing.T) {
	root := repoRoot(t)
	docs := readRoot(t, root, "DOCS.md")
	for _, f := range sourceFiles(t, root) {
		if !strings.Contains(docs, f) {
			t.Errorf("source file %s exists but is not listed in DOCS.md architecture section", f)
		}
	}
}

// TestDocCallbackPrefixes_DOCS verifies that every top-level callback prefix
// routed via HasPrefix(cb.Data, "...") is documented in DOCS.md.
func TestDocCallbackPrefixes_DOCS(t *testing.T) {
	root := repoRoot(t)
	docs := readRoot(t, root, "DOCS.md")
	src := appSources(t, root)

	re := regexp.MustCompile(`HasPrefix\(cb\.Data,\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		t.Fatal("found zero callback prefixes in internal/app — regex may be wrong")
	}
	for _, m := range matches {
		prefix := m[1]
		if !strings.Contains(docs, prefix) {
			t.Errorf("callback prefix %q is routed in handleCallback but not documented in DOCS.md", prefix)
		}
	}
}

// TestDocChangelogVersionFormat checks that every changelog entry has a
// semver-style version string and non-empty text (silent entries may omit text).
func TestDocChangelogVersionFormat(t *testing.T) {
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for i, entry := range app.Changelogs {
		if !semver.MatchString(entry.Version) {
			t.Errorf("Changelogs[%d].Version = %q — expected semver format (X.Y.Z)", i, entry.Version)
		}
		if !entry.Silent && strings.TrimSpace(entry.Text) == "" {
			t.Errorf("Changelogs[%d] (v%s) has empty Text (set Silent=true for internal releases)", i, entry.Version)
		}
	}
}

// TestDocChangelogVersionsUnique checks that no two changelog entries share the
// same version string.
func TestDocChangelogVersionsUnique(t *testing.T) {
	seen := make(map[string]int)
	for i, entry := range app.Changelogs {
		if prev, ok := seen[entry.Version]; ok {
			t.Errorf("Changelogs[%d] and [%d] both have version %q", prev, i, entry.Version)
		}
		seen[entry.Version] = i
	}
}
