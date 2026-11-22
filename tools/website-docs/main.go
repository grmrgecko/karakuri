// Command website-docs renders the guides in docs/ into the website's single
// documentation page. `make website` runs it; the page it writes is generated
// output and is rebuilt rather than edited.
package main

import (
	"flag"
	"fmt"
	"html"
	"html/template"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/grmrgecko/karakuri/docs"
)

// repoURL is where a link from a guide to a repository file other than a
// guide points, since the website ships only the rendered guides.
const repoURL = "https://github.com/grmrgecko/karakuri/blob/main/"

var (
	idAttr   = regexp.MustCompile(`id="([^"]+)"`)
	hrefAttr = regexp.MustCompile(`href="([^"]*)"`)
	guideRef = regexp.MustCompile(`^([a-z0-9-]+)\.md(?:#(.+))?$`)
	h2Tag    = regexp.MustCompile(`<h2 id="([^"]+)">(.*?)</h2>`)
	anyTag   = regexp.MustCompile(`<[^>]+>`)
)

// section is one second-level heading of a guide.
type section struct {
	Anchor string
	Label  string
}

// guide is one rendered guide.
type guide struct {
	ID       string
	Title    string
	Summary  string
	Sections []section
	HTML     template.HTML
}

// group is one heading of the index, in catalog order.
type group struct {
	Name   string
	Guides []guide
}

func main() {
	tmplPath := flag.String("template", "tools/website-docs/docs.html.tmpl", "page template")
	out := flag.String("out", "website/docs.html", "page to write")
	flag.Parse()

	if err := run(*tmplPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "website-docs:", err)
		os.Exit(1)
	}
}

// run renders every guide into the template and writes the page.
func run(tmplPath, out string) error {
	// Render each guide, keeping the catalog's group order.
	known := map[string]bool{}
	for _, g := range docs.Guides() {
		known[g.ID] = true
	}
	var groups []group
	for _, g := range docs.Guides() {
		source, ok := docs.Markdown(g.ID)
		if !ok {
			return fmt.Errorf("reading guide %s", g.ID)
		}
		body := rewrite(g.ID, docs.Render(source), known)
		item := guide{ID: g.ID, Title: g.Title, Summary: g.Summary, Sections: sections(body), HTML: template.HTML(body)}
		if len(groups) == 0 || groups[len(groups)-1].Name != g.Group {
			groups = append(groups, group{Name: g.Group})
		}
		groups[len(groups)-1].Guides = append(groups[len(groups)-1].Guides, item)
	}

	// Fill the template.
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return fmt.Errorf("reading template: %w", err)
	}
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("creating %s: %w", out, err)
	}
	if err := tmpl.Execute(f, map[string]any{"Groups": groups}); err != nil {
		f.Close()
		return fmt.Errorf("rendering %s: %w", out, err)
	}
	return f.Close()
}

// rewrite makes one guide's HTML safe to sit beside the others on one page.
// Heading IDs are prefixed with the guide's ID, since every guide has a
// "Notes" heading; links within the guide and to other guides become links
// to those prefixed anchors; links to other repository files go to GitHub.
func rewrite(id, body string, known map[string]bool) string {
	body = idAttr.ReplaceAllString(body, `id="`+id+`--$1"`)
	return hrefAttr.ReplaceAllStringFunc(body, func(attr string) string {
		href := html.UnescapeString(hrefAttr.FindStringSubmatch(attr)[1])
		switch {
		case strings.HasPrefix(href, "#"):
			return `href="#` + id + "--" + html.EscapeString(href[1:]) + `"`
		case strings.Contains(href, "://"), strings.HasPrefix(href, "mailto:"):
			return attr
		}
		if m := guideRef.FindStringSubmatch(href); m != nil && known[m[1]] {
			if m[2] != "" {
				return `href="#` + m[1] + "--" + html.EscapeString(m[2]) + `"`
			}
			return `href="#` + m[1] + `"`
		}
		return `href="` + html.EscapeString(repoURL+path.Join("docs", href)) + `"`
	})
}

// sections lists a rendered guide's second-level headings.
func sections(body string) []section {
	var out []section
	for _, m := range h2Tag.FindAllStringSubmatch(body, -1) {
		out = append(out, section{Anchor: m[1], Label: html.UnescapeString(anyTag.ReplaceAllString(m[2], ""))})
	}
	return out
}
