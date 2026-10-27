package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// The email template editor (#158): list, read, save, revert, preview and send
// a test of the 17 mail templates and the shared header and footer.
//
// The whole route is session-only (DenyMachineCredentials). Editing what the
// server mails from the operator's domain is a decision for a person at a
// browser, not for a key; and a key that could edit the reset mail could point
// it at a link of its own.

// TemplateMailer is the part of the email dispatcher the editor needs.
type TemplateMailer interface {
	PreviewDraftHTML(ctx context.Context, def notify.TemplateDef, subject, body, html, trackingPrefix string) (notify.Preview, error)
	SendTest(ctx context.Context, to string, p notify.Preview) error
}

// WithEmailTemplates turns on the template editor. Without it the routes
// answer 503 and mail is the compiled default.
func WithEmailTemplates(store emailtemplate.Store, mailer TemplateMailer) Option {
	return func(s *Server) {
		s.emailTemplates = store
		s.templateMailer = mailer
	}
}

// templateTestsPerWindow caps test sends per administrator: each is a real
// message through the operator's mail relay.
const (
	templateTestsPerWindow = 5
	templateTestWindow     = 10 * time.Minute
)

// emailTemplateEntityID is the audit entity id for a template. Templates have
// no row id, only a name, and the audit log keys on a UUID.
func emailTemplateEntityID(name string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_template:"+name))
}

type templateVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type templateSummary struct {
	Name          string     `json:"name"`
	Label         string     `json:"label"`
	Group         string     `json:"group"`
	Shell         bool       `json:"shell"`
	Customised    bool       `json:"customised"`
	HasHTML       bool       `json:"has_html"`
	Broken        bool       `json:"broken"`
	UpdatedAt     *time.Time `json:"updated_at"`
	UpdatedByName string     `json:"updated_by_name"`
}

type templateDetail struct {
	templateSummary
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	DefaultSubject string `json:"default_subject"`
	DefaultBody    string `json:"default_body"`
	// HTML is the optional HTML part; empty means the mail is plain text. There
	// is no default HTML, so there is no default_html.
	HTML      string             `json:"html"`
	Required  string             `json:"required"`
	Variables []templateVariable `json:"variables"`
	// BrokenReason is why the saved version cannot be sent, when Broken.
	BrokenReason   string `json:"broken_reason,omitempty"`
	MaxBodyBytes   int    `json:"max_body_bytes"`
	MaxSubjectByte int    `json:"max_subject_bytes"`
	MaxHTMLBytes   int    `json:"max_html_bytes"`
}

// templateDraft is what the editor sends. A pointer, so "not sent" is told
// apart from "empty": a test send with neither uses the saved text.
type templateDraft struct {
	Subject *string `json:"subject"`
	Body    *string `json:"body"`
	HTML    *string `json:"html"`
}

func (d templateDraft) html() string {
	if d.HTML == nil {
		return ""
	}
	return *d.HTML
}

func (d templateDraft) subject() string {
	if d.Subject == nil {
		return ""
	}
	return *d.Subject
}

func (d templateDraft) body() string {
	if d.Body == nil {
		return ""
	}
	return *d.Body
}

func (s *Server) templatesAvailable(w http.ResponseWriter) bool {
	if s.emailTemplates == nil || s.templateMailer == nil {
		Error(w, http.StatusServiceUnavailable, "not_available", "email templates are not available")
		return false
	}
	return true
}

// templateFromPath answers 404 for a name that is not in the registry.
func templateFromPath(w http.ResponseWriter, r *http.Request) (notify.TemplateDef, bool) {
	def, ok := notify.LookupTemplate(chi.URLParam(r, "name"))
	if !ok {
		Error(w, http.StatusNotFound, "not_found", "no such email template")
	}
	return def, ok
}

func (s *Server) summarise(ctx context.Context, def notify.TemplateDef, ov *emailtemplate.Override, broken map[string]notify.BrokenTemplate) templateSummary {
	sum := templateSummary{Name: def.Name, Label: def.Label, Group: def.Group, Shell: def.Shell}
	if ov == nil {
		return sum
	}
	sum.Customised = true
	sum.HasHTML = ov.HTML != ""
	_, sum.Broken = broken[def.Name]
	t := ov.UpdatedAt
	sum.UpdatedAt = &t
	if ov.UpdatedBy != nil {
		// A name that cannot be looked up costs the label, not the page.
		if u, err := s.users.GetByID(ctx, *ov.UpdatedBy); err == nil {
			sum.UpdatedByName = u.DisplayName
		}
	}
	return sum
}

