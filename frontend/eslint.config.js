import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

const banHtml = 'dangerouslySetInnerHTML is banned: render through React elements (see TicketText, #443).'
const banRaw = 'Raw HTML must never become elements (#443): it is shown as text. rehype-raw, hast-util-raw and allowDangerousHtml are banned.'
const banDom = 'Putting an HTML string into the DOM is banned (#443): create elements with React.'
const rawModules = '/^(rehype-raw|hast-util-raw)(\\/|$)/'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    // #443: ticket text is rendered only through TicketText, whose every
    // element comes from our own component map. These are the ways round that,
    // so they are banned outright rather than reviewed case by case. Each has a
    // probe in src/lint-bans.test.ts.
    rules: {
      'no-restricted-syntax': [
        'error',
        // The React escape hatch, however it is spelled: a JSX attribute, an
        // object key (plain, quoted or computed), or any string or template that
        // builds the name.
        { selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']", message: banHtml },
        { selector: "Property[key.name='dangerouslySetInnerHTML']", message: banHtml },
        { selector: "Literal[value=/dangerously/i]", message: banHtml },
        { selector: "TemplateElement[value.raw=/dangerously/i]", message: banHtml },
        // Letting a Markdown pipeline hand raw HTML on as elements.
        { selector: "Property[key.name='allowDangerousHtml']", message: banRaw },
        { selector: "Property[key.value='allowDangerousHtml']", message: banRaw },
        { selector: "JSXAttribute[name.name='allowDangerousHtml']", message: banRaw },
        // rehype-raw and hast-util-raw by any door a static import rule does not
        // cover: import(), require(), and re-exports.
        { selector: `ImportExpression[source.value=${rawModules}]`, message: banRaw },
        { selector: `CallExpression[callee.name='require'][arguments.0.value=${rawModules}]`, message: banRaw },
        { selector: `ExportNamedDeclaration[source.value=${rawModules}]`, message: banRaw },
        { selector: `ExportAllDeclaration[source.value=${rawModules}]`, message: banRaw },
      ],
      'no-restricted-imports': [
        'error',
        {
          patterns: [{ group: ['rehype-raw', 'rehype-raw/*', 'hast-util-raw', 'hast-util-raw/*'], message: banRaw }],
        },
      ],
      // Assigning or calling an HTML string into the DOM.
      'no-restricted-properties': [
        'error',
        { property: 'innerHTML', message: banDom },
        { property: 'outerHTML', message: banDom },
        { property: 'insertAdjacentHTML', message: banDom },
        { object: 'document', property: 'write', message: banDom },
        { object: 'document', property: 'writeln', message: banDom },
      ],
    },
  },
  // A test that inspects the DOM may read innerHTML. Nothing else is relaxed.
  {
    files: ['**/*.test.{ts,tsx}', 'e2e/**/*.ts'],
    rules: { 'no-restricted-properties': 'off' },
  },
])
