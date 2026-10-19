import type { ComponentProps, ReactNode } from 'react'
import { Children, Fragment, isValidElement } from 'react'
import { jsx, jsxs } from 'react/jsx-runtime'
import { toJsxRuntime } from 'hast-util-to-jsx-runtime'
import type { Components } from 'hast-util-to-jsx-runtime'
import type { Root } from 'hast'
import {
  ALLOWED_ELEMENTS,
  CODE_LANGUAGES,
  HLJS_CLASS,
  INVISIBLE,
  mailtoAddresses,
  parseAbsoluteUrl,
  safeUrl,
} from './markdownAllowList'

// #443: the page half of the renderer. The tree arrives from the worker already
// sanitized; this checks it once more and then creates every element from the
// component map below. Every element is ours, every piece of user text is a
// React text node, and the only attribute that carries user data is href,
// checked by safeUrl. No component spreads ...props.

const allowedTags = new Set<string>(ALLOWED_ELEMENTS)

/** Throws if the tree holds anything the allow-list does not: a tag outside it,
 *  a node that is not an element or text, an attribute we never read. The
 *  worker sanitizes; this is the main thread not trusting a message. */
function assertAllowed(node: Root | Root['children'][number]): void {
  if (node.type === 'text') return
  if (node.type !== 'root' && node.type !== 'element') throw new Error('unexpected node')
  if (node.type === 'element') {
    if (!allowedTags.has(node.tagName)) throw new Error(`unexpected element ${node.tagName}`)
    for (const key of Object.keys(node.properties ?? {})) {
      if (key !== 'href' && key !== 'start' && key !== 'className') throw new Error(`unexpected attribute ${key}`)
    }
  }
  for (const child of node.children) assertAllowed(child)
}

// Plain text, in React's hands: the children below are text nodes React escapes.
function flatten(children: ReactNode): string {
  return Children.toArray(children)
    .map((c) => {
      if (typeof c === 'string' || typeof c === 'number') return String(c)
      if (isValidElement<{ children?: ReactNode }>(c)) return flatten(c.props.children)
      return ''
    })
    .join('')
}

/** True only when the visible text cannot mislead: it is exactly the URL being
 *  linked (or the bare `www.` form GFM autolinks to http://www...), it is plain
 *  ASCII (no look-alike letters), and the URL has no user:password@ part (which
 *  makes `https://bank.example@evil.example` read as bank.example). Anything
 *  else gets the real host beside it. No case folding, no dropped slash, no
 *  protocol swap: each of those is a way to look like a URL you are not. */
function textIsExactDestination(text: string, href: string): boolean {
  if (!/^[\x21-\x7e]+$/.test(text)) return false
  const u = parseAbsoluteUrl(href)
  if (!u || u.username || u.password) return false
  if (text === href) return true
  if (u.protocol === 'mailto:') return text === href.slice('mailto:'.length)
  return /^www\./.test(text) && href === `http://${text}`
}

/** What a link really goes to, shown beside link text that does not say it:
 *  the host (look-alike international domains in their xn-- spelling), or the
 *  address(es) of a mailto with each domain in the same form. */
function describeTarget(href: string): string {
  const u = parseAbsoluteUrl(href)
  if (!u) return ''
  if (u.protocol === 'mailto:') return (mailtoAddresses(u) ?? []).join(', ')
  return u.host.replace(INVISIBLE, '')
}

/** A link to one of this help desk's own attachment routes. Its label shows
 *  this site's own host, and a click skips the "content looks like..." and
 *  reputation warnings the attachments list shows, so a requester could hide
 *  their own upload behind friendly text. It is not a link. */
function isOwnAttachmentRoute(href: string): boolean {
  const u = parseAbsoluteUrl(href)
  if (!u || typeof window === 'undefined' || u.origin !== window.location.origin) return false
  const parts = u.pathname.toLowerCase().split('/')
  return parts[1] === 'api' && parts.slice(2).some((seg) => seg === 'attachment' || seg === 'attachments')
}

type Tag<K extends keyof React.JSX.IntrinsicElements> = ComponentProps<K> & { node?: unknown }