// GET /api/v1/admin/email-templates
func (s *Server) handleListEmailTemplates(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	rows, err := s.emailTemplates.List(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	byName := make(map[string]emailtemplate.Override, len(rows))
	for _, o := range rows {
		byName[o.Name] = o
	}
	broken := brokenByName(rows)
	out := []templateSummary{}
	for _, def := range notify.Templates() {
		var ov *emailtemplate.Override
		if o, ok := byName[def.Name]; ok {
			ov = &o
		}
		out = append(out, s.summarise(r.Context(), def, ov, broken))
	}
	JSON(w, http.StatusOK, out)
}

// brokenByName is which stored templates would fail a send, judged over ALL the
// rows: whether a combination of header, footer and body is too long cannot be
// seen from one row.
func brokenByName(rows []emailtemplate.Override) map[string]notify.BrokenTemplate {
	out := map[string]notify.BrokenTemplate{}
	for _, b := range notify.BrokenTemplates(rows) {
		out[b.Name] = b
	}
	return out
}

func (s *Server) templateDetail(ctx context.Context, def notify.TemplateDef) (templateDetail, error) {
	rows, err := s.emailTemplates.List(ctx)
	if err != nil {
		return templateDetail{}, err
	}
	var ov *emailtemplate.Override
	for _, o := range rows {
		if o.Name == def.Name {
			o := o
			ov = &o
		}
	}
	broken := brokenByName(rows)
	d := templateDetail{
		templateSummary: s.summarise(ctx, def, ov, broken),
		Subject:         def.DefaultSubject, Body: def.DefaultBody,
		DefaultSubject: def.DefaultSubject, DefaultBody: def.DefaultBody,
		Required: def.Required, Variables: []templateVariable{},
		MaxBodyBytes: notify.MaxBodyBytes, MaxSubjectByte: notify.MaxSubjectBytes, MaxHTMLBytes: notify.MaxHTMLBytes,
	}
	for _, v := range def.Vars {
		d.Variables = append(d.Variables, templateVariable{Name: v.Name, Description: v.Description})
	}
	if ov != nil {
		d.Subject, d.Body, d.HTML = ov.Subject, ov.Body, ov.HTML
		if b, ok := broken[def.Name]; ok {
			d.BrokenReason = b.Reason
		}
	}
	return d, nil
}

// GET /api/v1/admin/email-templates/{name}
func (s *Server) handleGetEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	def, ok := templateFromPath(w, r)
	if !ok {
		return
	}
	d, err := s.templateDetail(r.Context(), def)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, d)
}

// invalidTemplate is the 400 for a draft the validator refuses. The first
// problem is the envelope's own field/line/col; errors holds all of them, one
// per field, so the editor can mark each.
func invalidTemplate(w http.ResponseWriter, errs []notify.TemplateError) {
	first := errs[0]
	JSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
		"code": "invalid_template", "message": first.Error(),
		"field": first.Field, "line": first.Line, "col": first.Col, "errors": errs,
	}})
}

// templateText is what an audit entry records of a template: all of it, so the
// log is the history. The HTML is recorded in full too, whether or not there
// is any, so "no HTML" reads as an empty value and not as a missing one.
func templateText(subject, body, html, name string) map[string]any {
	return map[string]any{"template_name": name, "template_subject": subject, "template_body": body, "template_html": html}
}

