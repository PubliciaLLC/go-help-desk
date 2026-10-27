package notify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The HTML part of an email template (#158, PR 2). Whatever the admin writes
// is checked at save against an allow-list and REFUSED with the reason; nothing
// is rewritten. Mail clients strip these constructs, so a preview that kept
// them would lie about what a recipient sees, and a form inside a password
// reset mail is the best payload a stolen admin session has.

func TestValidateHTML_Table(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, tc := range []struct {
		name string
		html string
		// wantMsg is empty for HTML that must be accepted.
		wantLine, wantCol int
		wantMsg           string
	}{
		// ── accepted ──
		{name: "empty means no HTML part", html: ""},
		{name: "text link", html: `<p>Open it: {{.TicketURL}}</p>`},
		{name: "link as the whole href", html: `<p>Hi</p><a href="{{.TicketURL}}">open</a>`},
		{name: "single quoted href", html: `<a href='{{.TicketURL}}'>open</a>`},
		{name: "unquoted href", html: `<a href={{.TicketURL}}>open</a>`},
		{name: "layout markup", html: `<!DOCTYPE html><html><body style="margin:0"><table width="100%" cellpadding="0"><tr><td align="center"><div><h1>Hi</h1><ul><li><span>x</span></li></ul><hr><br>{{.TicketURL}}</div></td></tr></table></body></html>`},
		{name: "style attribute", html: `<p style="color:#123456;font-family:Arial">{{.TicketURL}}</p>`},
		{name: "style block", html: `<style>p { color: red }</style><p>{{.TicketURL}}</p>`},
		{name: "remote image is the admin's choice", html: `<img src="https://cdn.example.com/logo.png" alt="logo" width="120">{{.TicketURL}}`},
		{name: "https http mailto links", html: `<a href="https://a.example">a</a><a href="http://b.example">b</a><a href="mailto:help@example.com">c</a>{{.TicketURL}}`},
		{name: "if around markup", html: `{{if .TrackingNumber}}<p>Ticket {{.TrackingNumber}}</p>{{end}}<p>{{.TicketURL}}</p>`},
		{name: "if else", html: `{{if .TrackingNumber}}<b>a</b>{{else}}<i>b</i>{{end}}{{.TicketURL}}`},
		{name: "ampersand entity", html: `<p>Tom &amp; Jerry &lt;3 {{.TicketURL}}</p>`},
		{name: "a template comment", html: `{{/* the link */}}<p>{{.TicketURL}}</p>`},

		// ── refused elements, each with the reason ──
		{name: "script", html: "<p>{{.TicketURL}}</p>\n<script>alert(1)</script>", wantLine: 2, wantCol: 1, wantMsg: "<script> is not allowed"},
		{name: "iframe", html: `{{.TicketURL}}<iframe src="https://x.example"></iframe>`, wantLine: 1, wantCol: 15, wantMsg: "<iframe> is not allowed"},
		{name: "frame", html: `{{.TicketURL}}<frame>`, wantMsg: "<frame> is not allowed"},
		{name: "object", html: `{{.TicketURL}}<object data="x"></object>`, wantMsg: "<object> is not allowed"},
		{name: "embed", html: `{{.TicketURL}}<embed src="https://x.example">`, wantMsg: "<embed> is not allowed"},
		{name: "form", html: `{{.TicketURL}}<form action="https://evil.example"><input name="password"></form>`, wantMsg: "<form> is not allowed"},
		{name: "input", html: `{{.TicketURL}}<input name="password">`, wantMsg: "<input> is not allowed"},
		{name: "button", html: `{{.TicketURL}}<button>go</button>`, wantMsg: "<button> is not allowed"},
		{name: "select", html: `{{.TicketURL}}<select><option>a</option></select>`, wantMsg: "<select> is not allowed"},
		{name: "textarea", html: `{{.TicketURL}}<textarea></textarea>`, wantMsg: "<textarea> is not allowed"},
		{name: "base", html: `{{.TicketURL}}<base href="https://evil.example/">`, wantMsg: "<base> is not allowed"},
		{name: "link", html: `{{.TicketURL}}<link rel="stylesheet" href="https://evil.example/a.css">`, wantMsg: "<link> is not allowed"},
		{name: "meta", html: `{{.TicketURL}}<meta http-equiv="refresh" content="0;url=https://evil.example">`, wantMsg: "<meta> is not allowed"},
		{name: "svg", html: `{{.TicketURL}}<svg onload="x"></svg>`, wantMsg: "<svg> is not allowed"},
		{name: "math", html: `{{.TicketURL}}<math></math>`, wantMsg: "<math> is not allowed"},
		{name: "upper case tag", html: `{{.TicketURL}}<SCRIPT>x</SCRIPT>`, wantMsg: "<script> is not allowed"},
		{name: "an end tag alone", html: `{{.TicketURL}}</script>`, wantMsg: "</script> is not allowed"},
		{name: "an element email does not use", html: `{{.TicketURL}}<blink>x</blink>`, wantMsg: "<blink> is not allowed"},

		// ── refused attributes ──
		{name: "onclick", html: `<a href="{{.TicketURL}}" onclick="x()">open</a>`, wantLine: 1, wantCol: 1, wantMsg: `attribute "onclick" is not allowed`},
		{name: "onerror upper case", html: `{{.TicketURL}}<img src="https://a.example/x.png" ONERROR="x()">`, wantMsg: `attribute "onerror" is not allowed`},
		{name: "onload on body", html: `<body onload="x()">{{.TicketURL}}`, wantMsg: `attribute "onload" is not allowed`},
		{name: "srcdoc", html: `{{.TicketURL}}<p srcdoc="x">y</p>`, wantMsg: `attribute "srcdoc" is not allowed`},
		{name: "formaction", html: `{{.TicketURL}}<a formaction="x">y</a>`, wantMsg: `attribute "formaction" is not allowed`},

		// ── refused URLs ──
		{name: "javascript href", html: `{{.TicketURL}}<a href="javascript:alert(1)">x</a>`, wantMsg: "href must start with https:, http: or mailto:"},
		{name: "javascript href mixed case", html: `{{.TicketURL}}<a href="JaVaScRiPt:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "javascript href with a tab inside", html: "{{.TicketURL}}<a href=\"java\tscript:alert(1)\">x</a>", wantMsg: "href must start with"},
		{name: "javascript href with leading space", html: `{{.TicketURL}}<a href="  javascript:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "javascript href by entity", html: `{{.TicketURL}}<a href="&#106;avascript:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "data href", html: `{{.TicketURL}}<a href="data:text/html,<b>x</b>">x</a>`, wantMsg: "href must start with"},
		{name: "relative href", html: `{{.TicketURL}}<a href="/tickets/1">x</a>`, wantMsg: "href must start with"},
		{name: "empty href", html: `{{.TicketURL}}<a href="">x</a>`, wantMsg: "href must start with"},
		{name: "http src", html: `{{.TicketURL}}<img src="http://a.example/x.png">`, wantMsg: "src must start with https:"},
		{name: "data src", html: `{{.TicketURL}}<img src="data:image/png;base64,AAAA">`, wantMsg: "src must start with https:"},
		{name: "javascript src", html: `{{.TicketURL}}<img src="javascript:alert(1)">`, wantMsg: "src must start with https:"},
		{name: "an action as src", html: `<img src="{{.TicketURL}}">`, wantMsg: "src must start with https:"},
		{name: "background attribute is a src", html: `{{.TicketURL}}<td background="http://a.example/x.png">x</td>`, wantMsg: "background must start with https:"},

		// ── templates only in text or as the whole href ──
		{name: "action in an attribute value", html: `{{.TicketURL}}<p title="{{.TrackingNumber}}">x</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action after a url prefix", html: `<a href="https://x.example/{{.TrackingNumber}}">{{.TicketURL}}</a>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in an attribute name", html: `{{.TicketURL}}<p {{.TrackingNumber}}="x">y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a tag name", html: `{{.TicketURL}}<{{.TrackingNumber}}>y`, wantMsg: "only in the text or as the whole href"},
		{name: "action as a bare attribute", html: `{{.TicketURL}}<p {{.TrackingNumber}}>y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a style attribute", html: `{{.TicketURL}}<p style="color:{{.TrackingNumber}}">y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a style block", html: `<style>p{content:"{{.TrackingNumber}}"}</style>{{.TicketURL}}`, wantMsg: "not allowed inside <style>"},
		{name: "if in a style block", html: `<style>{{if .TrackingNumber}}p{}{{end}}</style>{{.TicketURL}}`, wantMsg: "not allowed inside <style>"},
		{name: "action in an html comment", html: `<!-- {{.TicketURL}} -->hello`, wantMsg: "HTML comments are not allowed"},
		{name: "if inside a tag", html: `{{.TicketURL}}<a {{if .TrackingNumber}}class="x"{{end}}>y</a>`, wantMsg: "only in the text or as the whole href"},
		{name: "if inside an href", html: `<a href="{{if .TrackingNumber}}{{.TicketURL}}{{end}}">y</a>{{.TicketURL}}`, wantMsg: "only in the text or as the whole href"},
		// The parser drops a template comment, so the text either side is one
		// piece of markup and is read as what it renders: a script.
		{name: "template comment inside a tag name", html: `{{.TicketURL}}<scr{{/* x */}}ipt>alert(1)</script>`, wantMsg: "<script> is not allowed"},
		{name: "tag split across if branches", html: `{{.TicketURL}}{{if .TrackingNumber}}<{{else}}x{{end}}script>alert(1)</script>`, wantMsg: "write &lt;"},
		{name: "tag opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<scr{{else}}x{{end}}ipt>alert(1)</script>`, wantMsg: "tag is not finished"},
		{name: "comment opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<!--{{end}}x`, wantMsg: "comment is not finished"},
		{name: "style opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<style>{{end}}x`, wantMsg: "<style> is not closed"},

		// ── markup that would be read differently from how it is written ──
		{name: "a bare less-than", html: `{{.TicketURL}} a < b`, wantMsg: "write &lt;"},
		{name: "an unfinished tag", html: `{{.TicketURL}}<a href="https://x.example`, wantMsg: "tag is not finished"},
		{name: "an unfinished comment", html: `{{.TicketURL}}<!-- x`, wantMsg: "comment is not finished"},

		// ── HTML comments are refused, not stripped ──
		{name: "a comment", html: "{{.TicketURL}}\n<!-- note to self -->", wantLine: 2, wantCol: 1, wantMsg: "HTML comments are not allowed"},
		{name: "an outlook conditional comment", html: `{{.TicketURL}}<!--[if mso]><table><tr><td><![endif]-->`, wantMsg: "{{/* */}}"},
		{name: "the shortest comment", html: `{{.TicketURL}}<!-->`, wantMsg: "HTML comments are not allowed"},
		{name: "a downlevel-revealed comment", html: `{{.TicketURL}}<!--[if !mso]><!-->x<!--<![endif]-->`, wantMsg: "HTML comments are not allowed"},

		// ── CSS: style attributes and <style> blocks stay, but the ways CSS reaches out or runs are refused ──
		{name: "a plain style attribute", html: `<p style="color:red;background:url(https://cdn.example.com/a.png)">{{.TicketURL}}</p>`},
		{name: "a quoted https url in css", html: `<p style="background:url('https://cdn.example.com/a.png')">{{.TicketURL}}</p>`},
		{name: "css backslash in a style attribute", html: `{{.TicketURL}}<p style="col\6frs:x">y</p>`, wantMsg: "backslash"},
		{name: "css backslash in a style block", html: `<style>p { color: \72 ed }</style>{{.TicketURL}}`, wantMsg: "backslash"},
		{name: "css expression", html: `{{.TicketURL}}<p style="width:expression(alert(1))">y</p>`, wantMsg: "expression"},
		{name: "css expression upper case", html: `{{.TicketURL}}<p style="width:EXPRESSION(alert(1))">y</p>`, wantMsg: "expression"},
		{name: "css behavior", html: `{{.TicketURL}}<p style="behavior:url(https://x.example/a.htc)">y</p>`, wantMsg: "behavior"},
		{name: "css moz-binding", html: `{{.TicketURL}}<p style="-moz-binding:url(https://x.example/a.xml)">y</p>`, wantMsg: "-moz-binding"},
		{name: "css import", html: `<style>@import "https://evil.example/a.css";</style>{{.TicketURL}}`, wantMsg: "@import"},
		{name: "css import upper case", html: `<style>@IMPORT url(https://evil.example/a.css);</style>{{.TicketURL}}`, wantMsg: "@import"},
		{name: "css javascript url", html: `{{.TicketURL}}<p style="background:url(javascript:alert(1))">y</p>`, wantMsg: "javascript:"},
		{name: "css vbscript", html: `{{.TicketURL}}<p style="background:vbscript:x">y</p>`, wantMsg: "vbscript:"},
		{name: "css data url", html: `{{.TicketURL}}<p style="background:url(data:image/png;base64,AAAA)">y</p>`, wantMsg: "data:"},
		{name: "css http url", html: `{{.TicketURL}}<p style="background:url(http://a.example/x.png)">y</p>`, wantMsg: "url( must be followed by https:"},
		{name: "css relative url", html: `<style>p{background:url(/x.png)}</style>{{.TicketURL}}`, wantMsg: "url( must be followed by https:"},
		{name: "css url with spaces", html: `{{.TicketURL}}<p style="background:url(  http://a.example/x.png)">y</p>`, wantMsg: "url( must be followed by https:"},
		{name: "css url upper case", html: `{{.TicketURL}}<p style="background:URL(//a.example/x.png)">y</p>`, wantMsg: "url( must be followed by https:"},
		{name: "css image-set", html: `{{.TicketURL}}<p style="background:image-set('http://a.example/x.png' 1x)">y</p>`, wantMsg: "image-set("},

		// ── attributes are an allow-list ──
		{name: "an attribute email does not use", html: `{{.TicketURL}}<p contenteditable="true">y</p>`, wantMsg: `attribute "contenteditable" is not allowed`},
		{name: "a data attribute", html: `{{.TicketURL}}<p data-x="y">y</p>`, wantMsg: `attribute "data-x" is not allowed`},
		{name: "an aria attribute is fine", html: `{{.TicketURL}}<p aria-label="y" role="presentation">y</p>`},
		{name: "a vertical tab before a handler", html: "<a href=\"https://ok.example\" \vonclick=x>{{.TicketURL}}</a>", wantMsg: "is not allowed"},
		{name: "a handler after a slash", html: `{{.TicketURL}}<a href="https://ok.example"/onclick="x">y</a>`, wantMsg: `"onclick"`},
		{name: "an odd attribute name", html: `{{.TicketURL}}<p a$b="y">y</p>`, wantMsg: "letters, digits and hyphens"},
		{name: "xlink href", html: `{{.TicketURL}}<a xlink:href="javascript:alert(1)">y</a>`, wantMsg: `"xlink:href"`},
		{name: "srcset with a http candidate", html: `{{.TicketURL}}<img src="https://a.example/x.png" srcset="https://a.example/x.png 1x, http://a.example/y.png 2x">`, wantMsg: "srcset must start with https:"},
		{name: "srcset all https", html: `{{.TicketURL}}<img src="https://a.example/x.png" srcset="https://a.example/x.png 1x, https://a.example/y.png 2x">`},
		{name: "lowsrc", html: `{{.TicketURL}}<img src="https://a.example/x.png" lowsrc="http://a.example/x.png">`, wantMsg: "lowsrc must start with https:"},
		{name: "dynsrc", html: `{{.TicketURL}}<img dynsrc="javascript:x">`, wantMsg: "dynsrc must start with https:"},
		{name: "ping", html: `{{.TicketURL}}<a href="https://a.example" ping="http://t.example/p">y</a>`, wantMsg: "ping must start with https:"},
		{name: "cite", html: `{{.TicketURL}}<blockquote cite="http://a.example">y</blockquote>`, wantMsg: "cite must start with https:"},
		{name: "longdesc", html: `{{.TicketURL}}<img src="https://a.example/x.png" longdesc="/relative">`, wantMsg: "longdesc must start with https:"},
		{name: "a first href that is a javascript url", html: `{{.TicketURL}}<a href="javascript:alert(1)" href="https://ok.example">y</a>`, wantMsg: "href must start with"},
		{name: "a second handler after a repeated attribute", html: `{{.TicketURL}}<a title="a" title="b" onclick="x">y</a>`, wantMsg: `"onclick"`},

		// ── a whole-value action in a link must be a URL ──
		{name: "a tracking number as an href", html: `{{.TicketURL}}<a href="{{.TrackingNumber}}">y</a>`, wantMsg: "must be a link variable"},
		{name: "a tracking number in the only link", html: `<a href="{{.TrackingNumber}}">{{.TicketURL}}</a>`, wantMsg: "must be a link variable"},

		// ── differentials: where the validator's tokenizer and a browser could disagree ──
		// <style/> is NOT self-closing in HTML: a browser (and the tokenizer) read
		// what follows as CSS up to </style>. The check on CSS must follow.
		{name: "a self-closing style still starts CSS", html: `{{.TicketURL}}<style/>p{background:url(http://a.example/x)}</style>`, wantMsg: "url( must be followed by https:"},
		{name: "a self-closing style still refuses a template inside", html: `<style/>{{.TrackingNumber}}</style>{{.TicketURL}}`, wantMsg: "not allowed inside <style>"},
		{name: "a self-closing style left open", html: `{{.TicketURL}}<style/>p{}`, wantMsg: "<style> is not closed"},
		{name: "a self-closing style with CSS that runs", html: `{{.TicketURL}}<style />p{width:expression(alert(1))}</style>`, wantMsg: "expression"},
		{name: "style start tag with odd casing", html: `{{.TicketURL}}<STYLE>p{background:url(http://a.example/x)}</sTyLe >`, wantMsg: "url( must be followed by https:"},
		{name: "style end tag with a tab", html: "{{.TicketURL}}<style>p{}</style\t><script>x</script>", wantMsg: "<script> is not allowed"},
		{name: "style end tag with a slash", html: `{{.TicketURL}}<style>p{}</style/><script>x</script>`, wantMsg: "<script> is not allowed"},
		{name: "a comment close with a bang", html: `{{.TicketURL}}<!-- a --!><script>x</script> -->`, wantMsg: "HTML comments are not allowed"},
		{name: "a comment that is just dashes", html: `{{.TicketURL}}<!--->`, wantMsg: "HTML comments are not allowed"},
		{name: "a processing instruction", html: `{{.TicketURL}}<?php <script>x</script> ?>`, wantMsg: "HTML comments are not allowed"},
		{name: "cdata", html: `{{.TicketURL}}<![CDATA[ <script>x</script> ]]>`, wantMsg: "HTML comments are not allowed"},
		{name: "a bogus end tag", html: `{{.TicketURL}}</ script><script>x</script>`, wantMsg: "HTML comments are not allowed"},
		{name: "title", html: `{{.TicketURL}}<title></title>`, wantMsg: "<title> is not allowed"},
		{name: "textarea with an odd end", html: `{{.TicketURL}}<TEXTAREA></TextArea ><script>x</script>`, wantMsg: "<textarea> is not allowed"},
		{name: "a control character in an attribute name", html: "{{.TicketURL}}<p a\x01b=c>y</p>", wantMsg: "letters, digits and hyphens"},
		{name: "a form feed before a handler", html: "{{.TicketURL}}<p\x0conclick=x>y</p>", wantMsg: `"onclick"`},
		{name: "a control character after the tag name", html: "{{.TicketURL}}<a\x0b href=\"javascript:x\">y</a>", wantMsg: "is not allowed"},
		{name: "an entity for a NUL is only text", html: `{{.TicketURL}}<p title="&#0;">y</p>`},
		{name: "html entities for a javascript url", html: `{{.TicketURL}}<a href="java&Tab;script&colon;alert(1)">y</a>`, wantMsg: "href must start with"},
		{name: "a numeric entity with no semicolon", html: `{{.TicketURL}}<a href="&#106avascript:alert(1)">y</a>`, wantMsg: "href must start with"},
		{name: "entities in a style attribute are decoded", html: `{{.TicketURL}}<p style="background:u&#114;l(http://a.example/x)">y</p>`, wantMsg: "url( must be followed by https:"},
		{name: "entities in a style block are not decoded, and are not CSS", html: `<style>p{content:"&#117;rl(http://a.example/x)"}</style>{{.TicketURL}}`},
		{name: "css comments are refused", html: `{{.TicketURL}}<p style="color:red /* x */">y</p>`, wantMsg: "comment"},
		{name: "css comment splitting a keyword", html: `{{.TicketURL}}<p style="width:exp/**/ression(alert(1))">y</p>`, wantMsg: "comment"},
		{name: "css comment hiding a url", html: `<style>p{content:"/*";background:url(http://a.example/x)/* "}</style>{{.TicketURL}}`, wantMsg: "comment"},
		{name: "css newline inside url", html: "{{.TicketURL}}<p style=\"background:url(\nhttp://a.example/x)\">y</p>", wantMsg: "url( must be followed by https:"},
		{name: "css newline inside url is fine for https", html: "{{.TicketURL}}<p style=\"background:url(\nhttps://a.example/x)\">y</p>"},
		{name: "css src()", html: `{{.TicketURL}}<p style="background:src(http://a.example/x)">y</p>`, wantMsg: "src("},
		{name: "css image()", html: `<style>p{background:image(http://a.example/x)}</style>{{.TicketURL}}`, wantMsg: "image("},
		{name: "css cross-fade()", html: `<style>p{background:cross-fade(url(https://a.example/x), url(https://a.example/y))}</style>{{.TicketURL}}`, wantMsg: "cross-fade("},
		{name: "css webkit image-set", html: `{{.TicketURL}}<p style="background:-webkit-image-set(url(http://a.example/x) 1x)">y</p>`, wantMsg: "image-set("},
		{name: "css ie filter loader", html: `{{.TicketURL}}<p style="filter:progid:DXImageTransform.Microsoft.AlphaImageLoader(src='http://a.example/x')">y</p>`, wantMsg: "progid:"},
		{name: "css behavior with a comment", html: `{{.TicketURL}}<p style="beha/**/vior:x">y</p>`, wantMsg: "comment"},

		// ── the required link ──
		{name: "no link", html: `<p>Hello</p>`, wantLine: 1, wantCol: 1, wantMsg: "must include {{.TicketURL}}"},
		{name: "link only inside an if", html: `{{if .TrackingNumber}}<p>{{.TicketURL}}</p>{{end}}`, wantMsg: "must include {{.TicketURL}} outside any {{if}}"},
		{name: "the wrong variable", html: `<p>{{.TrackingNumber}}</p>`, wantMsg: "must include {{.TicketURL}}"},

		// ── the template language itself is the same as the text part ──
		{name: "range", html: "{{.TicketURL}}\n{{range .TrackingNumber}}x{{end}}", wantLine: 2, wantCol: 1, wantMsg: "range"},
		{name: "printf", html: "{{.TicketURL}}\n{{printf \"%0999999999d\" 1}}", wantLine: 2, wantCol: 3, wantMsg: "printf"},
		{name: "a pipe", html: `<a href="{{.TicketURL | html}}">x</a>`, wantMsg: "pipe"},
		{name: "ticket content by path", html: `{{.Ticket.Subject}}{{.TicketURL}}`, wantMsg: "not allowed"},
		{name: "unknown variable", html: `{{.Subject}}{{.TicketURL}}`, wantMsg: "Subject"},
		{name: "define", html: `{{.TicketURL}}{{define "x"}}y{{end}}`, wantMsg: "define"},
		{name: "unclosed if", html: `{{.TicketURL}}{{if .TrackingNumber}}x`, wantMsg: "{{end}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateHTML(def, tc.html)
			if tc.wantMsg == "" {
				require.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs, "accepted: %q", tc.html)
			require.Equal(t, "html", errs[0].Field)
			require.Contains(t, errs[0].Message, tc.wantMsg)
			if tc.wantLine > 0 {
				require.Equal(t, tc.wantLine, errs[0].Line, "line: %+v", errs[0])
				require.Equal(t, tc.wantCol, errs[0].Col, "col: %+v", errs[0])
			}
		})
	}
}

func TestValidateHTML_ALinkVariableIsAcceptedAsAHrefInEveryMail(t *testing.T) {
	for name, v := range map[string]string{"ticket_created": "TicketURL", "email_verification": "VerifyURL", "password_reset": "ResetURL"} {
		def := mustDef(t, name)
		require.Empty(t, ValidateHTML(def, `<a href="{{.`+v+`}}">go</a>`), name)
	}
	// The shells have no link variable, so a whole-value action cannot be one.
	for _, name := range []string{NameHeader, NameFooter} {
		errs := ValidateHTML(mustDef(t, name), `<a href="{{.SiteName}}">x</a>`)
		require.NotEmpty(t, errs, name)
		require.Contains(t, errs[0].Message, "must be a link variable")
	}
}

// golang.org/x/net/html keeps the FIRST of a repeated attribute and drops the
// rest, which is what a browser does, so the validator only ever sees the one
// that counts. This pins that: if a later x/net keeps the last instead, the
// second case below starts being accepted with a javascript: link in it, and
// this fails. (A mail client that reads the LAST one would see the other link;
// the first is the one a browser and the tokenizer agree on.)
func TestValidateHTML_ARepeatedAttributeIsReadTheFirstWay(t *testing.T) {
	def := mustDef(t, "ticket_created")
	require.NotEmpty(t, ValidateHTML(def, `{{.TicketURL}}<a href="javascript:alert(1)" href="https://ok.example">y</a>`), "the first one is the one that counts")
	require.Empty(t, ValidateHTML(def, `{{.TicketURL}}<a href="https://ok.example" href="javascript:alert(1)">y</a>`), "the dropped one is not read")
	require.NotEmpty(t, ValidateHTML(def, `{{.TicketURL}}<a onclick="x()" onclick="y()">y</a>`))
}

func TestValidateHTML_RefusalNamesWhy(t *testing.T) {
	// The reason matters as much as the refusal: it is what the admin reads.
	def := mustDef(t, "ticket_created")
	errs := ValidateHTML(def, `{{.TicketURL}}<script>x</script>`)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "mail clients remove")
	errs = ValidateHTML(def, `{{.TicketURL}}<form></form>`)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "form")
}

func TestValidateHTML_Caps(t *testing.T) {
	def := mustDef(t, "ticket_created")

	atLimit := "{{.TicketURL}}" + strings.Repeat("a", MaxHTMLBytes-len("{{.TicketURL}}"))
	require.Empty(t, ValidateHTML(def, atLimit))

	errs := ValidateHTML(def, atLimit+"a")
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "limit is 65536")

	for name, bad := range map[string]string{
		"invalid UTF-8": "{{.TicketURL}}\xff",
		"NUL":           "{{.TicketURL}}\x00",
	} {
		t.Run(name, func(t *testing.T) {
			errs := ValidateHTML(def, bad)
			require.NotEmpty(t, errs)
			require.Equal(t, "html", errs[0].Field)
		})
	}
}

func TestValidateHTML_RenderedSizeIsCapped(t *testing.T) {
	// Source under 64 KiB can still render past what a mail client will show
	// (Gmail clips near 102 KB) when a long value is repeated, so the render
	// itself is capped.
	def := mustDef(t, "ticket_created")
	src := strings.Repeat("{{.TicketURL}}", MaxHTMLBytes/len("{{.TicketURL}}"))
	require.LessOrEqual(t, len(src), MaxHTMLBytes)
	vars := map[string]string{"TrackingNumber": "GHD-1", "TicketURL": "https://" + strings.Repeat("h", 40) + ".example"}
	_, err := RenderHTML(def, src, vars)
	require.Error(t, err)
	require.Contains(t, err.Error(), "100 KiB")
}

func TestValidateHTML_ShellsNeedNoLinkAndMayUseSiteName(t *testing.T) {
	for _, name := range []string{NameHeader, NameFooter} {
		def := mustDef(t, name)
		require.Empty(t, ValidateHTML(def, `<table><tr><td>{{.SiteName}}</td></tr>`), name) // unbalanced on purpose: the footer closes it
		require.Empty(t, ValidateHTML(def, ""), name)
		errs := ValidateHTML(def, `<p>{{.TicketURL}}</p>`)
		require.NotEmpty(t, errs, name)
		require.Contains(t, errs[0].Message, "TicketURL")
		errs = ValidateHTML(def, `<script>x</script>`)
		require.NotEmpty(t, errs, name)
	}
}

func TestValidateHTML_AccountMailsRequireTheirOwnLink(t *testing.T) {
	for name, v := range map[string]string{"email_verification": "VerifyURL", "password_reset": "ResetURL"} {
		def := mustDef(t, name)
		require.Empty(t, ValidateHTML(def, fmt.Sprintf(`<a href="{{.%s}}">go</a>`, v)), name)
		errs := ValidateHTML(def, `<p>no link</p>`)
		require.NotEmpty(t, errs, name)
		require.Contains(t, errs[0].Message, "{{."+v+"}}")
	}
}

func TestValidateHTML_ReportsTheEarliestProblem(t *testing.T) {
	// Lists are checked on their own (so a branch cannot finish another's tag);
	// the answer is still the first problem in the text, not the first list's.
	def := mustDef(t, "ticket_created")
	html := "{{.TicketURL}}{{if .TrackingNumber}}\n<script>x</script>{{end}}<iframe></iframe>"
	errs := ValidateHTML(def, html)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "<script>")
	require.Equal(t, 2, errs[0].Line)
}

// ── rendering ────────────────────────────────────────────────────────────────

func TestRenderHTML_EscapesValuesInHTMLContext(t *testing.T) {
	// The values are server-built today, but the guarantee must not rest on
	// that: whatever reaches a variable is text, never markup.
	def := mustDef(t, "ticket_created")
	vars := map[string]string{
		"TrackingNumber": `<img src=x onerror=alert(1)>&"'`,
		"TicketURL":      `https://help.example.com/t?a="><script>alert(1)</script>`,
	}
	out, err := RenderHTML(def, `<p>Ref {{.TrackingNumber}}</p><a href="{{.TicketURL}}">{{.TicketURL}}</a>`, vars)
	require.NoError(t, err)
	require.NotContains(t, out, "<img")
	require.NotContains(t, out, "<script>")
	require.Contains(t, out, "&lt;img src=x onerror=alert(1)&gt;")
	require.Contains(t, out, `<a href="https://help.example.com/t?a=%22%3e%3cscript%3ealert%281%29%3c/script%3e">`)
}

func TestRenderHTML_AHrefValueThatIsNotAWebAddressIsNeutralised(t *testing.T) {
	// html/template filters a javascript: URL in a link to #ZgotmplZ. Today no
	// variable can hold one; this pins that the renderer would still not emit it.
	def := mustDef(t, "ticket_created")
	out, err := RenderHTML(def, `<a href="{{.TicketURL}}">x</a>`, map[string]string{"TrackingNumber": "", "TicketURL": "javascript:alert(1)"})
	require.NoError(t, err)
	require.NotContains(t, out, "javascript:")
}

func TestRenderHTML_TheGuestFragmentSurvivesEscaping(t *testing.T) {
	// A guest link carries its token in the URL fragment (/g#token). The
	// escaper must leave it exactly as built or the link stops working.
	def := mustDef(t, "ticket_created")
	link := "https://help.example.com/g#AbC-123_xyz.token~value"
	out, err := RenderHTML(def, `<a href="{{.TicketURL}}">open</a> {{.TicketURL}}`, map[string]string{"TrackingNumber": "GHD-1", "TicketURL": link})
	require.NoError(t, err)
	require.Contains(t, out, `<a href="`+link+`">`)
	require.Contains(t, out, "open</a> "+link)
}

func TestRenderHTML_RefusesInvalidAndIsEmptyForNone(t *testing.T) {
	def := mustDef(t, "ticket_created")
	out, err := RenderHTML(def, "", map[string]string{})
	require.NoError(t, err)
	require.Empty(t, out)

	_, err = RenderHTML(def, `<script>x</script>{{.TicketURL}}`, map[string]string{"TicketURL": "https://x"})
	var invalid *InvalidTemplateError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "html", invalid.Errors[0].Field)
}

