package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
	"unicode/utf8"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Editable email templates (#158).
//
// The one constraint, which is not negotiable: a template gets NO ticket
// content. The variables are a tracking number and server-built links, and
// that is deliberate. Mail leaves this server from the operator's domain, and
// whoever can file a ticket chooses its subject and body, so a template that
// could print them would hand back what v1.2.0 removed (GHSA-p2wx-wvfr-7h3p).
// Making the text editable must not widen what the text can see.
//
// A template is text/template, but only a sliver of it is accepted (see
// walkTemplate): the words, {{.Name}} for a variable this template is given,
// and {{if .Name}}…{{else}}…{{end}}. A FuncMap cannot do that job. printf,
// call, index, len, html and js are built in and cannot be removed, and
// {{range 1000000000}} is legal in Go 1.22 and later, so the parse tree is
// walked and anything outside the list is refused, with the line and column.

// Name constants for the two reserved rows that wrap every mail. They sit in
// the same table as the 17 content templates so the HTML variants (PR2) can be
// added beside them without a new table.
const (
	NameHeader = "_header"
	NameFooter = "_footer"
)

// Caps. With no loops, recursion or functions the output is bounded by the
// template's size times the values' size; MaxRenderedBytes is a backstop.
const (
	MaxBodyBytes     = 8 * 1024
	MaxSubjectBytes  = 200
	MaxRenderedBytes = 64 * 1024
)

// Template groups, in the order the editor lists them.
const (
	GroupRequester = "requester"
	GroupStaff     = "staff"
	GroupAccount   = "account"
	GroupShared    = "shared"
)

// Variable is one thing a template may print.
type Variable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TemplateDef is one editable template: what it is called, what it may use and
// what it ships with.
type TemplateDef struct {
	Name  string // registry key; also the row name in email_template_overrides
	Label string
	Group string
	// Vars is the whole of what the template can see.
	Vars []Variable
	// Required is a variable that must be printed at the top level of the body,
	// not under an if: the link the mail exists to deliver. Empty for a shell.
	Required string
	// Shell marks the shared header and footer: body only, no subject, no
	// required link, default empty.
	Shell          bool
	DefaultSubject string
	DefaultBody    string
}

// VarNames lists the variable names.
func (d TemplateDef) VarNames() []string {
	out := make([]string, len(d.Vars))
	for i, v := range d.Vars {
		out[i] = v.Name
	}
	return out
}

func (d TemplateDef) allows(name string) bool {
	for _, v := range d.Vars {
		if v.Name == name {
			return true
		}
	}
	return false
}

var (
	varTracking = Variable{"TrackingNumber", "The ticket's tracking number, such as GHD-2026-000042. It can be empty."}
	varTicket   = Variable{"TicketURL", "The link to the ticket. For a guest it is their own link, and it replaces any earlier one."}
	varVerify   = Variable{"VerifyURL", "The link that verifies the address and activates the account."}
	varReset    = Variable{"ResetURL", "The link that lets the person choose a new password."}
	varSite     = Variable{"SiteName", "The help desk's name from Settings."}
)

// subjectRef is the reference every ticket subject ends on: the tracking
// number, or "your ticket" when there is none. A missing number costs the
// subject its reference, not the customer the mail.
const subjectRef = "{{if .TrackingNumber}}[{{.TrackingNumber}}]{{else}}your ticket{{end}}"

// contentDefs is the registry of the 17 content templates, in the order the
// editor lists them. The default body is the embedded file of the same name.
// Subjects are templates, not Go strings, so that the text shown as "the
// default" is the text the mail actually uses.
var contentDefs = []struct {
	name, label, group, subject string
	vars                        []Variable
	required                    string
}{
	{"ticket_created", "Ticket received", GroupRequester, "We have received " + subjectRef, nil, ""},
	{"ticket_replied", "New reply on a ticket", GroupRequester, "There is a new reply on " + subjectRef, nil, ""},
	{"ticket_status_changed", "Ticket update (guest)", GroupRequester, "There is an update on " + subjectRef, nil, ""},
	{"ticket_resolved", "Ticket resolved (guest)", GroupRequester, "We have resolved " + subjectRef, nil, ""},
	{"ticket_reopened", "Ticket reopened (guest)", GroupRequester, "We have reopened " + subjectRef, nil, ""},
	{"guest_link", "New guest link", GroupRequester, "Your link to " + subjectRef, nil, ""},

	{"staff_assigned", "Assigned to you", GroupStaff, "A ticket is assigned to you: " + subjectRef, nil, ""},
	{"staff_assigned_group", "Assigned to your group", GroupStaff, "A ticket is assigned to your group: " + subjectRef, nil, ""},
	{"staff_assigned_follow", "Assigned, a ticket you follow", GroupStaff, "A ticket you follow is assigned: " + subjectRef, nil, ""},
	{"staff_requester_replied", "Requester replied", GroupStaff, "The requester replied on " + subjectRef, nil, ""},
	{"staff_requester_replied_group", "Requester replied, your group", GroupStaff, "The requester replied on a ticket assigned to your group: " + subjectRef, nil, ""},
	{"staff_requester_replied_follow", "Requester replied, a ticket you follow", GroupStaff, "The requester replied on a ticket you follow: " + subjectRef, nil, ""},
	{"staff_reopened", "Reopened, assigned to you", GroupStaff, "A ticket assigned to you was reopened: " + subjectRef, nil, ""},
	{"staff_reopened_group", "Reopened, your group", GroupStaff, "A ticket assigned to your group was reopened: " + subjectRef, nil, ""},
	{"staff_reopened_follow", "Reopened, a ticket you follow", GroupStaff, "A ticket you follow was reopened: " + subjectRef, nil, ""},

	{"email_verification", "Verify your email address", GroupAccount, "Verify your email address", []Variable{varVerify}, "VerifyURL"},
	{"password_reset", "Reset your password", GroupAccount, "Reset your password", []Variable{varReset}, "ResetURL"},
}

// Templates returns every editable template: the 17 content templates, then
// the shared header and footer.
func Templates() []TemplateDef {
	out := make([]TemplateDef, 0, len(contentDefs)+2)
	for _, c := range contentDefs {
		body, err := templateFS.ReadFile("templates/" + c.name + ".tmpl")
		if err != nil {
			// The registry and the embedded files are checked against each
			// other by a test; a gap is a build error, not a runtime one.
			panic(fmt.Sprintf("notify: no embedded template for %q: %v", c.name, err))
		}
		vars, required := c.vars, c.required
		if vars == nil {
			vars, required = []Variable{varTracking, varTicket}, "TicketURL"
		}
		out = append(out, TemplateDef{
			Name: c.name, Label: c.label, Group: c.group, Vars: vars, Required: required,
			DefaultSubject: c.subject, DefaultBody: string(body),
		})
	}
	// The header and footer see only what is true of the whole installation.
	// Not a ticket field, and nothing that varies per mail.
	out = append(out,
		TemplateDef{Name: NameHeader, Label: "Header", Group: GroupShared, Vars: []Variable{varSite}, Shell: true},
		TemplateDef{Name: NameFooter, Label: "Footer", Group: GroupShared, Vars: []Variable{varSite}, Shell: true},
	)
	return out
}

// LookupTemplate finds one template by its registry name.
func LookupTemplate(name string) (TemplateDef, bool) {
	for _, d := range Templates() {
		if d.Name == name {
			return d, true
		}
	}
	return TemplateDef{}, false
}

// SampleVars is the data a preview or a test send renders with. The links are
// built the way the real ones are but carry no live credential: a sample token
// answers token_invalid, so a test mail can never be a way in.
func SampleVars(def TemplateDef, baseURL, trackingPrefix string) map[string]string {
	base := strings.TrimRight(baseURL, "/")
	out := map[string]string{}
	for _, v := range def.Vars {
		switch v.Name {
		case "TrackingNumber":
			out[v.Name] = string(ticket.GenerateTrackingNumber(trackingPrefix, time.Now().Year(), 42))
		case "TicketURL":
			out[v.Name] = base + "/tickets/00000000-0000-0000-0000-000000000000"
		case "VerifyURL":
			out[v.Name] = base + "/verify-email?token=sample-not-a-real-link"
		case "ResetURL":
			out[v.Name] = base + "/reset-password?token=sample-not-a-real-link"
		case "SiteName":
			out[v.Name] = "Go Help Desk"
		}
	}
	return out
}

// TemplateError is one reason a draft is refused. Line and Col are 1-based
// and count characters; both are 0 when the problem has no place in the text
// (a length, say).
type TemplateError struct {
	Field   string `json:"field"` // "subject" or "body"
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
}

func (e TemplateError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s, line %d column %d: %s", e.Field, e.Line, e.Col, e.Message)
	}
	return e.Field + ": " + e.Message
}

