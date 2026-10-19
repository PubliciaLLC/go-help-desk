import type { ComponentProps, ReactNode } from 'react'
import { Children, isValidElement } from 'react'
import type { Options as ReactMarkdownOptions } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeSanitize from 'rehype-sanitize'
import { createLowlight } from 'lowlight'
import { visit } from 'unist-util-visit'
import type { Element, ElementContent, Root, Text } from 'hast'
import bash from 'highlight.js/lib/languages/bash'
import powershell from 'highlight.js/lib/languages/powershell'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import xml from 'highlight.js/lib/languages/xml'
import sql from 'highlight.js/lib/languages/sql'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import diff from 'highlight.js/lib/languages/diff'
import plaintext from 'highlight.js/lib/languages/plaintext'
import {
  ALLOWED_ELEMENTS,
  CODE_LANGUAGES,
  HLJS_CLASS,
  markdownSanitizeSchema,
  parseAbsoluteUrl,
  safeUrl,
} from './markdownAllowList'

// #443: the renderer. The guarantee is how the page is built, not filtering:
// every element is created by React from the component map below, every piece
// of user text is a React text node, and the only attribute that carries user
// data is href, checked by safeUrl. rehype-sanitize is a second, independent
// check. The raw-HTML escape hatch and rehype-raw are banned by lint.

/** Raw HTML in the Markdown becomes text. Must run BEFORE sanitize, which
 *  would otherwise delete `<server01>` and strip `<b>x</b>` to `x`: cutting
 *  text the requester wrote. (skipHtml would delete it just the same.) */
function rawToText() {
  return (tree: Root) => {
    visit(tree, 'raw' as never, (node: { type: string }) => {
      node.type = 'text'
    })
  }
}

/** An image is never loaded: it becomes a link to its source, which then goes
 *  through the same href rules as any link. (Sanitize alone would drop the
 *  image and its alt text, leaving an empty paragraph.) An image inside a link
 *  becomes plain text: a link cannot contain a link. */
function imagesToLinks() {
  return (tree: Root) => {
    const insideLink = new Set<Element>()
    visit(tree, 'element', (a) => {
      if (a.tagName !== 'a') return
      visit(a, 'element', (child) => {
        if (child.tagName === 'img') insideLink.add(child)
      })
    })
    visit(tree, 'element', (node, index, parent) => {
      if (node.tagName !== 'img' || !parent || index === undefined) return
      const alt = typeof node.properties?.alt === 'string' ? node.properties.alt : ''
      const src = typeof node.properties?.src === 'string' ? node.properties.src : ''
      const label: Text = { type: 'text', value: `[image: ${alt}]` }
      parent.children[index] =
        src && !insideLink.has(node)
          ? { type: 'element', tagName: 'a', properties: { href: src }, children: [label] }
          : label
    })
  }
}

/** Bidi controls and zero-width characters in link text can reorder or hide
 *  what a link claims to be. They are removed from text inside links. */
// The class deliberately lists combining and variation-selector characters: they are the point.
// eslint-disable-next-line no-misleading-character-class
const INVISIBLE = /[\u00AD\u034F\u061C\u115F\u1160\u17B4\u17B5\u180B-\u180E\u200B-\u200F\u202A-\u202E\u2060-\u206F\u3164\uFE00-\uFE0F\uFEFF\uFFA0]/g
function stripInvisibleInLinks() {
  return (tree: Root) => {
    visit(tree, 'element', (a) => {
      if (a.tagName !== 'a') return
      visit(a, 'text', (t: Text) => {
        t.value = t.value.replace(INVISIBLE, '')
      })
    })
  }
}

// lowlight directly, not rehype-highlight: that always bundles its 37-language
// "common" set (+65 KB gzipped) however few languages it is asked for. This is
// 13 languages and the same tree. The output is hast nodes, never an HTML string.
const lowlight = createLowlight({
  bash, powershell, json, yaml, xml, sql, javascript, typescript, python, go, ini, diff, plaintext,
})

function textOf(node: Element): string {
  return node.children.map((c) => (c.type === 'text' ? c.value : c.type === 'element' ? textOf(c) : '')).join('')
}

/** Highlights fenced code whose language is registered. A fence with no
 *  language is not guessed at; an unknown language is left alone. */
function highlightFences() {
  return (tree: Root) => {
    visit(tree, 'element', (node, _index, parent) => {
      if (node.tagName !== 'code' || parent?.type !== 'element' || (parent as Element).tagName !== 'pre') return
      const classes = Array.isArray(node.properties?.className) ? node.properties.className : []
      const lang = classes
        .map(String)
        .find((c) => c.startsWith('language-'))
        ?.slice('language-'.length)
      if (!lang || !lowlight.registered(lang)) return
      node.children = lowlight.highlight(lang, textOf(node)).children as ElementContent[]
    })
  }
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

/** The host a link really goes to, shown beside link text that does not say
 *  it. new URL gives look-alike international domains in their xn-- spelling. */
function describeTarget(href: string): string {
  const u = parseAbsoluteUrl(href)
  if (!u) return ''
  if (u.protocol !== 'mailto:') return u.host
  try {
    return decodeURIComponent(u.pathname)
  } catch {
    return u.pathname
  }
}

type Tag<K extends keyof React.JSX.IntrinsicElements> = ComponentProps<K> & { node?: unknown }

function renderLink({ href, children }: Tag<'a'>) {
  // urlTransform has already run; this is a second look at the same rule.
  const safe = typeof href === 'string' ? safeUrl(href) : null
  if (!safe) return <span>{children}</span>
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
        <span className="ml-1 text-xs text-gray-500 wrap-anywhere">({describeTarget(safe)})</span>
      )}
    </>
  )
}

const heading = ({ children }: Tag<'h1'>) => <h4 className="text-sm font-semibold">{children}</h4>

// No component spreads ...props: each sets only the attributes it names.
// className, rel, target and tabIndex below are ours, never read from the Markdown.
const components: NonNullable<ReactMarkdownOptions['components']> = {
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

export const markdownProps: Pick<
  ReactMarkdownOptions,
  'remarkPlugins' | 'rehypePlugins' | 'allowedElements' | 'unwrapDisallowed' | 'urlTransform' | 'components'
> = {
  remarkPlugins: [remarkGfm],
  // Order matters (each point measured): raw to text and images to links
  // before sanitize; highlight before sanitize so it checks what highlighting made.
  rehypePlugins: [rawToText, imagesToLinks, stripInvisibleInLinks, highlightFences, [rehypeSanitize, markdownSanitizeSchema]],
  allowedElements: [...ALLOWED_ELEMENTS],
  unwrapDisallowed: true,
  urlTransform: (value) => safeUrl(value),
  components,
}