// PUT /api/v1/admin/email-templates/{name}
//
// Validated here whatever the editor already checked: the editor's check is a
// convenience and this one is the authority. Saving text equal to the shipped
// default removes the row instead of storing a copy, so "customised" means
// exactly "differs from the default" and a later release can improve a default
// nobody had changed.
func (s *Server) handlePutEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	def, ok := templateFromPath(w, r)
	if !ok {
		return
	}
	var in templateDraft
	if err := DecodeJSON(r, &in); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	subject, body, html := in.subject(), in.body(), in.html()
	rows, err := s.emailTemplates.List(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	stored := make(map[string]emailtemplate.Override, len(rows))
	for _, o := range rows {
		stored[o.Name] = o
	}
	if in.HTML == nil {
		// A client that does not know about HTML (a tab opened before the
		// upgrade) must not wipe it: no key means unchanged, and removing the
		// HTML takes an explicit empty string.
		html = stored[def.Name].HTML
	}
	if errs := append(notify.ValidateTemplate(def, subject, body), notify.ValidateHTML(def, html)...); len(errs) > 0 {
		invalidTemplate(w, errs)
		return
	}
	// The size limit is on the header, body and footer together, which no part
	// can see alone: the same check a send makes, with long links so that what
	// is saved here can be sent.
	if te := notify.CheckJoinedHTML(def, stored, emailtemplate.Override{Name: def.Name, Subject: subject, Body: body, HTML: html}); te != nil {
		invalidTemplate(w, []notify.TemplateError{*te})
		return
	}

	actor := authmw.GetActor(r)
	var next *emailtemplate.Override
	// There is no default HTML, so any HTML makes the template differ.
	if subject != def.DefaultSubject || body != def.DefaultBody || html != "" {
		next = &emailtemplate.Override{Name: def.Name, Subject: subject, Body: body, HTML: html, UpdatedBy: &actor.UserID}
	}
	err = s.emailTemplates.Apply(r.Context(), def.Name, next, func(prev *emailtemplate.Override) *audit.Entry {
		if prev == nil && next == nil {
			return nil // the default over the default changes nothing
		}
		beforeSubject, beforeBody, beforeHTML := def.DefaultSubject, def.DefaultBody, ""
		if prev != nil {
			beforeSubject, beforeBody, beforeHTML = prev.Subject, prev.Body, prev.HTML
		}
		return &audit.Entry{
			ID: uuid.New(), ActorID: &actor.UserID, EntityType: "email_template", EntityID: emailTemplateEntityID(def.Name),
			Action: "email_template_saved", Before: templateText(beforeSubject, beforeBody, beforeHTML, def.Name),
			After: templateText(subject, body, html, def.Name), CreatedAt: time.Now(),
		}
	})
	if err != nil {
		handleError(w, fmt.Errorf("saving email template %s: %w", def.Name, err))
		return
	}
	d, err := s.templateDetail(r.Context(), def)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, d)
}

