import type { Options as SanitizeSchema } from 'rehype-sanitize'

// #443: the one list of what Markdown in ticket text may turn into. Three
// things are derived from it and nothing else restates it:
//
//   1. the rehype-sanitize schema (the second, independent check),
//   2. the renderer's allowedElements and component map (lib/markdownPipeline),
//   3. the editor's node and mark list (components/editor).
//
// markdownAllowList.test.ts fails when any of the three disagrees with it.
// Add an element here and those tests show you everywhere else it must go.

/** Fence languages we highlight. A fence in any other language is left plain. */
export const CODE_LANGUAGES = [
  'bash', 'powershell', 'json', 'yaml', 'xml', 'sql', 'javascript',
  'typescript', 'python', 'go', 'ini', 'diff', 'plaintext',
] as const

/** The only URL schemes a link may carry. Everything else is shown as text. */
export const ALLOWED_PROTOCOLS = ['http', 'https', 'mailto'] as const

/** highlight.js token classes. Closed on purpose: no free-form class names. */
export const HLJS_CLASS = /^hljs-[a-z_]+$/
const LANGUAGE_CLASS = new RegExp(`^language-(?:${CODE_LANGUAGES.join('|')})$`)

interface ElementRule {
  /** Attributes the Markdown may influence on this element. Nothing else gets through. */
  attributes: readonly string[]
  /** Editor node/mark names that produce this element. */
  node?: readonly string[]
  mark?: readonly string[]
}

const none: ElementRule['attributes'] = []

export const MARKDOWN_ALLOW_LIST = {
  elements: {
    p: { attributes: none, node: ['paragraph'] },
    // All six levels render as one small h4 (see lib/markdownPipeline): a
    // requester cannot post a page-sized "Session expired, sign in at..." banner.
    h1: { attributes: none, node: ['heading'] },
    h2: { attributes: none, node: ['heading'] },
    h3: { attributes: none, node: ['heading'] },
    h4: { attributes: none, node: ['heading'] },
    h5: { attributes: none, node: ['heading'] },
    h6: { attributes: none, node: ['heading'] },
    blockquote: { attributes: none, node: ['blockquote'] },
    ul: { attributes: none, node: ['bullet_list'] },
    ol: { attributes: ['start'], node: ['ordered_list'] },
    li: { attributes: none, node: ['list_item'] },
    pre: { attributes: none, node: ['code_block'] },
    hr: { attributes: none, node: ['hr'] },
    table: { attributes: none, node: ['table'] },
    thead: { attributes: none, node: ['table_header_row'] },
    tbody: { attributes: none },
    tr: { attributes: none, node: ['table_row'] },
    th: { attributes: none, node: ['table_header'] },
    td: { attributes: none, node: ['table_cell'] },
    em: { attributes: none, mark: ['emphasis'] },
    strong: { attributes: none, mark: ['strong'] },
    del: { attributes: none, mark: ['strike_through'] },
    code: { attributes: ['className'], mark: ['inlineCode'] },
    br: { attributes: none, node: ['hardbreak'] },
    a: { attributes: ['href'], mark: ['link'] },
    // Highlight tokens only; the class is checked against HLJS_CLASS.
    span: { attributes: ['className'] },
  },
  /**
   * Editor nodes that exist without a rendered element of their own, and why.
   * doc and text are structural. image stands in for the "[image: alt]" link
   * and is never an image element. html holds raw HTML as literal text.
   */
  editorOnlyNodes: ['doc', 'text', 'image', 'html'],
} as const satisfies {
  elements: Record<string, ElementRule>
  editorOnlyNodes: readonly string[]
}

export type AllowedElement = keyof typeof MARKDOWN_ALLOW_LIST.elements

export const ALLOWED_ELEMENTS = Object.keys(MARKDOWN_ALLOW_LIST.elements) as AllowedElement[]

/** Every node and mark name the editor schema may contain, derived from the list above. */
export const EDITOR_NODES: string[] = [
  ...new Set([
    ...Object.values<ElementRule>(MARKDOWN_ALLOW_LIST.elements).flatMap((e) => [...(e.node ?? [])]),
    ...MARKDOWN_ALLOW_LIST.editorOnlyNodes,
  ]),
]
export const EDITOR_MARKS: string[] = [
  ...new Set(Object.values<ElementRule>(MARKDOWN_ALLOW_LIST.elements).flatMap((e) => [...(e.mark ?? [])])),
]

