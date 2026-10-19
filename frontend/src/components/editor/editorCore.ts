import { Editor, defaultValueCtx, editorViewCtx, editorViewOptionsCtx, rootCtx, serializerCtx } from '@milkdown/kit/core'
import {
  commonmark,
  imageSchema,
  insertImageInputRule,
  listItemKeymap,
} from '@milkdown/kit/preset/commonmark'
import {
  extendListItemSchemaForTask,
  footnoteDefinitionSchema,
  footnoteReferenceSchema,
  gfm,
  tableKeymap,
  wrapInTaskListInputRule,
} from '@milkdown/kit/preset/gfm'
import { history } from '@milkdown/kit/plugin/history'
import { clipboard } from '@milkdown/kit/plugin/clipboard'
import { $prose, $remark, getMarkdown, replaceAll } from '@milkdown/kit/utils'
import { Plugin } from '@milkdown/kit/prose/state'
import { visit } from 'unist-util-visit'
import type { Root } from 'mdast'
import type { MilkdownPlugin } from '@milkdown/kit/ctx'

// #443: the Milkdown editor, built to match lib/markdownAllowList. It writes
// Markdown; what staff see afterwards goes through the strict renderer.

// What the presets contain that the allow-list does not: task-list items,
// footnotes, and the image input rule. (The table column-resizing plugin is
// exported by the GFM preset but not part of its default list.) Dropped by
// identity so a preset upgrade that renames something fails loudly in
// editorCore.test.ts instead of quietly re-adding it.
const flat = (...parts: unknown[]): unknown[] => parts.flat(Infinity)
const dropped = new Set<unknown>(
  flat(extendListItemSchemaForTask, footnoteDefinitionSchema, footnoteReferenceSchema, wrapInTaskListInputRule, insertImageInputRule),
)

// An image is never a live <img>. A plain-text paste is parsed as Markdown and
// then serialised to DOM in the live document, so a stock image node would make
// the browser fetch its source. Here it is a span that reads "[image: alt]";
// nothing parses an <img> into it either.
const imageAsText = imageSchema.extendSchema((prev) => (ctx) => ({
  ...prev(ctx),
  parseDOM: [],
  toDOM: (node) => ['span', { class: 'md-image' }, `[image: ${String(node.attrs.alt ?? '')}]`],
}))

function without(plugins: MilkdownPlugin[]): MilkdownPlugin[] {
  return plugins.filter((p) => !dropped.has(p))
}

// remark-gfm parses footnotes, and the editor has no node for them, which
// would make Milkdown throw on a paste or a saved text that contains one. They
// become the literal text they were written as. (The renderer drops the
// footnote machinery in the same spirit: relative links are refused.)
const footnotesAsText = $remark('footnotesAsText', () => () => (tree: Root) => {
  visit(tree, (node, index, parent) => {
    if (!parent || index === undefined) return
    if (node.type === 'footnoteReference') {
      parent.children[index] = { type: 'text', value: `[^${node.label ?? node.identifier}]` }
    } else if (node.type === 'footnoteDefinition') {
      const first = node.children[0]
      const prefix = { type: 'text' as const, value: `[^${node.label ?? node.identifier}]: ` }
      const lead =
        first?.type === 'paragraph'
          ? [{ ...first, children: [prefix, ...first.children] }, ...node.children.slice(1)]
          : [{ type: 'paragraph' as const, children: [prefix] }, ...node.children]
      parent.children.splice(index, 1, ...lead)
    }
  })
})

// Milkdown's listener plugin debounces by 200 ms, so a form submitted straight
// after the last keystroke would send the text from before it. This reports
// the Markdown on every document change instead, synchronously.
function reportChanges(onMarkdown: (markdown: string) => void, onSelection: () => void) {
  return $prose(
    (ctx) =>
      new Plugin({
        view: () => ({
          update: (view, prev) => {
            if (!view.state.doc.eq(prev.doc)) onMarkdown(ctx.get(serializerCtx)(view.state.doc))
            if (!view.state.selection.eq(prev.selection) || view.state.storedMarks !== prev.storedMarks) onSelection()
          },
        }),
      }),
  )
}

export const editorPlugins: MilkdownPlugin[] = [
  ...without(commonmark),
  ...without(gfm),
  imageAsText,
  footnotesAsText,
  history,
  clipboard,
].flat() as MilkdownPlugin[]

export interface EditorOptions {
  root: HTMLElement
  initial: string
  labelId: string
  placeholder?: string
  editable: () => boolean
  onMarkdown: (markdown: string) => void
  onSelection: () => void
}

export async function createEditor(opts: EditorOptions): Promise<Editor> {
  const editor = Editor.make()
    .config((ctx) => {
      ctx.set(rootCtx, opts.root)
      ctx.set(defaultValueCtx, opts.initial)
      ctx.update(editorViewOptionsCtx, (prev) => ({
        ...prev,
        editable: opts.editable,
        attributes: {
          ...(prev.attributes as Record<string, string> | undefined),
          role: 'textbox',
          'aria-multiline': 'true',
          'aria-labelledby': opts.labelId,
          ...(opts.placeholder ? { 'aria-placeholder': opts.placeholder } : {}),
          class: 'ProseMirror md-editor-surface',
        },
      }))
      // Tab must always move focus out of the editor (WCAG 2.1.2). The presets
      // bind it to sink/lift a list item and to move between table cells;
      // those keep Mod-] and Mod-[ only.
      ctx.update(listItemKeymap.key, (prev) => ({
        ...prev,
        SinkListItem: { ...prev.SinkListItem, shortcuts: 'Mod-]' },
        LiftListItem: { ...prev.LiftListItem, shortcuts: 'Mod-[' },
      }))
      ctx.update(tableKeymap.key, (prev) => ({
        ...prev,
        NextCell: { ...prev.NextCell, shortcuts: 'Mod-]' },
        PrevCell: { ...prev.PrevCell, shortcuts: 'Mod-[' },
      }))
    })
    .use(editorPlugins)
    .use(reportChanges(opts.onMarkdown, opts.onSelection))
  await editor.create()
  return editor
}

export function setMarkdown(editor: Editor, markdown: string) {
  editor.action(replaceAll(markdown))
}

export function markdownOf(editor: Editor): string {
  return editor.action(getMarkdown())
}

export function viewOf(editor: Editor) {
  return editor.ctx.get(editorViewCtx)
}
