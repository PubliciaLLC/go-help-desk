import { describe, expect, it } from 'vitest'
import { ESLint } from 'eslint'

// #443: the renderer's guarantee is that every element comes from our own
// component map. Raw-HTML escape hatches are therefore banned by lint, and
// this test is what keeps the ban itself from being deleted unnoticed.

async function lint(code: string, filePath = 'src/components/Evil.tsx') {
  const eslint = new ESLint()
  const [result] = await eslint.lintText(code, { filePath })
  return result.messages
}

describe('lint bans', () => {
  it('dangerouslySetInnerHTML is refused', async () => {
    const msgs = await lint('export const A = ({h}: {h: string}) => <div dangerouslySetInnerHTML={{ __html: h }} />\n')
    expect(msgs.map((m) => m.ruleId)).toContain('no-restricted-syntax')
  })

  it('importing rehype-raw is refused', async () => {
    const msgs = await lint("import rehypeRaw from 'rehype-raw'\nexport const x = rehypeRaw\n")
    expect(msgs.map((m) => m.ruleId)).toContain('no-restricted-imports')
  })

  it('ordinary JSX is not flagged by the bans', async () => {
    const msgs = await lint('export const A = () => <div className="x" />\n')
    expect(msgs.filter((m) => m.ruleId === 'no-restricted-syntax' || m.ruleId === 'no-restricted-imports')).toEqual([])
  })
})
