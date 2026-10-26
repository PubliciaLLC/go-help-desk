package notify

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"text/template/parse"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// The HTML part of a template (#158, PR 2).
//
// It is html/template, so the values are escaped for the place they land in.
// The admin's own markup is another matter: a template that was valid text is
// not necessarily safe to mail as HTML, so the source is checked at save and
// REFUSED, with the reason, when it holds anything outside the allow-list.
// Nothing is cleaned up quietly: an editor that rewrites what you typed leaves
// the preview and the mail disagreeing, and a refusal names the thing to fix.
//
// Why refuse at all, when the author is a trusted administrator: mail clients
// remove these constructs, so a preview that kept them would lie about what a
// recipient sees; and a form in a password-reset mail is the best payload a
// stolen administrator session could plant.

const (
	// MaxHTMLBytes caps the source. MaxRenderedHTMLBytes caps what is sent:
	// Gmail clips a message near 102 KB, and the part that carries the link
	// should not be the part it cuts off.
	MaxHTMLBytes         = 64 * 1024
	MaxRenderedHTMLBytes = 100 * 1024
)

// Stand-ins for a template action while the markup is read as HTML. They are
// private-use characters; any the author typed are replaced first, so a
// sentinel always means an action.
const (
	sentAction = '' // {{.Name}}
	sentBlock  = '' // {{if}}…{{end}} or a template comment
	sentinels  = ""
)

// refusedElements are the elements the design names, each with why.
var refusedElements = map[string]string{
	"script":   "mail clients remove scripts, so the preview would not show what recipients get, and a script is what a stolen session would plant",
	"iframe":   "mail clients remove embedded pages",
	"frame":    "mail clients remove embedded pages",
	"object":   "mail clients remove embedded content",
	"embed":    "mail clients remove embedded content",
	"form":     "a form does not work in mail, and one in a mail is a way to collect passwords",
	"input":    "a form does not work in mail, and one in a mail is a way to collect passwords",
	"button":   "a form does not work in mail, and one in a mail is a way to collect passwords",
	"select":   "a form does not work in mail, and one in a mail is a way to collect passwords",
	"textarea": "a form does not work in mail, and one in a mail is a way to collect passwords",
	"base":     "mail clients remove it, and it would change where every link goes",
	"link":     "mail clients remove it; put styles in a <style> block or a style attribute",
	"meta":     "mail clients remove it, and a refresh or a different character set would change how the mail is read",
	"svg":      "mail clients do not show it; use an <img> of an https: address",
	"math":     "mail clients do not show it",
}

// allowedElements is the markup email can be relied on to carry. An element
// that is neither here nor in refusedElements is refused too: an allow-list
// that only lists what is forbidden stops being a list the day a new element
// is invented.
var allowedElements = map[string]bool{
	"html": true, "head": true, "body": true, "style": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
	"caption": true, "colgroup": true, "col": true,
	"div": true, "p": true, "span": true, "a": true, "img": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"b": true, "strong": true, "i": true, "em": true, "u": true, "s": true, "small": true, "big": true,
	"sub": true, "sup": true, "center": true, "font": true, "blockquote": true, "pre": true, "code": true, "abbr": true,
}

// refusedAttributes are refused whatever the element.
var refusedAttributes = map[string]string{
	"srcdoc":     "it embeds a page",
	"formaction": "it submits a form",
}

const templatesWhere = "a template ({{ }}) is allowed only in the text or as the whole href value of a link"

// ValidateHTML checks the HTML source of a template, in this order: bytes and
// size, the template parses to the allowed nodes, the markup holds only what
// is allowed, the link the mail exists to deliver is in it, and it renders
// against sample data. It reports the first problem, by position in the text.
// Empty source means the template has no HTML part and is always fine.
func ValidateHTML(def TemplateDef, src string) []TemplateError {
	if src == "" {
		return nil
	}
	if te := checkHTML(def, src); te != nil {
		return []TemplateError{*te}
	}
	return nil
}

func checkHTML(def TemplateDef, src string) *TemplateError {
	fail := func(line, col int, format string, a ...any) *TemplateError {
		return &TemplateError{Field: "html", Line: line, Col: col, Message: fmt.Sprintf(format, a...)}
	}
	if !utf8.ValidString(src) {
		return fail(0, 0, "must be valid UTF-8")
	}
	if strings.ContainsRune(src, 0) {
		return fail(0, 0, "must not contain a NUL byte")
	}
	if len(src) > MaxHTMLBytes {
		return fail(0, 0, "is %d bytes; the limit is %d", len(src), MaxHTMLBytes)
	}

	tree, w, te := parseAndWalk(def, "html", src)
	if te != nil {
		return te
	}
	c := &htmlCheck{src: src}
	c.list(tree.Root)
	if c.err != nil {
		return c.err
	}
	if !def.Shell && !w.foundRequired {
		return fail(1, 1, "must include {{.%s}} outside any {{if}}; without it the person cannot reach the link", def.Required)
	}
	if _, err := executeHTML(def, src, SampleVars(def, "https://help.example.com", "GHD")); err != nil {
		return fail(0, 0, "%s", err.Error())
	}
	return nil
}