function renderLink({ href, children }: Tag<'a'>) {
  // The tree has been through safeUrl already; this is a second look at the same rule.
  const safe = typeof href === 'string' ? safeUrl(href) : null
  if (!safe) return <span>{children}</span>
  if (isOwnAttachmentRoute(safe)) {
    return (
      <span>
        {children} <span className="text-xs text-gray-500">(attachment: open it from the attachments list)</span>
      </span>
    )
  }
  const text = flatten(children)
  return (
    <>
      <a
        href={safe}
        target="_blank"
        rel="noopener noreferrer nofollow ugc"
        className="text-blue-600 underline wrap-anywhere"
      >
        {children}
      </a>
      {!textIsExactDestination(text, safe) && (
        // dir=ltr and isolate: the label reads left to right and cannot be
        // reordered by the right-to-left text before it.
        <span dir="ltr" className="ml-1 text-xs text-gray-500 wrap-anywhere [unicode-bidi:isolate]">
          ({describeTarget(safe)})
        </span>
      )}
    </>
  )
}

const heading = ({ children }: Tag<'h1'>) => <h4 className="text-sm font-semibold">{children}</h4>

// className, rel, target and tabIndex below are ours, never read from the Markdown.
export const markdownComponents: Components = {
  p: ({ children }) => <p className="whitespace-pre-line wrap-anywhere">{children}</p>,
  h1: heading,
  h2: heading,
  h3: heading,
  h4: heading,
  h5: heading,
  h6: heading,
  blockquote: ({ children }) => (
    <blockquote className="border-l-4 border-gray-300 pl-3 text-gray-600">{children}</blockquote>
  ),
  ul: ({ children }) => <ul className="list-disc space-y-1 pl-5">{children}</ul>,
  ol: ({ start, children }) => (
    <ol className="list-decimal space-y-1 pl-5" start={typeof start === 'number' ? start : undefined}>
      {children}
    </ol>
  ),
  li: ({ children }) => <li className="whitespace-pre-line wrap-anywhere">{children}</li>,
  pre: ({ children }) => (
    <pre className="max-w-full overflow-x-auto rounded bg-gray-100 p-2 text-xs" tabIndex={0}>
      {children}
    </pre>
  ),
  hr: () => <hr className="my-2 border-gray-200" />,
  table: ({ children }) => (
    <div className="max-w-full overflow-x-auto" tabIndex={0}>
      <table className="border-collapse text-sm">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead>{children}</thead>,
  tbody: ({ children }) => <tbody>{children}</tbody>,
  tr: ({ children }) => <tr>{children}</tr>,
  th: ({ children }) => (
    <th className="border border-gray-300 px-2 py-1 text-left font-semibold break-words">{children}</th>
  ),
  td: ({ children }) => <td className="border border-gray-300 px-2 py-1 break-words">{children}</td>,
  em: ({ children }) => <em>{children}</em>,
  strong: ({ children }) => <strong>{children}</strong>,
  del: ({ children }) => <del>{children}</del>,
  br: () => <br />,
  code: ({ className, children }) => {
    // Only a language we know survives as a class; anything else is dropped.
    const lang = String(className ?? '')
      .split(/\s+/)
      .find((c) => c.startsWith('language-') && (CODE_LANGUAGES as readonly string[]).includes(c.slice(9)))
    return <code className={lang ? `${lang} font-mono` : 'font-mono'}>{children}</code>
  },
  a: renderLink,
  span: ({ className, children }) => {
    const token = String(className ?? '')
      .split(/\s+/)
      .filter((c) => HLJS_CLASS.test(c))
      .join(' ')
    return <span className={token || undefined}>{children}</span>
  },
}

/** The sanitized tree as React elements, or a throw if it is not what the
 *  worker is supposed to send. */
export function renderHast(tree: Root): ReactNode {
  assertAllowed(tree)
  return toJsxRuntime(tree, {
    Fragment,
    jsx,
    jsxs,
    components: markdownComponents,
    passNode: false,
    passKeys: true,
    elementAttributeNameCase: 'react',
  })
}
