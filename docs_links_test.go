package hygiene_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsLinksResolve guards the READMEs and the docs/ redirect page:
// every relative markdown link must point at a file that exists. A moved
// or renamed file otherwise 404s silently on GitHub.
//
// The topic manuals moved to the MiBee documentation hub (mibee-docs#6,
// single source of truth); docs/ is intentionally a one-page redirect now.
// The old floors (>=10 markdown files, >=20 links) guarded the removed
// in-repo corpus and no longer apply.
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
	if len(files) < 3 {
		t.Fatalf("expected READMEs + docs redirect page, found %d", len(files))
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
	if checked < 3 {
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

// TestDocsIsSingleRedirectPage pins the post-migration contract
// (mibee-docs#6): docs/ holds exactly one redirect page pointing at the
// documentation hub, so manuals cannot drift back into this repository.
func TestDocsIsSingleRedirectPage(t *testing.T) {
	entries, err := os.ReadDir("docs")
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "README.md" {
		t.Fatalf("docs/ must contain only the redirect README.md, found %d entries", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join("docs", "README.md"))
	if err != nil {
		t.Fatalf("read docs/README.md: %v", err)
	}
	if !strings.Contains(string(raw), "https://www.mlsbs.top/docs/mibeelibs") {
		t.Fatal("docs/README.md must link the documentation hub")
	}
}
