package server_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The server discards what is between the markers of the built index.html and
// writes its own block there. Anything else in that region is therefore lost
// without a word: a script the bundler put there would stop the app from
// starting, a stylesheet would leave the page unstyled. The region may hold only
// what the server also writes: the icon link and the title.

var (
	tagRE = regexp.MustCompile(`(?is)<(/?)([a-z][a-z0-9-]*)([^>]*)>`)
	relRE = regexp.MustCompile(`(?i)\brel\s*=\s*["']?([^"'\s>]+)`)
)

// headRegionProblems lists what is in the marked region of an index.html that
// the server's block would not put back. Empty means the region is only the
// icon link and the title.
func headRegionProblems(index string) []string {
	o, c := strings.Index(index, "<!-- ghd:head -->"), strings.Index(index, "<!-- /ghd:head -->")
	if o < 0 || c < o {
		return []string{"the markers are missing or out of order"}
	}
	region := index[o+len("<!-- ghd:head -->") : c]
	var problems []string
	for _, m := range tagRE.FindAllStringSubmatch(region, -1) {
		closing, name, attrs := m[1] == "/", strings.ToLower(m[2]), m[3]
		switch {
		case name == "title":
		case name == "link" && !closing && strings.EqualFold(firstGroup(relRE, attrs), "icon"):
		default:
			problems = append(problems, strings.TrimSpace(m[0]))
		}
	}
	// A tag-less remainder (text between the tags) is not expected either, but
	// the title's own text is: remove tags and the title, and see what is left.
	rest := tagRE.ReplaceAllString(region, "")
	if t := regexp.MustCompile(`(?is)<title[^>]*>.*?</title>`).FindString(region); t != "" {
		rest = tagRE.ReplaceAllString(strings.Replace(region, t, "", 1), "")
	}
	if strings.TrimSpace(rest) != "" {
		problems = append(problems, "text outside the title: "+strings.TrimSpace(rest))
	}
	return problems
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func TestHeadRegionProblems(t *testing.T) {
	wrap := func(region string) string {
		return "<html><head><!-- ghd:head -->" + region + "<!-- /ghd:head --><script type=\"module\" src=\"/a.js\"></script></head></html>"
	}
	cases := []struct {
		name   string
		index  string
		wantOK bool
	}{
		{"the icon link and the title", wrap(`
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <title>Go Help Desk</title>
    `), true},
		{"attributes in another order", wrap(`<link href="/favicon.svg" rel="icon"><TITLE>x</TITLE>`), true},
		{"empty", wrap(""), true},
		{"a module script", wrap(`<link rel="icon" href="/f.svg"><script type="module" crossorigin src="/assets/index-abc.js"></script><title>x</title>`), false},
		{"an inline script", wrap(`<title>x</title><script>alert(1)</script>`), false},
		{"a stylesheet", wrap(`<link rel="icon" href="/f.svg"><link rel="stylesheet" crossorigin href="/assets/index.css"><title>x</title>`), false},
		{"a modulepreload", wrap(`<link rel="modulepreload" crossorigin href="/assets/vendor.js"><title>x</title>`), false},
		{"a meta", wrap(`<meta name="viewport" content="width=device-width"><title>x</title>`), false},
		{"a style", wrap(`<style>body{margin:0}</style><title>x</title>`), false},
		{"stray text", wrap(`<title>x</title> hello`), false},
		{"no markers", "<html></html>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := headRegionProblems(tc.index)
			if tc.wantOK {
				require.Empty(t, got)
			} else {
				require.NotEmpty(t, got)
			}
		})
	}
}