func TestRenderHTML_AMissingValueIsAnErrorNotBlankText(t *testing.T) {
	def := mustDef(t, "ticket_created")
	_, err := RenderHTML(def, `<p>{{.TrackingNumber}}</p>{{.TicketURL}}`, map[string]string{"TicketURL": "https://x"})
	require.Error(t, err)
}

// ── fuzz ─────────────────────────────────────────────────────────────────────

// treeProblem reads rendered HTML the way a browser does, with the HTML5 tree
// builder, not with the tokenizer the validator uses, and returns the first
// thing in it that the validator exists to keep out. The two disagreeing about
// what some bytes are (a comment that ends early, a raw-text element, a name
// with an odd character) shows up here as a script, a handler or a bad URL in
// the tree of something the validator accepted.
func treeProblem(out string) string {
	nodes, err := nethtml.ParseFragment(strings.NewReader(out), &nethtml.Node{Type: nethtml.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		return "unparseable: " + err.Error()
	}
	var walk func(n *nethtml.Node) string
	walk = func(n *nethtml.Node) string {
		if n.Type == nethtml.ElementNode {
			if n.Namespace != "" {
				return "foreign element " + n.Data
			}
			if _, bad := refusedElements[n.Data]; bad || !allowedElements[n.Data] {
				return "element " + n.Data
			}
			for _, a := range n.Attr {
				k := strings.ToLower(a.Key)
				switch {
				case strings.HasPrefix(k, "on"):
					return "handler " + a.Key
				case k == "href" && !urlHasScheme(a.Val, "https:", "http:", "mailto:"):
					return "href " + a.Val
				case httpsOnly[k] && !allURLsHTTPS(k, a.Val):
					return k + " " + a.Val
				case k == "style" && checkCSS(a.Val) != "":
					return "style " + a.Val
				case !allowedAttributes[k] && !strings.HasPrefix(k, "aria-"):
					return "attribute " + a.Key
				}
			}
			if n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if why := checkCSS(c.Data); why != "" {
						return "style block: " + why
					}
				}
			}
		}
		if n.Type == nethtml.CommentNode {
			return "comment"
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if p := walk(c); p != "" {
				return p
			}
		}
		return ""
	}
	for _, n := range nodes {
		if p := walk(n); p != "" {
			return p
		}
	}
	return ""
}