// InvalidTemplateError is what RenderTemplate returns for text that fails
// ValidateTemplate.
type InvalidTemplateError struct{ Errors []TemplateError }

func (e *InvalidTemplateError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, te := range e.Errors {
		parts[i] = te.Error()
	}
	return strings.Join(parts, "; ")
}

// ValidateTemplate checks a draft the same way at save, in the editor's live
// check, and again at send, so a template that was valid when saved and is not
// now (an upgrade removed a variable) is found rather than sent. It returns at
// most one error per field, in the order the text is read.
func ValidateTemplate(def TemplateDef, subject, body string) []TemplateError {
	var errs []TemplateError
	if def.Shell {
		if subject != "" {
			errs = append(errs, TemplateError{Field: "subject", Message: "the " + strings.ToLower(def.Label) + " has no subject"})
		}
	} else if te := checkText(def, "subject", subject, MaxSubjectBytes, false); te != nil {
		errs = append(errs, *te)
	}
	if te := checkText(def, "body", body, MaxBodyBytes, !def.Shell); te != nil {
		errs = append(errs, *te)
	}
	return errs
}

// checkText runs the checks for one field, in order, and stops at the first.
func checkText(def TemplateDef, field, text string, maxBytes int, wantLink bool) *TemplateError {
	fail := func(line, col int, format string, a ...any) *TemplateError {
		return &TemplateError{Field: field, Line: line, Col: col, Message: fmt.Sprintf(format, a...)}
	}

	// 1. Bytes.
	if !utf8.ValidString(text) {
		return fail(0, 0, "must be valid UTF-8")
	}
	if strings.ContainsRune(text, 0) {
		return fail(0, 0, "must not contain a NUL byte")
	}
	// 2. Size, and a subject is one line: a line break in it is a header break.
	if len(text) > maxBytes {
		return fail(0, 0, "is %d bytes; the limit is %d", len(text), maxBytes)
	}
	if field == "subject" {
		if strings.ContainsAny(text, "\r\n") {
			return fail(0, 0, "must be one line")
		}
		if strings.TrimSpace(text) == "" {
			return fail(0, 0, "cannot be empty")
		}
	}
	if text == "" && def.Shell {
		return nil // an empty header or footer is the default
	}

	// 3 and 4. It parses, and only the allowed nodes are in it.
	_, w, te := parseAndWalk(def, field, text)
	if te != nil {
		return te
	}
	// 5. The link the mail exists to deliver is always there.
	if wantLink && !w.foundRequired {
		return fail(1, 1, "must include {{.%s}} outside any {{if}}; without it the person cannot reach the link", def.Required)
	}

	// 6. It runs against sample data.
	if _, err := execute(def, text, SampleVars(def, "https://help.example.com", "GHD")); err != nil {
		return fail(0, 0, "%s", err.Error())
	}
	return nil
}