// DELETE /api/v1/admin/email-templates/{name}: back to the shipped text. The
// edited text is not lost: it is in the audit entry.
func (s *Server) handleRevertEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	def, ok := templateFromPath(w, r)
	if !ok {
		return
	}
	actor := authmw.GetActor(r)
	err := s.emailTemplates.Apply(r.Context(), def.Name, nil, func(prev *emailtemplate.Override) *audit.Entry {
		if prev == nil {
			return nil // nothing was edited, so nothing changed
		}
		return &audit.Entry{
			ID: uuid.New(), ActorID: &actor.UserID, EntityType: "email_template", EntityID: emailTemplateEntityID(def.Name),
			Action: "email_template_reverted", Before: templateText(prev.Subject, prev.Body, prev.HTML, def.Name), CreatedAt: time.Now(),
		}
	})
	if err != nil {
		handleError(w, fmt.Errorf("reverting email template %s: %w", def.Name, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/admin/email-templates/{name}/preview
//
// The live check as well as the preview: an invalid draft is answered with 200
// and valid:false and every problem with its place, not refused, because "not
// valid yet" is the normal state of a draft mid-typing. Nothing is sent or
// stored.
func (s *Server) handlePreviewEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	def, ok := templateFromPath(w, r)
	if !ok {
		return
	}
	var in templateDraft
	if err := DecodeJSON(r, &in); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	p, err := s.templateMailer.PreviewDraftHTML(r.Context(), def, in.subject(), in.body(), in.html(), s.adminSvc.TicketPrefix(r.Context()))
	var invalid *notify.InvalidTemplateError
	switch {
	case errors.As(err, &invalid):
		JSON(w, http.StatusOK, map[string]any{"valid": false, "errors": invalid.Errors})
	case errors.Is(err, notify.ErrPermanent):
		// The draft is valid on its own, but it cannot be rendered with what is
		// saved (a saved header or footer that no longer works).
		JSON(w, http.StatusOK, map[string]any{"valid": true, "errors": []notify.TemplateError{}, "preview_error": err.Error()})
	case err != nil:
		handleError(w, err)
	default:
		JSON(w, http.StatusOK, map[string]any{"valid": true, "errors": []notify.TemplateError{}, "subject": p.Subject, "body": p.Body, "html": p.HTML})
	}
}

// POST /api/v1/admin/email-templates/{name}/test
//
// Sends the draft, or the saved text when none is given, to the signed-in
// administrator's own address and nowhere else: the request has no recipient to
// honour. It is a real message through the operator's relay, so it is limited
// per administrator, and the SMTP error comes back to the page because that is
// the whole point of sending a test.
func (s *Server) handleTestEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templatesAvailable(w) {
		return
	}
	def, ok := templateFromPath(w, r)
	if !ok {
		return
	}
	if !s.cfg.EmailEnabled() {
		Error(w, http.StatusServiceUnavailable, "email_not_configured", "email is not configured: set SMTP_HOST and SMTP_FROM")
		return
	}
	var in templateDraft
	if err := DecodeJSON(r, &in); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	subject, body, html := in.subject(), in.body(), in.html()
	if in.Subject == nil && in.Body == nil || in.HTML == nil {
		// What is not given is what is saved (a tab from before HTML existed
		// sends no html key, and its draft is still in front of the saved HTML).
		cur, err := s.templateDetail(r.Context(), def)
		if err != nil {
			handleError(w, err)
			return
		}
		if in.Subject == nil && in.Body == nil {
			subject, body = cur.Subject, cur.Body
		}
		if in.HTML == nil {
			html = cur.HTML
		}
	}
	actor := authmw.GetActor(r)
	me, err := s.users.GetByID(r.Context(), actor.UserID)
	if err != nil {
		handleError(w, err)
		return
	}

	p, err := s.templateMailer.PreviewDraftHTML(r.Context(), def, subject, body, html, s.adminSvc.TicketPrefix(r.Context()))
	var invalid *notify.InvalidTemplateError
	if errors.As(err, &invalid) {
		invalidTemplate(w, invalid.Errors)
		return
	}
	if err != nil {
		Error(w, http.StatusConflict, "template_broken", err.Error())
		return
	}

	// Spent before the send, and not given back on failure: a relay that
	// errors is still a relay being hit.
	if ok, _ := s.templateTestLimiter.Take(actor.UserID.String()); !ok {
		tooManyAttempts(w, s.templateTestLimiter.Remaining())
		return
	}
	if err := s.templateMailer.SendTest(r.Context(), me.Email, p); err != nil {
		if errors.Is(err, notify.ErrEmailNotConfigured) {
			Error(w, http.StatusServiceUnavailable, "email_not_configured", err.Error())
			return
		}
		Error(w, http.StatusBadGateway, "send_failed", err.Error())
		return
	}
	// Best effort, like the key audit: a sent test is not a change, and the
	// entry names the template, never the address.
	if s.auditStore != nil {
		s.writeAudit(r.Context(), audit.Entry{
			ID: uuid.New(), ActorID: &actor.UserID, EntityType: "email_template", EntityID: emailTemplateEntityID(def.Name),
			Action: "email_template_test_sent", After: map[string]any{"template_name": def.Name}, CreatedAt: time.Now(),
		})
	}
	JSON(w, http.StatusOK, map[string]any{"sent": true})
}

// brokenTemplate is one entry of email.broken_templates in the security warnings.
type brokenTemplate struct {
	Name      string    `json:"name"`
	Label     string    `json:"label"`
	Reason    string    `json:"reason"`
	UpdatedAt time.Time `json:"updated_at"`
}

// brokenTemplates reads the stored rows and returns those a send would fail
// on. Live on every request (at most 19 rows), so a revert clears the notice at
// once on every replica. A failed read reports nothing: a warning that fires on
// a database hiccup is worse than one that stays quiet.
func (s *Server) brokenTemplates(ctx context.Context) []brokenTemplate {
	out := []brokenTemplate{}
	if s.emailTemplates == nil {
		return out
	}
	rows, err := s.emailTemplates.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "reading email templates for the security warnings failed", "error", err)
		return out
	}
	for _, b := range notify.BrokenTemplates(rows) {
		out = append(out, brokenTemplate{Name: b.Name, Label: b.Label, Reason: b.Reason, UpdatedAt: b.UpdatedAt})
	}
	return out
}
