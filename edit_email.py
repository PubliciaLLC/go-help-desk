p='internal/server/notify/email.go'
s=open(p).read()

s=s.replace('''// EmailDispatcher sends email notifications on ticket events.
// It is a no-op when SMTP is not configured.
type EmailDispatcher struct {
	cfg       *config.Config
	templates *template.Template
}
''','''// EmailDispatcher sends email notifications on ticket events.
// It is a no-op when SMTP is not configured.
type EmailDispatcher struct {
	cfg *config.Config
	// templates holds the compiled defaults, by file name. An admin's edited
	// version, when there is one, is read at send time instead (#158).
	templates *template.Template
	overrides emailtemplate.Reader
	site      SiteNamer
}

// SiteNamer supplies the help desk's name for the shared header and footer.
// It is the one value in those that is not fixed text.
type SiteNamer interface {
	SiteName(ctx context.Context) string
}

// WithTemplates lets the admin's edited templates, header and footer take
// effect. Without it every mail is the compiled default.
func (d *EmailDispatcher) WithTemplates(r emailtemplate.Reader, site SiteNamer) *EmailDispatcher {
	d.overrides, d.site = r, site
	return d
}

// ErrEmailNotConfigured is returned by SendTest when SMTP is not set up.
var ErrEmailNotConfigured = errors.New("email is not configured")
''')