// parseAndWalk is steps 3 and 4 of a check, shared by the text and HTML parts:
// the text parses as exactly one template, and the tree holds only the allowed
// nodes. The walker it returns knows whether the required link was found.
func parseAndWalk(def TemplateDef, field, text string) (*parse.Tree, *walker, *TemplateError) {
	// SkipFuncCheck so that a function is refused by the walk, with its
	// position, rather than by the parser without one.
	tree := parse.New(def.Name)
	tree.Mode = parse.SkipFuncCheck
	trees := map[string]*parse.Tree{}
	if _, err := tree.Parse(text, "", "", trees); err != nil {
		line, col, msg := locateParseError(text, err)
		return nil, nil, &TemplateError{Field: field, Line: line, Col: col, Message: msg}
	}
	// Exactly one template: {{define}} and {{block}} would add more.
	if len(trees) != 1 || trees[def.Name] == nil {
		line, col := findKeyword(text, "define", "block")
		return nil, nil, &TemplateError{Field: field, Line: line, Col: col, Message: "{{define}} and {{block}} are not allowed"}
	}
	w := &walker{def: def, text: text, field: field}
	w.list(trees[def.Name].Root, true)
	if w.err != nil {
		return nil, nil, w.err
	}
	return trees[def.Name], w, nil
}