// differentialSeeds are inputs where a tokenizer and a browser could disagree.
var differentialSeeds = []string{
	"<style/>p{background:url(http://a/x)}</style>", "<style />p{width:expression(x)}</style>", "<style/>{{.TrackingNumber}}</style>",
	"<style>p{background:url(http://a/x)/* }</style>", "<p style=\"background:src(http://a/x)\">", "<p a\x01b=c>",
	"<!-->", "<!--->", "<!-- x --!> <script>alert(1)</script> -->", "<!-- a --> b", "<!--[if mso]><p><![endif]-->",
	"<title>&lt;/title&gt;<script>alert(1)</script></title>", "<textarea></textarea><script>x</script>",
	"<style>p{}</STYLE ><script>x</script>", "<style>p{}</style\t><script>x</script>", "<style>p{}</style/><script>x</script>",
	"<style>/* </style><script>alert(1)</script> */</style>", "<style>a{b:'</style><script>alert(1)</script>'}</style>",
	"<p a\x00=b>", "<p \x0bonclick=x>", "<p\x0conclick=x>", "<a href=\"https://ok\"onclick=x>y</a>", "<a href=\"https://ok\"/onclick=x>y</a>",
	"<a href=\"&#x6A;avascript:x\">y</a>", "<a href=\"java&Tab;script:x\">y</a>", "<a href=\"java&NewLine;script&colon;x\">y</a>",
	"<a href=\"&#106avascript:x\">y</a>", "<a href=\"&amp;#106;avascript:x\">y</a>", "<a href=\"\x01javascript:x\">y</a>", "<a href=\"\u00a0javascript:x\">y</a>",
	"<p style=\"background:url(&#104;ttp://a/x)\">", "<p style=\"background:u&#114;l(http://a/x)\">", "<p style=\"background:url(\nhttp://a/x)\">",
	"<p style=\"background:url(/**/http://a/x)\">", "<p style=\"background:ur/**/l(http://a/x)\">", "<p style=\"width:exp/**/ression(x)\">",
	"<p style=\"background:url('https://a/x');background:url(http://a/y)\">", "<style>p{background:url(http://a/x)}</style>",
	"<style>@im/**/port 'http://a/x';</style>", "<style>p{content:\"/*\";background:url(http://a/x)/* \"}</style>",
	"<style>p{background:src(http://a/x)}</style>", "<p style=\"filter:progid:DXImageTransform.Microsoft.AlphaImageLoader(src='http://a/x')\">",
	"<noscript><p title=\"</noscript><script>x</script>\"></noscript>", "<xmp><script>x</script></xmp>", "<plaintext><script>x</script>",
	"<table><tr><td><style>p{}</style><script>x</script>", "<select><script>x</script></select>", "<template><script>x</script></template>",
	"<svg><style><![CDATA[</style><script>x</script>]]></style></svg>", "<math><mtext><table><mglyph><style><!--</style><img src onerror=x>",
	"<![CDATA[ <script>x</script> ]]>", "<?php <script>x</script> ?>", "</ script><script>x</script>", "<!x <script>y</script> >",
	"<img src=\"https://a/x\" srcset=\"https://a/x 1x,http://a/y 2x\">", "<a ping=\"http://a\" href=\"https://ok\">y</a>",
	"<a href=https://ok.example/a`b onclick=x>y</a>", "<p a=`b onclick=x`>", "&lt;script&gt;", "&#60;script&#62;",
}

