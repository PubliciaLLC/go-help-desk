import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkRehype from 'remark-rehype'
import rehypeSanitize from 'rehype-sanitize'
import { createLowlight } from 'lowlight'
import { visit } from 'unist-util-visit'
import type { Element, ElementContent, Root as HastRoot, RootContent, Text } from 'hast'
import type { Root as MdastRoot, Table } from 'mdast'
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
import { ALLOWED_ELEMENTS, INVISIBLE, markdownSanitizeSchema, safeUrl } from './markdownAllowList'

// #443: Markdown text to a sanitized hast tree. No React and no DOM in here:
// this runs in a Web Worker (markdown.worker.ts) under a time budget, because
// parsing is not linear in the size of the input. What comes out is data;
// lib/markdownComponents.tsx turns it into elements on the page, from our own
// component map, after checking it again.

/** A table's cells are its header width times its rows, padding included. */
export const MAX_TABLE_CELLS = 5_000
/** Elements and text nodes in the result, so the page cannot be asked for millions. */
export const MAX_NODES = 40_000

/** The text is too heavy to format. The caller shows it as written. */
export class FormatRefused extends Error {}

/** Raw HTML is text. Block HTML becomes a paragraph of its characters, inline
 *  HTML the characters themselves; nothing here ever asks for raw HTML to be
 *  turned into elements, so `<server01>` and `<b>x</b>` read as written. */
function htmlToText() {
  const flow = new Set(['root', 'blockquote', 'listItem'])
  return (tree: MdastRoot) => {
    visit(tree, 'html', (node, index, parent) => {
      if (!parent || index === undefined) return
      const text = { type: 'text' as const, value: node.value }
      parent.children[index] = (flow.has(parent.type) ? { type: 'paragraph', children: [text] } : text) as never
    })
  }
}

/** A table is its header width times its rows once padded, which is what
 *  turned a 50 KB reply into millions of cells. Refused before it is built. */
function limitTables() {
  return (tree: MdastRoot) => {
    let cells = 0
    visit(tree, 'table', (table: Table) => {
      const columns = Math.max(table.align?.length ?? 0, table.children[0]?.children.length ?? 0)
      cells += columns * table.children.length
      if (cells > MAX_TABLE_CELLS) throw new FormatRefused('table too large')
    })
  }
}

/** An image is never loaded: it becomes a link to its source, which then goes
 *  through the same href rules as any link. (Sanitize alone would drop the
 *  image and its alt text, leaving an empty paragraph.) An image inside a link
 *  becomes plain text: a link cannot contain a link. */
function imagesToLinks() {
  return (tree: HastRoot) => {
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
function stripInvisibleInLinks() {
  return (tree: HastRoot) => {
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
  return (tree: HastRoot) => {
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

const allowed = new Set<string>(ALLOWED_ELEMENTS)

/** The last pass over what sanitize returned: elements outside the list are
 *  unwrapped (their children stay), an href that fails safeUrl is dropped,
 *  positions are removed (the tree crosses a thread boundary), and the size is
 *  counted. */
function finalize(root: HastRoot): HastRoot {
  let nodes = 0
  const clean = (children: RootContent[]): RootContent[] => {
    const out: RootContent[] = []
    for (const child of children) {
      if (++nodes > MAX_NODES) throw new FormatRefused('result too large')
      delete child.position
      if (child.type === 'text') {
        out.push(child)
      } else if (child.type === 'element') {
        child.children = clean(child.children) as ElementContent[]
        if (!allowed.has(child.tagName)) {
          out.push(...child.children)
          continue
        }
        if (child.tagName === 'a') {
          const href = child.properties?.href
          if (typeof href !== 'string' || safeUrl(href) === null) delete child.properties.href
        }
        out.push(child)
      }
      // Comments, doctypes and anything else are dropped.
    }
    return out
  }
  return { type: 'root', children: clean(root.children) as HastRoot['children'] }
}

const processor = unified()
  .use(remarkParse)
  .use(remarkGfm)
  .use(htmlToText)
  .use(limitTables)
  .use(remarkRehype)
  .use(imagesToLinks)
  .use(stripInvisibleInLinks)
  .use(highlightFences)
  .use(rehypeSanitize, markdownSanitizeSchema)

/** Markdown to a sanitized hast tree, or FormatRefused. Synchronous and
 *  unbounded in time: call it from the worker, never from the page. */
export function markdownToHast(text: string): HastRoot {
  const tree = processor.runSync(processor.parse(text)) as HastRoot
  return finalize(tree)
}
