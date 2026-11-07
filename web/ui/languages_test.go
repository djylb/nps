package ui

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLanguagesCoverTags fails for a langtag that the pages or scripts use
// without an entry in languages.xml, and for an entry without both
// languages, so that a new label cannot be left untranslated.
func TestLanguagesCoverTags(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "static", "page", "languages.xml"))
	if err != nil {
		t.Fatal(err)
	}
	ids, errs := languageIDs(b)
	for _, err := range errs {
		t.Error(err)
	}
	// Tags spelled out in full; those built from template or script values
	// are not checked.
	tag := regexp.MustCompile(`langtag=(?:"([a-zA-Z0-9_-]+)"|'([a-zA-Z0-9_-]+)')`)
	for _, dir := range []string{filepath.Join("..", "views"), filepath.Join("..", "static", "js")} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js")) {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range tag.FindAllStringSubmatch(string(src), -1) {
				name := m[1] + m[2]
				if !ids[strings.ToLower(name)] {
					t.Errorf("%s uses langtag %q, which languages.xml lacks", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// emptyEntries may have no text: "word-" is what "word-" + value names when
// the value is empty, and it shows nothing.
var emptyEntries = map[string]bool{"word-": true}

// languageIDs returns the names a langtag may use, the ids of the lang
// entries and the other elements under content, in lower case, and an error
// for each entry that is listed twice or lacks a language. An entry with no
// languages is the same text in both.
func languageIDs(b []byte) (map[string]bool, []error) {
	var doc struct {
		Items []struct {
			XMLName xml.Name
			ID      string `xml:"id,attr"`
			ZhCN    string `xml:"zh-CN"`
			EnUS    string `xml:"en-US"`
			Text    string `xml:",chardata"`
		} `xml:",any"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, []error{err}
	}
	ids := make(map[string]bool, len(doc.Items))
	var errs []error
	for _, it := range doc.Items {
		if it.XMLName.Local != "lang" {
			ids[strings.ToLower(it.XMLName.Local)] = true
			continue
		}
		id := strings.ToLower(it.ID)
		if ids[id] {
			errs = append(errs, fmt.Errorf("languages.xml lists %q twice", it.ID))
		}
		ids[id] = true
		zh, en, text := strings.TrimSpace(it.ZhCN), strings.TrimSpace(it.EnUS), strings.TrimSpace(it.Text)
		switch {
		case zh != "" && en != "":
		case zh == "" && en == "" && (text != "" || emptyEntries[id]):
		default:
			errs = append(errs, fmt.Errorf("languages.xml entry %q lacks zh-CN or en-US", it.ID))
		}
	}
	return ids, errs
}
