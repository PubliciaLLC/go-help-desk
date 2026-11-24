import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { useState } from 'react'
import { Select } from '@/components/ui/select'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

/**
 * What the server accepts when the operator has never set a list, from
 * admin.DefaultAllowedTypes().
 *
 * Duplicated here because the API does not send it: an instance that has never
 * set the list has no row, so the field would otherwise be empty — and an
 * empty list is itself a valid setting meaning "no attachments at all", which
 * is the opposite of what an unset instance does. Display only. An untouched
 * list is never written back, so an instance that never chose one keeps
 * following whatever the server's default becomes.
 */
const DEFAULT_ALLOWED_TYPES = ['.pdf', '.docx', '.xlsx', '.txt', '.log', '.jpg', '.jpeg', '.png', '.bmp']

/** Extensions out of the textarea: whitespace or commas, in any combination. */
function parseAllowedTypes(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

export function UploadsSection({ draft }: { draft: SettingsDraft }) {
  const { has, str, strArr, setStr, setStrArr } = draft
  const typesSet = has('attachment_allowed_types')
  const [typesText, setTypesText] = useState(
    (typesSet ? strArr('attachment_allowed_types') : DEFAULT_ALLOWED_TYPES).join('\n'),
  )
  const mismatchHandling = str('attachment_mismatch_handling') || 'refuse'
  function editTypes(v: string) {
    // Lowercased as it is typed, the way the tracking prefix is uppercased:
    // the server compares against strings.ToLower(filepath.Ext(name)) and
    // refuses an uppercase entry outright, so ".PDF" is only ever a refusal
    // waiting to happen.
    const lower = v.toLowerCase()
    setTypesText(lower)
    setStrArr('attachment_allowed_types', parseAllowedTypes(lower))
  }
  return (
    <Section title="Uploads">
      <div className="space-y-2 px-5 py-4">
        <label htmlFor="attachment-allowed-types" className="block text-sm font-medium text-gray-900">
          Allowed file types
        </label>
        <div className="text-sm text-gray-500">
          One extension per line, with the leading dot — <span className="font-mono">.pdf</span>. Anything
          else is refused at upload, before the file is read. An empty list means this instance accepts no
          attachments at all.
        </div>
        <textarea
          id="attachment-allowed-types"
          rows={9}
          className="w-full max-w-xs rounded border border-gray-300 p-2 font-mono text-sm"
          value={typesText}
          onChange={(e) => editTypes(e.target.value)}
        />
        {!typesSet && (
          <p className="text-xs text-gray-500">
            This instance has never set a list, so the built-in default is shown. It is saved only if you
            change it.
          </p>
        )}
      </div>

      {/* Here rather than beside the scanner settings, for the same reason
          the infected-handling select is beside those: a control belongs
          with the thing that produces the finding it acts on. This one acts
          on the content check every upload goes through, and the list above
          is what decides which of its findings are wrapped — a file is
          wrapped when what it turned out to be is not something this
          instance accepts.

          The wording deliberately shares the decision's shape with "When a
          scan finds malware" and none of its vocabulary. Nothing found
          anything wrong with a mislabelled file; it is named wrongly, the
          archive carries no password, and borrowing the other tier's words
          would say something false about what is stored and make both tiers
          mean less. */}
      <SettingRow
        label="When a file’s content contradicts its name"
        description="Every upload is compared against the extension it claims. A file that turns out to be a type this instance does not accept is either turned away or stored wrapped in a ZIP named suspicious-<crc32>.zip and flagged on the ticket. One whose real type is on the list above is flagged and stored under its own name whichever you choose."
      >
        <Select
          className="w-72 max-w-full"
          aria-label="When a file’s content contradicts its name"
          value={mismatchHandling}
          onChange={(e) => setStr('attachment_mismatch_handling', e.target.value)}
        >
          {/* Two values, and "quarantine" is not one of them however much it
              looks like it should be: it is the other setting's word, and
              the server refuses it rather than reading it as "wrap". */}
          <option value="refuse">Refuse the upload</option>
          <option value="wrap">Store it in a renamed archive</option>
        </Select>
      </SettingRow>
    </Section>
  )
}