// htmlCheck reads the markup. The source cannot be handed to an HTML tokenizer
// as it is, because it is not HTML until the actions have run; and a branch
// of an {{if}} must not be able to finish a tag another one began. So every
// list of nodes (the top level, each branch) is read ON ITS OWN, with each
// action replaced by a sentinel, and must come out whole: no tag, comment or
// <style> left open, no bare "<". A tag that is whole in every list is whole
// whichever branches run, so the tags the check sees are the tags that render.
type htmlCheck struct {
	src string
	err *TemplateError
	pos int // source offset of err, to keep the earliest
}

// skeleton is one list as markup, with the source offset of every byte.
type skeleton struct {
	b   []byte
	src []int
}

func (s *skeleton) text(t string, at int) {
	// A sentinel the author typed would pass for an action.
	t = strings.NewReplacer("", "�", "", "�").Replace(t)
	for i := 0; i < len(t); i++ {
		s.b = append(s.b, t[i])
		s.src = append(s.src, at+i)
	}
}

func (s *skeleton) sentinel(r rune, at int) {
	for i := 0; i < utf8.RuneLen(r); i++ {
		s.src = append(s.src, at)
	}
	s.b = utf8.AppendRune(s.b, r)
}

func (c *htmlCheck) list(l *parse.ListNode) {
	if l == nil {
		return
	}
	sk := &skeleton{}
	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			sk.text(string(n.Text), int(n.Pos))
		case *parse.ActionNode:
			sk.sentinel(sentAction, c.openAt(n.Pos))
		case *parse.CommentNode:
			sk.sentinel(sentBlock, c.openAt(n.Pos))
		case *parse.IfNode:
			sk.sentinel(sentBlock, c.openAt(n.Pos))
			c.list(n.List)
			c.list(n.ElseList)
		}
	}
	c.scan(sk)
}

// openAt moves a node's position back to the "{{" that opens it.
func (c *htmlCheck) openAt(pos parse.Pos) int {
	p := min(int(pos), len(c.src))
	if i := strings.LastIndex(c.src[:p], "{{"); i >= 0 {
		return i
	}
	return p
}

// fail keeps the problem earliest in the text, so lists read in any order give
// the same answer.
func (c *htmlCheck) fail(sk *skeleton, at int, format string, a ...any) {
	off := 0
	if len(sk.src) > 0 {
		off = sk.src[min(max(at, 0), len(sk.src)-1)]
	}
	if c.err != nil && off >= c.pos {
		return
	}
	line, col := lineCol(c.src, off)
	c.err = &TemplateError{Field: "html", Line: line, Col: col, Message: fmt.Sprintf(format, a...)}
	c.pos = off
}

func (c *htmlCheck) scan(sk *skeleton) {
	z := html.NewTokenizer(strings.NewReader(string(sk.b)))
	off, styleAt, inStyle := 0, 0, false
	for {
		tt := z.Next()
		raw := string(z.Raw()) // copied: the next call reuses the buffer
		at := off
		off += len(raw)

		// A comment that does not end with ">" ran into the end of the list, and
		// whatever follows the list in the template would finish it.
		if (tt == html.CommentToken || tt == html.DoctypeToken) && !strings.HasSuffix(raw, ">") {
			c.fail(sk, at, "this comment is not finished; close it with -->")
			return
		}
		switch tt {
		case html.ErrorToken:
			switch {
			case raw != "" && strings.HasPrefix(raw, "<!--"):
				c.fail(sk, at, "this comment is not finished; close it with -->")
			case raw != "":
				c.fail(sk, at, "this tag is not finished; end it with > before the end of the text or the {{if}} branch")
			case inStyle:
				c.fail(sk, styleAt, "this <style> is not closed; add </style> in the same text or {{if}} branch")
			}
			return

		case html.TextToken:
			if inStyle && strings.ContainsAny(raw, sentinels) {
				c.fail(sk, at+strings.IndexAny(raw, sentinels), "a template is not allowed inside <style>")
				return
			}
			if i := strings.IndexByte(raw, '<'); i >= 0 {
				if next := raw[i+1:]; next != "" && strings.ContainsRune(sentinels, []rune(next)[0]) {
					c.fail(sk, at+i, "%s", templatesWhere)
				} else {
					c.fail(sk, at+i, "write &lt; for a literal < in the text; a < starts a tag")
				}
				return
			}

		case html.CommentToken, html.DoctypeToken:
			if strings.ContainsAny(raw, sentinels) {
				c.fail(sk, at+strings.IndexAny(raw, sentinels), "%s", templatesWhere)
				return
			}

		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			if !c.tag(sk, tok, raw, at) {
				return
			}
			name := tok.Data
			switch {
			case name == "style" && tt == html.StartTagToken:
				inStyle, styleAt = true, at
			case name == "style" && tt == html.EndTagToken:
				inStyle = false
			}
		}
	}
}