type walker struct {
	def           TemplateDef
	text, field   string
	err           *TemplateError
	foundRequired bool
}

func (w *walker) refuse(pos parse.Pos, format string, a ...any) {
	if w.err != nil {
		return
	}
	line, col := lineCol(w.text, int(pos))
	w.err = &TemplateError{Field: w.field, Line: line, Col: col, Message: fmt.Sprintf(format, a...)}
}

// refuseAction points at the {{ that opens the action, for a statement the
// parser positions after its keyword.
func (w *walker) refuseAction(pos parse.Pos, format string, a ...any) {
	if i := strings.LastIndex(w.text[:pos], "{{"); i >= 0 {
		pos = parse.Pos(i)
	}
	w.refuse(pos, format, a...)
}

func (w *walker) list(l *parse.ListNode, top bool) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		if w.err != nil {
			return
		}
		switch n := n.(type) {
		case *parse.TextNode, *parse.CommentNode:
		case *parse.ActionNode:
			if f := w.pipe(n.Pipe); f != "" && top && f == w.def.Required {
				w.foundRequired = true
			}
		case *parse.IfNode:
			w.pipe(n.Pipe)
			w.list(n.List, false)
			w.list(n.ElseList, false)
		case *parse.RangeNode:
			w.refuseAction(n.Pos, "{{range}} is not allowed")
		case *parse.WithNode:
			w.refuseAction(n.Pos, "{{with}} is not allowed")
		case *parse.TemplateNode:
			w.refuseAction(n.Pos, "{{template}} is not allowed")
		case *parse.BreakNode:
			w.refuseAction(n.Pos, "{{break}} is not allowed")
		case *parse.ContinueNode:
			w.refuseAction(n.Pos, "{{continue}} is not allowed")
		default:
			w.refuseAction(n.Position(), "this is not allowed in a template")
		}
	}
}

// pipe accepts exactly one variable, as {{.Name}} or the condition of an if,
// and returns its name.
func (w *walker) pipe(p *parse.PipeNode) string {
	switch {
	case len(p.Decl) > 0:
		w.refuse(p.Pos, "variables ($x) are not allowed")
	case len(p.Cmds) != 1:
		w.refuse(p.Pos, "pipes (|) are not allowed")
	}
	if w.err != nil {
		return ""
	}
	args := p.Cmds[0].Args
	for _, a := range args {
		if id, ok := a.(*parse.IdentifierNode); ok {
			w.refuse(id.Pos, "the function %s is not allowed; templates have no functions", id.Ident)
			return ""
		}
	}
	f, ok := args[0].(*parse.FieldNode)
	if !ok || len(args) != 1 {
		w.refuse(w.openParen(args[0].Position()), "only a variable such as {{.%s}} is allowed here", w.def.Vars[0].Name)
		return ""
	}
	if len(f.Ident) != 1 {
		// For a field of more than one part the parser positions the node at the
		// SECOND part, whatever the count; the error belongs at the first.
		start := int(f.Pos) - len(f.Ident[0]) - 1
		w.refuse(parse.Pos(start), ".%s is not allowed; templates only get the variables listed beside the editor, never nested data", strings.Join(f.Ident, "."))
		return ""
	}
	if !w.def.allows(f.Ident[0]) {
		w.refuse(f.Pos, ".%s is not available in this template; the variables are %s", f.Ident[0], dotted(w.def.VarNames()))
		return ""
	}
	return f.Ident[0]
}