s=s.replace('''func (d *EmailDispatcher) Dispatch(_ context.Context, event notification.Event) error {''','''func (d *EmailDispatcher) Dispatch(ctx context.Context, event notification.Event) error {''')
s=s.replace('''	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, templateName, data); err != nil {
		// Used to be swallowed ("non-fatal"), which made the worker delete the
		// row as delivered: a broken template lost every mail silently.
		return fmt.Errorf("rendering %s: %w: %w", templateName, ErrPermanent, err)
	}

	return d.send(to, subject, buf.Bytes())
}
''','''	view, _ := data.(map[string]string)
	subject, body, err := d.compose(ctx, strings.TrimSuffix(templateName, ".tmpl"), subject, view)
	if err != nil {
		return err
	}
	return d.send(to, subject, body)
}

// compose builds the subject and body of one mail: the template's edited
// version if the admin saved one, else the compiled default, wrapped in the
// shared header and footer (empty unless the admin wrote them).
//
// An edited version that no longer validates FAILS the mail, permanently,
// naming the template; it does not fall back to the default. The admin chose
// that knowing a reset or guest link could be missed (#158), and in return the
// failure is never quiet: it reaches the email-health warning, the startup log
// and the broken-template notice, and reverting is one click.
func (d *EmailDispatcher) compose(ctx context.Context, name, defaultSubject string, view map[string]string) (string, []byte, error) {
	def, ok := LookupTemplate(name)
	if !ok {
		return "", nil, fmt.Errorf("no email template %q: %w", name, ErrPermanent)
	}
	ovs, err := d.storedOverrides(ctx, name, NameHeader, NameFooter)
	if err != nil {
		return "", nil, err
	}

	subject, content := defaultSubject, ""
	if ov, edited := ovs[name]; edited {
		subject, content, err = RenderTemplate(def, ov.Subject, ov.Body, view)
		if err != nil {
			return "", nil, &staleTemplateError{label: def.Label, err: err}
		}
	} else {
		var buf bytes.Buffer
		if err := d.templates.ExecuteTemplate(&buf, name+".tmpl", view); err != nil {
			// Used to be swallowed ("non-fatal"), which made the worker delete the
			// row as delivered: a broken template lost every mail silently.
			return "", nil, fmt.Errorf("rendering %s: %w: %w", name, ErrPermanent, err)
		}
		content = buf.String()
	}

	header, err := d.renderShell(ctx, ovs, NameHeader)
	if err != nil {
		return "", nil, err
	}
	footer, err := d.renderShell(ctx, ovs, NameFooter)
	if err != nil {
		return "", nil, err
	}
	return subject, []byte(header + content + footer), nil
}

// storedOverrides reads the rows for these names in one lookup. A failed read
// is returned as it is, so the worker retries: the database having a bad
// moment is not the template's fault, and sending the default instead would
// quietly drop the admin's wording.
func (d *EmailDispatcher) storedOverrides(ctx context.Context, names ...string) (map[string]emailtemplate.Override, error) {
	if d.overrides == nil {
		return nil, nil
	}
	ovs, err := d.overrides.Overrides(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("reading edited email templates: %w", err)
	}
	return ovs, nil
}

func (d *EmailDispatcher) siteName(ctx context.Context) string {
	if d.site == nil {
		return "Go Help Desk"
	}
	return d.site.SiteName(ctx)
}

// renderShell renders the saved header or footer, or "" when there is none.
func (d *EmailDispatcher) renderShell(ctx context.Context, ovs map[string]emailtemplate.Override, name string) (string, error) {
	ov, ok := ovs[name]
	if !ok {
		return "", nil
	}
	def, _ := LookupTemplate(name)
	_, text, err := RenderTemplate(def, ov.Subject, ov.Body, map[string]string{"SiteName": d.siteName(ctx)})
	if err != nil {
		return "", &staleTemplateError{label: def.Label, err: err}
	}
	return text, nil
}

// staleTemplateError is an edited template that does not validate any more.
// Its text is what the outbox keeps as the failure reason and the admin reads,
// so it names the template and the problem and holds no address.
type staleTemplateError struct {
	label string
	err   error
}

func (e *staleTemplateError) Error() string {
	return fmt.Sprintf("email template %q: your edited version does not work with this release (%s); revert it under Admin, Email templates",
		e.label, e.err)
}

func (e *staleTemplateError) Unwrap() error { return e.err }

// Is makes it permanent: the text cannot change by itself, so the worker fails
// the row at once instead of retrying.
func (e *staleTemplateError) Is(target error) bool { return target == ErrPermanent }

// Preview is a draft rendered with sample data, as it would be mailed.
type Preview struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// PreviewDraft renders an unsaved draft of def with sample data, inside the
// saved header and footer. Drafting the header or footer itself shows it
// around a sample ticket mail. Nothing is sent or stored. An invalid draft is
// an *InvalidTemplateError.
func (d *EmailDispatcher) PreviewDraft(ctx context.Context, def TemplateDef, subject, body, trackingPrefix string) (Preview, error) {
	vars := SampleVars(def, d.cfg.BaseURL, trackingPrefix)
	renderedSubject, renderedBody, err := RenderTemplate(def, subject, body, vars)
	if err != nil {
		return Preview{}, err
	}
	ovs, err := d.storedOverrides(ctx, NameHeader, NameFooter)
	if err != nil {
		return Preview{}, err
	}
	header, err := d.renderShell(ctx, ovs, NameHeader)
	if err != nil {
		return Preview{}, err
	}
	footer, err := d.renderShell(ctx, ovs, NameFooter)
	if err != nil {
		return Preview{}, err
	}
	switch def.Name {
	case NameHeader:
		header = renderedBody
	case NameFooter:
		footer = renderedBody
	}
	if def.Shell {
		// Around a sample mail, so the admin sees it in place.
		sample, _ := LookupTemplate("ticket_created")
		renderedSubject, renderedBody, err = RenderTemplate(sample, sample.DefaultSubject, sample.DefaultBody,
			SampleVars(sample, d.cfg.BaseURL, trackingPrefix))
		if err != nil {
			return Preview{}, err
		}
	}
	return Preview{Subject: renderedSubject, Body: header + renderedBody + footer}, nil
}

// SendTest mails a preview to one address with "[Test] " in front of the
// subject, so it cannot be mistaken for a real notification. The caller picks
// the address; the request that triggers this carries none.
func (d *EmailDispatcher) SendTest(_ context.Context, to string, p Preview) error {
	if !d.cfg.EmailEnabled() {
		return ErrEmailNotConfigured
	}
	return d.send(to, "[Test] "+p.Subject, []byte(p.Body))
}
''')

