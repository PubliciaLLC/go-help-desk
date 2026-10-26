import { useEffect, useId, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  emailTemplateProblems,
  getEmailTemplate,
  listEmailTemplates,
  previewEmailTemplate,
  revertEmailTemplate,
  saveEmailTemplate,
  testEmailTemplate,
} from '@/api/admin'
import type {
  EmailTemplateDetail,
  EmailTemplateDraft,
  EmailTemplateGroup,
  EmailTemplatePreview,
  EmailTemplateProblem,
  EmailTemplateSummary,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'

// How long the draft sits unchanged before it is sent for checking. Long
// enough that typing does not send a request per keystroke, short enough that
// the answer lands while the person is still looking at the line.
const CHECK_DELAY_MS = 400

const GROUPS: { id: EmailTemplateGroup; title: string }[] = [
  { id: 'requester', title: 'Mail to requesters' },
  { id: 'staff', title: 'Mail to staff' },
  { id: 'account', title: 'Account mail' },
  { id: 'shared', title: 'Header and footer' },
]

const ADVISORY = 'https://github.com/PubliciaLLC/go-help-desk/security/advisories/GHSA-p2wx-wvfr-7h3p'

// 44 px on a coarse pointer (a phone), as on the categories page. Desktop is
// unchanged, and the shared Button default is left alone.
const TOUCH = 'pointer-coarse:min-h-11 pointer-coarse:min-w-11'

const queryKey = ['admin', 'email-templates'] as const

// The template open in the editor is kept in the address (?t=name), so the
// notice in the banner can link straight to it.
function selectedFromAddress(): string | null {
  return new URLSearchParams(window.location.search).get('t')
}

function setAddress(name: string | null) {
  const url = window.location.pathname + (name ? `?t=${encodeURIComponent(name)}` : '')
  window.history.replaceState(null, '', url)
}

export function EmailTemplatesPage() {
  const [selected, setSelected] = useState<string | null>(selectedFromAddress)
  const choose = (name: string | null) => {
    setSelected(name)
    setAddress(name)
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Email templates</h1>
          <p className="mt-1 text-sm text-gray-500">
            The wording of the mail this help desk sends. Templates cannot include ticket text, by design: mail leaves
            from your domain, and whoever files a ticket chooses its subject and replies, so a template only gets the
            tracking number and links this server builds (see{' '}
            <a className="underline" href={ADVISORY} target="_blank" rel="noopener noreferrer">
              GHSA-p2wx-wvfr-7h3p
            </a>
            ). The header and footer are added to every mail.
          </p>
        </div>
        {selected ? <Editor name={selected} onBack={() => choose(null)} /> : <TemplateList onOpen={choose} />}
      </div>
    </Layout>
  )
}

function TemplateList({ onOpen }: { onOpen: (name: string) => void }) {
  const { data, isLoading, error } = useQuery({ queryKey, queryFn: listEmailTemplates })
  if (isLoading) return <div className="flex justify-center py-12"><Spinner /></div>
  if (error) return <p role="alert" className="text-sm text-red-600">{extractError(error)}</p>

  return (
    <div className="space-y-5">
      {GROUPS.map((g) => {
        const rows = (data ?? []).filter((t) => t.group === g.id)
        if (rows.length === 0) return null
        return (
          <section key={g.id} aria-labelledby={`grp-${g.id}`}>
            <h2 id={`grp-${g.id}`} className="mb-2 text-sm font-semibold text-gray-700">{g.title}</h2>
            <ul className="divide-y rounded-lg border bg-white">
              {rows.map((t) => (
                <li key={t.name}>
                  <TemplateRow t={t} onOpen={onOpen} />
                </li>
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

function TemplateRow({ t, onOpen }: { t: EmailTemplateSummary; onOpen: (name: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(t.name)}
      className="flex min-h-[44px] w-full flex-wrap items-center gap-2 px-4 py-2 text-left text-sm hover:bg-gray-50"
    >
      <span className="font-medium text-gray-900 wrap-anywhere">{t.label}</span>
      {t.broken && <span className="rounded bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-800">Broken</span>}
      {t.customised && <span className="rounded bg-blue-50 px-1.5 py-0.5 text-xs text-blue-800">Customised</span>}
      {t.has_html && <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700">HTML</span>}
    </button>
  )
}

function Editor({ name, onBack }: { name: string; onBack: () => void }) {
  const { data, isLoading, error } = useQuery({
    queryKey: [...queryKey, name],
    queryFn: () => getEmailTemplate(name),
  })
  return (
    <div className="space-y-4">
      <Button type="button" variant="outline" size="sm" className={TOUCH} onClick={onBack}>
        All templates
      </Button>
      {isLoading && <div className="flex justify-center py-12"><Spinner /></div>}
      {error && <p role="alert" className="text-sm text-red-600">{extractError(error)}</p>}
      {/* Keyed by what was saved, so a save or a revert starts the draft afresh. */}
      {data && <EditorForm key={`${data.name}|${data.updated_at ?? ''}|${data.customised}`} detail={data} />}
    </div>
  )
}

interface Check {
  /** The draft this answer is about; an answer to an older draft is ignored. */
  draft: string
  result: EmailTemplatePreview | null
  failure: string
}

const byteLength = (s: string) => new TextEncoder().encode(s).length

type FieldName = 'subject' | 'body' | 'html'

// The draft as the API takes it. No html key when there is none: that is how
// the server is told "no HTML part".
const draftOf = (subject: string, body: string, html: string): EmailTemplateDraft =>
  html === '' ? { subject, body } : { subject, body, html }

const escapeText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

// Starting points, inserted into an empty HTML field. They are drafts like any
// other: nothing is saved until the person presses Save, and the server checks
// them the same way. The header opens a layout that the footer closes, so a
// pair of them brands every mail that has an HTML part.
const HEADER_STARTER = `<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,Helvetica,sans-serif;color:#18181b">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:24px 12px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;background:#ffffff">
<tr><td style="padding:16px 24px;background:#1d4ed8;color:#ffffff;font-size:18px;font-weight:bold">{{.SiteName}}</td></tr>
<tr><td style="padding:24px;font-size:16px;line-height:1.5">
`

const FOOTER_STARTER = `
</td></tr>
<tr><td style="padding:16px 24px;font-size:12px;color:#71717a">Sent by {{.SiteName}}</td></tr>
</table>
</td></tr></table>
</body>
</html>
`

// The template's own text as paragraphs, with its link made clickable.
function contentStarter(detail: EmailTemplateDetail, text: string): string {
  const paras = text.trim().split(/\n\s*\n/).map((p) => `<p>${escapeText(p.trim()).replace(/\n/g, '<br>\n')}</p>`)
  let out = paras.join('\n')
  if (detail.required) {
    const v = `{{.${detail.required}}}`
    out = out.split(v).join(`<a href="${v}">${v}</a>`)
    if (!out.includes(`href="${v}"`)) out += `\n<p><a href="${v}">${v}</a></p>`
  }
  return out + '\n'
}

// What the preview frame shows. The policy comes first and the page's own
// policy still applies on top (policies combine), so the markup can only
// tighten it. No script, no remote anything; images only as data:.
const PREVIEW_CSP = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`
// A click on a sample link goes nowhere: the sandbox lets nothing open a new window, so target=_blank is refused.
const previewDocument = (html: string) => `${PREVIEW_CSP}<base target="_blank">${html}`

function EditorForm({ detail }: { detail: EmailTemplateDetail }) {
  const qc = useQueryClient()
  const [subject, setSubject] = useState(detail.subject)
  const [body, setBody] = useState(detail.body)
  const [html, setHTML] = useState(detail.html ?? '')
  const [check, setCheck] = useState<Check | null>(null)
  // The server's refusal of the last save, shown until the next edit.
  const [refused, setRefused] = useState<{ draft: string; problems: EmailTemplateProblem[]; message: string } | null>(null)
  const [notice, setNotice] = useState('')
  const [noticeIsError, setNoticeIsError] = useState(false)
  const [confirmRevert, setConfirmRevert] = useState(false)
  const lastField = useRef<FieldName>('body')
  const subjectRef = useRef<HTMLInputElement>(null)
  const bodyRef = useRef<HTMLTextAreaElement>(null)
  const htmlRef = useRef<HTMLTextAreaElement>(null)
  const ids = useId()

  const draftKey = JSON.stringify([subject, body, html])
  const dirty = subject !== detail.subject || body !== detail.body || html !== (detail.html ?? '')

  // The check: the draft goes to the server 400 ms after the last change. An
  // answer that arrives for an older draft is stored but never matches the
  // current one, so it cannot unlock Save.
  useEffect(() => {
    const draft = draftKey
    const timer = setTimeout(() => {
      previewEmailTemplate(detail.name, draftOf(subject, body, html))
        .then((result) => setCheck({ draft, result, failure: '' }))
        .catch((err) => setCheck({ draft, result: null, failure: extractError(err) }))
    }, CHECK_DELAY_MS)
    return () => clearTimeout(timer)
    // subject, body and html are inside draftKey.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftKey, detail.name])

  const settled = check !== null && check.draft === draftKey
  const result = settled ? check.result : null
  const valid = settled && !!result?.valid
  const serverRefused = refused !== null && refused.draft === draftKey ? refused : null

  const problems: EmailTemplateProblem[] = serverRefused?.problems.length
    ? serverRefused.problems
    : result && !result.valid
      ? result.errors
      : []
  const forField = (f: FieldName) => problems.filter((p) => p.field === f)

  const refresh = () => {
    void qc.invalidateQueries({ queryKey })
    void qc.invalidateQueries({ queryKey: ['security-warnings'] })
  }

  const save = useMutation({
    mutationFn: () => saveEmailTemplate(detail.name, draftOf(subject, body, html)),
    onSuccess: () => {
      setNotice('Saved.')
      setNoticeIsError(false)
      refresh()
    },
    onError: (err) => {
      setNotice('')
      setRefused({ draft: draftKey, problems: emailTemplateProblems(err), message: extractError(err) })
    },
  })

  const revert = useMutation({
    mutationFn: () => revertEmailTemplate(detail.name),
    onSuccess: () => {
      setConfirmRevert(false)
      refresh()
    },
  })

  const sendTest = useMutation({
    mutationFn: () => testEmailTemplate(detail.name, draftOf(subject, body, html)),
    onSuccess: () => {
      setNotice('A test email was sent to your own address.')
      setNoticeIsError(false)
    },
    onError: (err) => {
      setNotice(extractError(err))
      setNoticeIsError(true)
    },
  })

  const insert = (token: string) => {
    // The header and footer have no subject; a subject chip never lands there.
    const field: FieldName = detail.shell && lastField.current === 'subject' ? 'body' : lastField.current
    const el = field === 'subject' ? subjectRef.current : field === 'html' ? htmlRef.current : bodyRef.current
    const value = field === 'subject' ? subject : field === 'html' ? html : body
    const start = el?.selectionStart ?? value.length
    const end = el?.selectionEnd ?? value.length
    const next = value.slice(0, start) + token + value.slice(end)
    if (field === 'subject') setSubject(next)
    else if (field === 'html') setHTML(next)
    else setBody(next)
    const caret = start + token.length
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(caret, caret)
    })
  }

  const starter = detail.shell
    ? detail.name === '_footer' ? FOOTER_STARTER : HEADER_STARTER
    : contentStarter(detail, body)

  const canSave = dirty && valid && !save.isPending

  return (
    <form
      aria-label={`Edit ${detail.label}`}
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (canSave) save.mutate()
      }}
    >
      <h2 className="text-lg font-semibold text-gray-900 wrap-anywhere">{detail.label}</h2>
      {detail.shell && (
        <p className="text-sm text-gray-500">
          Shared by all mail: added to the start (header) or the end (footer) of every message, whichever template it
          is. Leave it empty to add nothing. It cannot use the tracking number or any link.
        </p>
      )}

      {detail.broken && (
        <div role="alert" className="rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900">
          <p className="font-semibold">The saved version does not work with this release, so mail that uses it is not being sent.</p>
          {detail.broken_reason && <p className="mt-1 wrap-anywhere">{detail.broken_reason}</p>}
          <p className="mt-1">Fix it below, or revert to the default.</p>
        </div>
      )}

      {!detail.shell && (
        <Field
          id={`${ids}-subject`}
          label="Subject"
          problems={forField('subject')}
          hint={`One line, up to ${detail.max_subject_bytes} bytes.`}
        >
          {(aria) => (
            <Input
              id={`${ids}-subject`}
              ref={subjectRef}
              value={subject}
              onFocus={() => (lastField.current = 'subject')}
              onChange={(e) => setSubject(e.target.value)}
              className="font-mono text-sm"
              {...aria}
            />
          )}
        </Field>
      )}

      <Field
        id={`${ids}-body`}
        label="Body"
        problems={forField('body')}
        hint={
          detail.required
            ? `Must include {{.${detail.required}}} outside any {{if}}: without it the person cannot reach the link. ${byteLength(body)} of ${detail.max_body_bytes} bytes.`
            : `${byteLength(body)} of ${detail.max_body_bytes} bytes.`
        }
      >
        {(aria) => (
          <Textarea
            id={`${ids}-body`}
            ref={bodyRef}
            value={body}
            rows={12}
            spellCheck={false}
            onFocus={() => (lastField.current = 'body')}
            onChange={(e) => setBody(e.target.value)}
            className="font-mono text-sm"
            {...aria}
          />
        )}
      </Field>

      <Field
        id={`${ids}-html`}
        label="HTML"
        problems={forField('html')}
        hint={
          (detail.shell
            ? 'Added to the HTML part of every template that has one. '
            : 'Leave empty to send plain text only. With HTML the mail has both parts, and the text above is still what plain-text readers get. ') +
          (detail.required ? `Needs {{.${detail.required}}} outside any {{if}}, as text or as the whole href of a link. ` : '') +
          `${byteLength(html)} of ${detail.max_html_bytes ?? 65536} bytes.`
        }
      >
        {(aria) => (
          <Textarea
            id={`${ids}-html`}
            ref={htmlRef}
            value={html}
            rows={12}
            spellCheck={false}
            onFocus={() => (lastField.current = 'html')}
            onChange={(e) => setHTML(e.target.value)}
            className="font-mono text-sm"
            {...aria}
          />
        )}
      </Field>
      <Button type="button" variant="outline" className={TOUCH} disabled={html !== ''} onClick={() => setHTML(starter)}>
        Start from a basic layout
      </Button>

      <div>
        <p className="text-sm font-medium text-gray-900">Variables</p>
        <ul className="mt-1 space-y-1">
          {detail.variables.map((v) => (
            <li key={v.name} className="flex flex-wrap items-baseline gap-x-2 text-sm">
              <button
                type="button"
                onClick={() => insert(`{{.${v.name}}}`)}
                className={`inline-flex items-center rounded bg-gray-100 px-1.5 py-1 font-mono text-xs text-gray-900 hover:bg-gray-200 ${TOUCH}`}
              >
                {`{{.${v.name}}}`}
              </button>
              <span className="text-gray-500 wrap-anywhere">{v.description}</span>
            </li>
          ))}
        </ul>
        <p className="mt-1 text-xs text-gray-500">
          Also allowed: {'{{if .Name}} … {{else}} … {{end}}'}. Nothing else.
        </p>
      </div>

      <div className="flex flex-wrap gap-2">
        <Button type="submit" className={TOUCH} disabled={!canSave}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
        <Button type="button" variant="outline" className={TOUCH} disabled={!valid || sendTest.isPending} onClick={() => sendTest.mutate()}>
          {sendTest.isPending ? 'Sending…' : 'Send test'}
        </Button>
        {detail.customised && (
          <Button type="button" variant="outline" className={TOUCH} onClick={() => setConfirmRevert(true)}>
            Revert to default
          </Button>
        )}
      </div>

      {!settled && <p className="text-xs text-gray-500">Checking…</p>}
      {settled && check.failure && (
        <p className="text-sm text-red-600 wrap-anywhere">The draft could not be checked: {check.failure}</p>
      )}
      {serverRefused && problems.length === 0 && (
        <p role="alert" className="text-sm text-red-600 wrap-anywhere">{serverRefused.message}</p>
      )}
      {notice && (
        <p role="status" className={cn('text-sm wrap-anywhere', noticeIsError ? 'text-red-600' : 'text-green-700')}>
          {notice}
        </p>
      )}

      <Preview result={result} hasHTML={html !== ''} />

      <ConfirmDialog
        open={confirmRevert}
        onOpenChange={setConfirmRevert}
        title="Revert to the default?"
        description="Your version is replaced by the one that ships with the server. It stays in the audit log."
        confirmLabel="Revert"
        isPending={revert.isPending}
        error={revert.error ? extractError(revert.error) : undefined}
        onConfirm={() => revert.mutate()}
      />
    </form>
  )
}

function Field({
  id, label, problems, hint, children,
}: {
  id: string
  label: string
  problems: EmailTemplateProblem[]
  hint: string
  children: (aria: { 'aria-invalid'?: boolean; 'aria-describedby': string }) => React.ReactNode
}) {
  const problem = problems[0]
  const errorId = `${id}-error`
  const hintId = `${id}-hint`
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      {children({
        ...(problem ? { 'aria-invalid': true } : {}),
        // The error first: it is the thing a screen reader should say.
        'aria-describedby': problem ? errorId : hintId,
      })}
      {problem ? (
        <p id={errorId} className="text-sm text-red-600 wrap-anywhere">
          {problem.line > 0 ? `Line ${problem.line}, column ${problem.col}: ` : ''}
          {problem.message}
        </p>
      ) : (
        <p id={hintId} className="text-xs text-gray-500 wrap-anywhere">{hint}</p>
      )}
    </div>
  )
}

// What the mail would look like, rendered with sample data. The text part is a
// <pre>: it holds what the server rendered and nothing is interpreted. The HTML
// part is markup an administrator wrote, on the origin staff sessions live on,
// so it goes in an iframe with an EMPTY sandbox (no scripts, no same-origin)
// and is never written into this page. A test pins that.
function Preview({ result, hasHTML }: { result: EmailTemplatePreview | null; hasHTML: boolean }) {
  return (
    <div>
      <p className="text-sm font-medium text-gray-900">Preview</p>
      <p className="text-xs text-gray-500">Sample data. The links are not real.</p>
      {result?.preview_error && <p className="mt-1 text-sm text-red-600 wrap-anywhere">{result.preview_error}</p>}
      {result?.valid && result.body !== undefined ? (
        <div className="mt-1 rounded-md border bg-gray-50 p-3 text-sm">
          <p className="font-medium wrap-anywhere">Subject: {result.subject}</p>
          <p className="mt-2 text-xs font-medium text-gray-700">Plain text part</p>
          <pre className="mt-1 whitespace-pre-wrap font-mono text-xs [overflow-wrap:anywhere]">{result.body}</pre>
        </div>
      ) : (
        <p className="mt-1 text-sm text-gray-500">The preview appears when the draft is valid.</p>
      )}
      {result?.valid && result.body !== undefined && (
        <div className="mt-3">
          <p className="text-xs font-medium text-gray-700">HTML part</p>
          {hasHTML && result.html ? (
            <>
              <p className="text-xs text-gray-500">
                Images from other sites do not load in this preview. Use Send test to see the real thing.
              </p>
              <iframe
                title="HTML preview"
                sandbox=""
                referrerPolicy="no-referrer"
                srcDoc={previewDocument(result.html)}
                className="mt-1 h-96 w-full rounded-md border bg-white"
              />
            </>
          ) : (
            <p className="mt-1 text-sm text-gray-500">This template has no HTML part: it is sent as plain text.</p>
          )}
        </div>
      )}
    </div>
  )
}