// openParen moves a position back over the "(" of a parenthesised pipeline,
// which the parser positions after.
func (w *walker) openParen(pos parse.Pos) parse.Pos {
	if pos > 0 && w.text[pos-1] == '(' {
		return pos - 1
	}
	return pos
}

func dotted(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "." + n
	}
	return strings.Join(out, ", ")
}

// lineCol converts a byte offset to a 1-based line and character column.
func lineCol(text string, pos int) (line, col int) {
	// Clamped both ways: a position is never trusted to be inside the text.
	pos = max(0, min(pos, len(text)))
	before := text[:pos]
	line = 1 + strings.Count(before, "\n")
	col = 1 + utf8.RuneCountInString(before[strings.LastIndex(before, "\n")+1:])
	return line, col
}

var parseErrPrefix = regexp.MustCompile(`^template: [^:]*:(\d+): `)

// locateParseError turns the parser's error into a line, a column and a plain
// sentence. The parser reports a line and no column, so the column is the
// action on that line the message is about (the one holding {{end}}, say), or
// the last action on it. It is a pointer into the right line, not a promise.
func locateParseError(text string, err error) (line, col int, msg string) {
	msg = err.Error()
	line = 1
	if m := parseErrPrefix.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1]) // the pattern matched digits
		msg = strings.TrimPrefix(msg, m[0])
	}
	if msg == "unexpected EOF" {
		msg = "unexpected end of the text: an {{if}} is missing its {{end}}"
	}
	lines := strings.Split(text, "\n")
	if line > len(lines) {
		line = len(lines)
	}
	src := lines[line-1]
	at := strings.LastIndex(src, "{{")
	if kw := regexp.MustCompile(`\{\{(\w+)\}\}`).FindStringSubmatch(msg); kw != nil {
		if m := regexp.MustCompile(`\{\{-?\s*` + kw[1] + `\b`).FindStringIndex(src); m != nil {
			at = m[0]
		}
	}
	if at < 0 {
		return line, 1, msg
	}
	return line, 1 + utf8.RuneCountInString(src[:at]), msg
}

// findKeyword locates the first {{keyword in the text, for an error about a
// construct the parse tree no longer shows where it was.
func findKeyword(text string, words ...string) (line, col int) {
	re := regexp.MustCompile(`\{\{-?\s*(` + strings.Join(words, "|") + `)\b`)
	if m := re.FindStringIndex(text); m != nil {
		return lineCol(text, m[0])
	}
	return 1, 1
}

// limitedWriter stops a render that outgrows its cap: MaxRenderedBytes, or max
// (with its own error) for the HTML part.
type limitedWriter struct {
	w   io.Writer
	n   int
	max int
	err error
}

var errTooLarge = errors.New("the result is longer than 64 KiB")

func (l *limitedWriter) Write(p []byte) (int, error) {
	limit, err := l.max, l.err
	if limit == 0 {
		limit, err = MaxRenderedBytes, errTooLarge
	}
	if l.n+len(p) > limit {
		return 0, err
	}
	l.n += len(p)
	return l.w.Write(p)
}