start=s.index('	// A missing tracking number costs the subject line its reference')
end=s.index('// SendVerificationEmail sends')
new='''	templateName, ok = emailTemplateFor(event)
	if !ok {
		return "", "", "", nil, false
	}
	// The default subject is the registry's text, so the one the editor shows
	// as "the default" is the one a never-edited instance sends. A missing
	// tracking number costs the subject its reference, not the customer the
	// notification.
	def, found := LookupTemplate(strings.TrimSuffix(templateName, ".tmpl"))
	if !found {
		return "", "", "", nil, false
	}
	subject, err := execute(def, def.DefaultSubject, view)
	if err != nil {
		return "", "", "", nil, false
	}
	return templateName, subject, event.Recipient, view, true
}

// emailTemplateFor picks the template for an event.
//
// ticket_status_changed, ticket_resolved and ticket_reopened send only to a
// guest — Recipient is empty for a ticket with a reporter account, so
// eventToEmail returns before here. An account holder signs in and sees a
// status change; mailing them every transition would be a stream of email
// nobody asked for, carrying a link they do not need. They exist because
// rotation has to deliver its replacement: each invalidates the link already
// in the guest's inbox, so each has to carry the new one or the guest is
// locked out.
//
// Staff mail (#441) follows the same no-content rule: the tracking number and
// a link to the ticket behind the sign-in, nothing a requester wrote.
// Somebody mailed because of their group is told it is their group's ticket,
// and a follower that it is one they follow: "assigned to you" would be false
// for them.
func emailTemplateFor(event notification.Event) (string, bool) {
	staff := func(base string) (string, bool) {
		switch event.StaffReason {
		case notification.SwitchGroup:
			return base + "_group.tmpl", true
		case notification.SwitchFollow:
			return base + "_follow.tmpl", true
		}
		return base + ".tmpl", true
	}
	switch event.Type {
	case notification.EventTicketCreated:
		return "ticket_created.tmpl", true
	case notification.EventTicketReplied:
		return "ticket_replied.tmpl", true
	case notification.EventTicketStatusChanged:
		return "ticket_status_changed.tmpl", true
	case notification.EventTicketResolved:
		return "ticket_resolved.tmpl", true
	case notification.EventTicketReopened:
		return "ticket_reopened.tmpl", true
	case notification.EventStaffAssigned:
		return staff("staff_assigned")
	case notification.EventStaffRequesterReplied:
		return staff("staff_requester_replied")
	case notification.EventStaffReopened:
		return staff("staff_reopened")
	case notification.EventGuestLinkResent:
		return "guest_link.tmpl", true
	}
	return "", false
}

'''
s=s[:start]+new+s[end:]

# account mails
a0=s.index('func (d *EmailDispatcher) SendVerificationEmail')
b0=s.index('// send builds a MIME message')
acct='''func (d *EmailDispatcher) SendVerificationEmail(ctx context.Context, to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	return d.sendAccountMail(ctx, to, "email_verification", map[string]string{
		"VerifyURL": baseURL + "/verify-email?token=" + token,
	})
}

// SendPasswordResetEmail sends the link a "forgot password" request is
// answered with (#451). It implements passwordreset.Mailer. The link's host
// comes from baseURL, which the caller takes from configuration, never from a
// request.
func (d *EmailDispatcher) SendPasswordResetEmail(ctx context.Context, to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	return d.sendAccountMail(ctx, to, "password_reset", map[string]string{
		"ResetURL": baseURL + "/reset-password?token=" + token,
	})
}

func (d *EmailDispatcher) sendAccountMail(ctx context.Context, to, name string, view map[string]string) error {
	def, _ := LookupTemplate(name)
	defaultSubject, err := execute(def, def.DefaultSubject, view)
	if err != nil {
		return fmt.Errorf("rendering %s subject: %w: %w", name, ErrPermanent, err)
	}
	subject, body, err := d.compose(ctx, name, defaultSubject, view)
	if err != nil {
		return err
	}
	return d.send(to, subject, body)
}

'''
s=s[:a0]+acct+s[b0:]
s=s.replace('"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"\n)','"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"\n\t"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"\n)')
open(p,'w').write(s)
