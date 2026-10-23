package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

// The published documentation is built from docs/ by GitHub Pages. These tests
// keep it from rotting in the two ways it can: a page that stops being a page,
// and a link that stops pointing at anything.
//
// They live beside the OpenAPI tests rather than in docs/ itself, because a Go
// file under docs/ would be copied into the published site.
const docsDir = "../../docs"

// markdownLink matches [text](target), which is every internal link these pages
// use. Reference-style links and bare URLs are not used here and are not
// matched; a test that silently skips what it cannot parse would be worse than
// no test, so TestEveryDocIsAPage counts what it found.
var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

func docFiles(t *testing.T) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no markdown under docs/")
	}
	return found
}

// Jekyll renders a markdown file as a page only if it has front matter.
// Without it the file is copied verbatim, so visiting it downloads raw
// markdown instead of showing a page -- which looks like a broken site rather
// than a missing one.
func TestEveryDocIsAPage(t *testing.T) {
	for _, path := range docFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s has no front matter, so Jekyll will serve it as a raw download "+
				"rather than render it. Add:\n---\ntitle: \"...\"\ndescription: \"...\"\n---", path)
			continue
		}
		end := strings.Index(text[4:], "\n---\n")
		if end < 0 {
			t.Errorf("%s opens front matter and never closes it", path)
			continue
		}

		// Parsed, not pattern-matched. Checking that "description:" appears
		// somewhere would have passed the page whose description contained a
		// colon -- which is a YAML mapping error, and a build failure for the
		// whole site rather than a bad page. That is exactly what this missed
		// the first time.
		var front struct {
			Title       string `yaml:"title"`
			Description string `yaml:"description"`
		}
		if err := yaml.UnmarshalStrict([]byte(text[4:4+end]), &front); err != nil {
			t.Errorf("%s front matter is not valid YAML, which fails the site build, not just "+
				"this page: %v\nQuote the values.", path, err)
			continue
		}
		if front.Title == "" {
			t.Errorf("%s front matter has no title", path)
		}
		if front.Description == "" {
			t.Errorf("%s front matter has no description", path)
		}
	}
}

// A relative link has to resolve to something. The site writes them without
// the .md -- ./SPEC renders as a page -- so each candidate spelling is tried.
func TestDocLinksResolve(t *testing.T) {
	checked := 0
	for _, path := range docFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(path)
		for _, match := range markdownLink.FindAllStringSubmatch(string(raw), -1) {
			target := match[1]
			switch {
			case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"),
				strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "#"):
				continue
			}
			// Drop any anchor: the file is what has to exist.
			if i := strings.Index(target, "#"); i >= 0 {
				target = target[:i]
			}
			if target == "" {
				continue
			}
			checked++
			if !resolves(filepath.Join(dir, target)) {
				t.Errorf("%s links to %q, which resolves to nothing", path, match[1])
			}
		}
	}
	// A regex that stopped matching would make this test pass by finding
	// nothing to check. The floor is deliberately well below the real count --
	// it exists to catch the pattern breaking entirely, not to be a number
	// that has to be maintained every time a link is added or removed.
	if checked < 5 {
		t.Errorf("checked only %d relative links; the link pattern has probably stopped matching", checked)
	}
}

// resolves tries the spellings a published page and a repository file use for
// the same document: the literal path, the page without its .md, and a
// directory's index.
func resolves(path string) bool {
	path = filepath.Clean(path)
	for _, candidate := range []string{path, path + ".md", filepath.Join(path, "index.md")} {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

// The guide points at the generated spec, and the spec is the one file in
// docs/ that must NOT become a page: Jekyll copies it verbatim only while it
// has no front matter.
func TestSpecIsServedAsAFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(docsDir, "api", "swagger.json"))
	if err != nil {
		t.Fatalf("read the published spec: %v", err)
	}
	if strings.HasPrefix(string(raw), "---") {
		t.Error("swagger.json has front matter; Jekyll would try to render it as a page")
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		t.Error("swagger.json is not JSON")
	}
}