// FuzzValidateHTML asserts what the save path relies on: the validator never
// panics, and anything it accepts renders without error into bytes that, read
// the way a browser reads them (a different parser from the validator's), hold
// nothing the validator forbids. `go test` runs the seed corpus; `go test
// -fuzz=FuzzValidateHTML` explores.
func FuzzValidateHTML(f *testing.F) {
	for _, s := range []string{
		``, `{{.TicketURL}}`, `<a href="{{.TicketURL}}">x</a>`, `<p>{{.TrackingNumber}}</p>{{.TicketURL}}`,
		`<script>x</script>{{.TicketURL}}`, `{{if .TrackingNumber}}<{{else}}x{{end}}script>`, `<style>p{}</style>{{.TicketURL}}`,
		`<a href="javascript:x">{{.TicketURL}}`, `<!-- {{.TicketURL}} -->`, `<table><tr><td>{{.TicketURL}}`,
		"{{.TicketURL}}\xff", `{{range .X}}`, `{{.TicketURL}}<img src="https://a/b.png" onerror=x>`, `<a href=`, `<`, `{{`, `{{.TicketURL`,
		`{{- .TicketURL -}}<br/>`, `&#106;avascript:`, `<p {{.TicketURL}}>`,
	} {
		f.Add(s)
	}
	for _, s := range differentialSeeds {
		f.Add("{{.TicketURL}}" + s)
		f.Add(s + "{{.TicketURL}}")
	}
	def, ok := LookupTemplate("ticket_created")
	if !ok {
		f.Fatal("no ticket_created template")
	}
	vars := SampleVars(def, "https://help.example.com", "GHD")
	f.Fuzz(func(t *testing.T, src string) {
		if len(ValidateHTML(def, src)) > 0 {
			return
		}
		out, err := RenderHTML(def, src, vars)
		if err != nil {
			t.Fatalf("accepted but did not render: %v\nsource: %q", err, src)
		}
		if p := treeProblem(out); p != "" {
			t.Fatalf("accepted source rendered markup a browser would read as %s\nsource: %q\noutput: %q", p, src, out)
		}
	})
}