// Attribute values the sanitizer lets through. href and start are checked by
// protocol/shape; className on code and span is checked against a closed list.
const attributeSpecs: Record<string, Array<string | [string, ...(string | RegExp)[]]>> = {}
for (const [tag, rule] of Object.entries<ElementRule>(MARKDOWN_ALLOW_LIST.elements)) {
  const specs = rule.attributes.map((a): string | [string, ...(string | RegExp)[]] => {
    if (a === 'className') return ['className', tag === 'code' ? LANGUAGE_CLASS : HLJS_CLASS]
    return a
  })
  if (specs.length) attributeSpecs[tag] = specs
}

export const markdownSanitizeSchema: SanitizeSchema = {
  tagNames: [...ALLOWED_ELEMENTS],
  attributes: attributeSpecs,
  protocols: { href: [...ALLOWED_PROTOCOLS] },
  // No id, no name: nothing the Markdown writes can collide with the page's own ids.
  clobber: [],
  clobberPrefix: '',
  strip: ['script', 'style'],
  // Nothing is required, ancestors are not restricted: the component map and
  // allowedElements already decide structure; this is the second check.
  required: {},
}

/** Bidi controls, zero-width and other invisible characters. Removed from link
 *  text and from anything shown as a link's destination: they can reorder or
 *  hide what a link claims to be. */
// The class deliberately lists combining and variation-selector characters: they are the point.
// eslint-disable-next-line no-misleading-character-class
export const INVISIBLE = /[\u00AD\u034F\u061C\u115F\u1160\u17B4\u17B5\u180B-\u180E\u200B-\u200F\u202A-\u202E\u2060-\u206F\u3164\uFE00-\uFE0F\uFEFF\uFFA0]/g

/**
 * The one URL check. Returns the URL only when it parses as an absolute
 * http(s) or mailto URL; relative URLs, fragments, javascript:, data: and
 * anything unparseable return null. A mailto: must name only real addresses
 * and carry no query at all (?bcc=, ?cc=, ?subject=, ?body= can add recipients
 * or write the message). Used by the renderer and by the editor's link button,
 * so the two cannot disagree.
 */
export function safeUrl(value: string): string | null {
  const u = parseAbsoluteUrl(value)
  if (!u) return null
  const proto = u.protocol.replace(/:$/, '')
  if (!(ALLOWED_PROTOCOLS as readonly string[]).includes(proto)) return null
  if (proto === 'mailto' && !mailtoAddresses(u)) return null
  return value
}

/**
 * The addresses a mailto: URL names, each with its domain in the form the
 * browser will really use (look-alike letters appear as xn--, a fullwidth dot
 * as the dot it is), or null when the URL is not acceptable: it has a query, an
 * address has no local part, or a domain is not a host.
 */
export function mailtoAddresses(u: URL): string[] | null {
  if (u.protocol !== 'mailto:' || u.href.includes('?')) return null
  let path: string
  try {
    path = decodeURIComponent(u.pathname)
  } catch {
    return null
  }
  // Real addresses need none of these, and each lets a recipient hide from the
  // label: a mail client splits on ';' as well as ',', a quote or a control
  // character (CR, LF, tab) can start a header or a quoted local part, and a
  // '%' left after decoding is a second layer of escaping the label cannot show.
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f"\\;%\s]/.test(path)) return null
  const out: string[] = []
  for (const addr of path.split(',')) {
    const at = addr.lastIndexOf('@')
    const local = addr.slice(0, at)
    // A second '@' in the local part (written as %40) would be read as the
    // separator by one parser and as part of the name by another.
    if (at < 1 || local.includes('@')) return null
    const host = parseAbsoluteUrl(`http://${addr.slice(at + 1)}`)
    if (!host || host.hostname === '' || host.pathname !== '/' || host.search || host.hash || host.username || host.password || host.port) {
      return null
    }
    out.push(`${local.replace(INVISIBLE, '')}@${host.hostname}`)
  }
  return out.length > 0 ? out : null
}

/** The only place this module family parses a URL. Null for anything that is not absolute. */
export function parseAbsoluteUrl(value: string): URL | null {
  try {
    return new URL(value)
  } catch {
    return null
  }
}
