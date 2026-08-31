package hygiene_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsLinksResolve guards the README documentation indexes: every
// relative markdown link in the READMEs and docs/ guides must point at a
// file that exists. A moved or renamed topic file otherwise 404s silently
// on GitHub (the index once linked docs/foo.md while the files live under
// docs/en/ and docs/zh/).
func TestDocsLinksResolve(t *testing.T) {
	files := []string{"README.md", "README.zh-CN.md"}
	err := filepath.WalkDir("docs", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	if len(files) < 10 {
		t.Fatalf("expected markdown files to check, found %d", len(files))
	}

	checked := 0
	var broken []string
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, target := range relativeLinkTargets(string(raw)) {
			checked++
			resolved := filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				broken = append(broken, file+" -> "+target)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("expected a meaningful number of links, checked %d", checked)
	}
	if len(broken) > 0 {
		t.Fatalf("broken documentation links (%d of %d checked):\n%s",
			len(broken), checked, strings.Join(broken, "\n"))
	}
}

// TestReadmeIndexesStaySingleLanguage pins the language separation of the
// documentation indexes: the English README must not link Chinese guides
// (docs/zh/) and the Chinese README must not link English guides (docs/en/).
func TestReadmeIndexesStaySingleLanguage(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, target := range relativeLinkTargets(string(raw)) {
		if strings.HasPrefix(target, "docs/zh/") {
			t.Fatalf("README.md (English) links the Chinese guide %s — "+
				"each language's index must reference only its own guides", target)
		}
	}
	raw, err = os.ReadFile("README.zh-CN.md")
	if err != nil {
		t.Fatalf("read README.zh-CN.md: %v", err)
	}
	for _, target := range relativeLinkTargets(string(raw)) {
		if strings.HasPrefix(target, "docs/en/") {
			t.Fatalf("README.zh-CN.md (Chinese) links the English guide %s — "+
				"each language's index must reference only its own guides", target)
		}
	}
}

// relativeLinkTargets extracts ](target) link targets from markdown text,
// skipping web links, mailto, and pure-#anchor targets.
func relativeLinkTargets(text string) []string {
	var out []string
	for {
		idx := strings.Index(text, "](")
		if idx < 0 {
			break
		}
		rest := text[idx+2:]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			break
		}
		target := rest[:end]
		path := target
		if i := strings.IndexByte(target, '#'); i >= 0 {
			path = target[:i]
		}
		if path != "" &&
			!strings.HasPrefix(target, "http://") &&
			!strings.HasPrefix(target, "https://") &&
			!strings.HasPrefix(target, "mailto:") {
			out = append(out, path)
		}
		text = rest[end:]
	}
	return out
}