// The oracle itself: it must see what it is there to see.
func TestTreeProblem_SeesWhatABrowserWouldSee(t *testing.T) {
	for _, bad := range []string{
		`<script>x</script>`, `<p onclick="x">y</p>`, `<a href="javascript:x">y</a>`, `<!-- c -->`,
		`<title>t</title>`, `<p style="width:expression(x)">y</p>`, `<img src="http://a/x">`,
		`<!-- a --!><script>x</script>`, `<svg></svg>`, `<p data-x="y">`,
	} {
		require.NotEmpty(t, treeProblem(bad), bad)
	}
	require.Empty(t, treeProblem(`<table><tr><td style="color:red"><a href="https://a.example">x</a> &amp; y</td></tr></table>`))
}

// The bytes that are sent are checked, not only the source they came from.
func TestCheckRenderedHTML_IsTheSameCheckOnTheFinalBytes(t *testing.T) {
	require.Nil(t, checkRenderedHTML(`<table><tr><td><p>Hi &amp; bye <a href="https://a.example/g#tok">x</a></p></td></tr></table>`))
	require.Nil(t, checkRenderedHTML("Site  name"), "private-use characters in a value are text, not template markers")
	for _, bad := range []string{`<script>x</script>`, `<p onclick=x>`, `<a href="javascript:x">y</a>`, `<style/>p{background:url(http://a/x)}</style>`, `<!-- c -->`, `a < b`} {
		require.NotNil(t, checkRenderedHTML(bad), bad)
	}
}

func TestSend_RefusesFinalHTMLThatFailsTheCheck(t *testing.T) {
	// joinHTML is what compose and the preview both use for the final bytes.
	def := mustDef(t, "ticket_created")
	_, err := joinHTML(def, "<p>", "<script>x</script>", "</p>")
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), "<script> is not allowed")
	out, err := joinHTML(def, "<p>", "x", "</p>")
	require.NoError(t, err)
	require.Equal(t, "<p>x</p>", string(out))
}