// tag checks one start or end tag and reports whether the scan may go on.
func (c *htmlCheck) tag(sk *skeleton, tok html.Token, raw string, at int) bool {
	name, closing := tok.Data, strings.HasPrefix(raw, "</")
	shown := "<" + name + ">"
	if closing {
		shown = "</" + name + ">"
	}
	if strings.ContainsAny(name, sentinels) {
		c.fail(sk, at, "%s", templatesWhere)
		return false
	}
	if why, bad := refusedElements[name]; bad {
		c.fail(sk, at, "%s is not allowed: %s", shown, why)
		return false
	}
	if !allowedElements[name] {
		c.fail(sk, at, "%s is not allowed: email clients do not show it reliably; use tables, div, p, span, a, img, headings or lists", shown)
		return false
	}
	accepted := 0
	for _, a := range tok.Attr {
		key, val := a.Key, a.Val
		switch {
		case strings.HasPrefix(key, "on"):
			c.fail(sk, at, "attribute %q is not allowed: event handlers are scripts", key)
			return false
		case refusedAttributes[key] != "":
			c.fail(sk, at, "attribute %q is not allowed: %s", key, refusedAttributes[key])
			return false
		case strings.ContainsAny(key, sentinels):
			c.fail(sk, at, "%s", templatesWhere)
			return false
		}
		switch key {
		case "href":
			if val == string(sentAction) {
				accepted++ // a server-built link, the whole value
				continue
			}
			if strings.ContainsAny(val, sentinels) {
				c.fail(sk, at, "%s", templatesWhere)
				return false
			}
			if !urlHasScheme(val, "https:", "http:", "mailto:") {
				c.fail(sk, at, "href must start with https:, http: or mailto:, or be exactly one {{.Variable}}; a javascript:, data: or relative address is not allowed")
				return false
			}
		case "src", "background":
			if !urlHasScheme(val, "https:") {
				c.fail(sk, at, "%s must start with https:; a http:, data: or relative address is not allowed", key)
				return false
			}
			if strings.ContainsAny(val, sentinels) {
				c.fail(sk, at, "%s", templatesWhere)
				return false
			}
		default:
			if strings.ContainsAny(val, sentinels) {
				c.fail(sk, at, "%s", templatesWhere)
				return false
			}
		}
	}
	// Every sentinel in the tag must be one accepted above; one anywhere else
	// (between attributes, in an odd spot the tokenizer folds away) is not.
	if strings.Count(raw, string(sentAction))+strings.Count(raw, string(sentBlock)) != accepted {
		c.fail(sk, at, "%s", templatesWhere)
		return false
	}
	return true
}

// urlHasScheme reports whether a URL starts with one of the schemes, the way a
// browser reads it: character references are already decoded by the tokenizer,
// tabs and line breaks anywhere in the URL are dropped, and leading or
// trailing control characters and spaces are trimmed. Otherwise "java\tscript:"
// and " javascript:" would pass as relative addresses.
func urlHasScheme(raw string, schemes ...string) bool {
	u := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, raw)
	u = strings.ToLower(strings.TrimFunc(u, func(r rune) bool { return r <= 0x20 }))
	for _, s := range schemes {
		if strings.HasPrefix(u, s) {
			return true
		}
	}
	return false
}

func executeHTML(def TemplateDef, src string, vars map[string]string) (string, error) {
	t, err := htmltemplate.New(def.Name).Option("missingkey=error").Parse(src)
	if err != nil {
		return "", fmt.Errorf("parsing: %w", err)
	}
	var b strings.Builder
	w := &limitedWriter{w: &b, max: MaxRenderedHTMLBytes, err: errors.New("the HTML is longer than 100 KiB, which mail clients cut off")}
	if err := t.Execute(w, vars); err != nil {
		return "", fmt.Errorf("rendering: %w", err)
	}
	return b.String(), nil
}

// RenderHTML validates the HTML source and renders it with vars. Empty source
// is no HTML part and renders as "". The error is an *InvalidTemplateError
// when the source is refused.
func RenderHTML(def TemplateDef, src string, vars map[string]string) (string, error) {
	if errs := ValidateHTML(def, src); len(errs) > 0 {
		return "", &InvalidTemplateError{Errors: errs}
	}
	if src == "" {
		return "", nil
	}
	return executeHTML(def, src, vars)
}