func execute(def TemplateDef, text string, vars map[string]string) (string, error) {
	// An empty FuncMap and missingkey=error: a variable the data lacks is an
	// error, not an empty string, so a typo cannot ship as blank text.
	t, err := template.New(def.Name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing: %w", err)
	}
	var b strings.Builder
	if err := t.Execute(&limitedWriter{w: &b}, vars); err != nil {
		return "", fmt.Errorf("rendering: %w", err)
	}
	return b.String(), nil
}

// RenderTemplate validates a subject and body and renders them with vars. The
// error is an *InvalidTemplateError when the text is refused. For a shell the
// subject is ignored and returned empty.
func RenderTemplate(def TemplateDef, subject, body string, vars map[string]string) (renderedSubject, renderedBody string, err error) {
	if errs := ValidateTemplate(def, subject, body); len(errs) > 0 {
		return "", "", &InvalidTemplateError{Errors: errs}
	}
	if !def.Shell {
		if renderedSubject, err = execute(def, subject, vars); err != nil {
			return "", "", err
		}
		// Save checked the sample's values; these are the real ones. A line
		// break in a subject is a header break, so refuse it here too
		// (sanitizeHeader at send is the last line, not the only one).
		if strings.ContainsAny(renderedSubject, "\r\n") {
			return "", "", errors.New("the subject came out with a line break in it")
		}
	}
	if renderedBody, err = execute(def, body, vars); err != nil {
		return "", "", err
	}
	return renderedSubject, renderedBody, nil
}

// BrokenTemplate is a stored row that no longer validates.
type BrokenTemplate struct {
	Name      string
	Label     string
	Reason    string
	UpdatedAt time.Time
}

// BrokenTemplates picks the rows that would fail a send. It is the check the
// admin notice and the startup warning use, and it is the same ValidateTemplate
// the send path runs, so the notice cannot say a template is fine that a mail
// would then fail on. A row whose name is not in the registry (a template a
// later release retired) is never read by a send and is left out.
func BrokenTemplates(rows []emailtemplate.Override) []BrokenTemplate {
	return BrokenTemplatesFor(rows, defaultSiteName)
}

// BrokenTemplatesFor is BrokenTemplates with the help desk's real name, which
// the size of a header or footer that prints it depends on: a long name makes
// the notice appear at once.
func BrokenTemplatesFor(rows []emailtemplate.Override, siteName string) []BrokenTemplate {
	out := []BrokenTemplate{}
	for _, r := range rows {
		def, ok := LookupTemplate(r.Name)
		if !ok {
			continue
		}
		if errs := append(ValidateTemplate(def, r.Subject, r.Body), ValidateHTML(def, r.HTML)...); len(errs) > 0 {
			out = append(out, BrokenTemplate{Name: r.Name, Label: def.Label, Reason: errs[0].Error(), UpdatedAt: r.UpdatedAt})
		}
	}
	// A combination of parts that are each fine can still be too long to send.
	for _, b := range overCapBroken(rows, siteName) {
		seen := false
		for _, o := range out {
			seen = seen || o.Name == b.Name
		}
		if !seen {
			out = append(out, b)
		}
	}
	return out
}

// TemplateLister is what the startup check reads.
type TemplateLister interface {
	List(ctx context.Context) ([]emailtemplate.Override, error)
}

// WarnBrokenTemplates logs one WARN for every stored template that no longer
// validates, and returns how many. It never blocks startup: the install still
// serves, the affected mail fails visibly (the admin notice and the email
// health warning), and one click reverts it. A read failure is logged and
// counts as none.
func WarnBrokenTemplates(ctx context.Context, src TemplateLister, log *slog.Logger) int {
	rows, err := src.List(ctx)
	if err != nil {
		log.Error("checking the edited email templates failed", "error", err)
		return 0
	}
	broken := BrokenTemplates(rows)
	for _, b := range broken {
		log.Warn("an edited email template does not work with this release, so mail that uses it fails; revert it under Admin, Email templates",
			"template", b.Name, "label", b.Label, "reason", b.Reason)
	}
	return len(broken)
}
