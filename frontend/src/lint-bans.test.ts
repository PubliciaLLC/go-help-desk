/* eslint-disable no-restricted-syntax -- the probes below are the banned strings, as text */
import { describe, expect, it } from 'vitest'
import { ESLint } from 'eslint'

// #443: the renderer's guarantee is that every element comes from our own
// component map. Raw-HTML escape hatches are therefore banned by lint, and
// this test is what keeps the ban itself from being deleted unnoticed. Each
// probe is a way round the guarantee that a reviewer tried; each must be
// blocked by a restricted-* rule.

async function lint(code: string, filePath = 'src/components/Evil.tsx') {
  const eslint = new ESLint()
  const [result] = await eslint.lintText(code, { filePath })
  return result.messages.filter((m) => /restricted/.test(m.ruleId ?? '')).map((m) => m.ruleId)
}

const probes: Record<string, string> = {
  jsxAttr: 'export const A = ({h}: {h: string}) => <div dangerouslySetInnerHTML={{ __html: h }} />\n',
  objKey: "import { createElement } from 'react'\nexport const A = (h: string) => createElement('div', { dangerouslySetInnerHTML: { __html: h } })\n",
  stringKey: "import { createElement } from 'react'\nexport const A = (h: string) => createElement('div', { 'dangerouslySetInnerHTML': { __html: h } })\n",
  computedKey: "import { createElement } from 'react'\nconst k = 'dangerously' + 'SetInnerHTML'\nexport const A = (h: string) => createElement('div', { [k]: { __html: h } })\n",
  jsxSpread: "const p = { ['dangerously' + 'SetInnerHTML']: { __html: 'x' } }\nexport const A = () => <div {...p} />\n",
  innerHTML: 'export function f(el: HTMLElement, h: string) { el.innerHTML = h }\n',
  outerHTML: 'export function f(el: HTMLElement, h: string) { el.outerHTML = h }\n',
  insertAdjacent: "export function f(el: HTMLElement, h: string) { el.insertAdjacentHTML('beforeend', h) }\n",
  documentWrite: "export function f(h: string) { document.write(h) }\n",
  importRaw: "import rehypeRaw from 'rehype-raw'\nexport const x = rehypeRaw\n",
  dynImport: "export const load = () => import('rehype-raw')\n",
  reexport: "export { default } from 'rehype-raw'\n",
  requireRaw: "const r = require('rehype-raw')\nexport default r\n",
  hastRaw: "import { raw } from 'hast-util-raw'\nexport const x = raw\n",
  hastRawDyn: "export const load = () => import('hast-util-raw')\n",
  allowDanger: "import ReactMarkdown from 'react-markdown'\nexport const A = ({t}:{t:string}) => <ReactMarkdown remarkRehypeOptions={{ allowDangerousHtml: true }}>{t}</ReactMarkdown>\n",
  allowDangerString: "export const o = { 'allowDangerousHtml': true }\n",
}

describe('lint bans', () => {
  it.each(Object.entries(probes))('%s is refused', async (_name, code) => {
    expect((await lint(code)).length).toBeGreaterThan(0)
  })

  it('ordinary JSX and ordinary properties are not flagged', async () => {
    expect(await lint('export const A = () => <div className="x" title="t" />\nexport const t = document.title\n')).toEqual([])
  })

  it('test files may read innerHTML (a test inspecting the DOM), but not the other bans', async () => {
    expect(await lint('export const h = (el: HTMLElement) => el.innerHTML\n', 'src/x.test.tsx')).toEqual([])
    expect((await lint("import r from 'rehype-raw'\nexport default r\n", 'src/x.test.tsx')).length).toBeGreaterThan(0)
  })
})
