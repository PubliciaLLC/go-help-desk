import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { Select } from '@/components/ui/select'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

// The four per-user staff-email switches live on each person's profile (#441,
// and SLA alerts from #670). These override them for everyone: leave it to each
// person (the default), force it on, or force it off.
// Exported so the mirror test can pin these names to the profile labels (#674).
// eslint-disable-next-line react-refresh/only-export-components
export const STAFF_EMAIL_SWITCHES:{ key: string; name: string; help: string }[] = [
  {
    key: 'staff_email_assigned_override',
    name: 'Tickets assigned to me',
    help: 'A ticket is assigned to a person.',
  },
  {
    key: 'staff_email_activity_override',
    name: "Activity on tickets I'm assigned to or follow",
    help: 'A requester replies on a ticket assigned to a person, or it is reopened; or a ticket a person follows is assigned, gets a reply or is reopened.',
  },
  {
    key: 'staff_email_group_override',
    name: "My groups' tickets",
    help: 'A ticket is assigned to a group, a requester replies on a group ticket nobody has taken yet, or a group ticket nobody has taken yet is reopened. Goes to every member of the group.',
  },
  {
    key: 'staff_email_sla_override',
    name: 'SLA alerts',
    help: 'A target on a ticket assigned to a person, on a group ticket nobody has taken yet, or on a ticket a person follows is breached or at risk. Needs SLA alerts turned on under Features.',
  },
]

export function StaffEmailSection({ draft }: { draft: SettingsDraft }) {
  const { str, setStr } = draft
  return (
    <Section title="Staff email about tickets">
      <div className="px-5 py-4 text-sm text-gray-500">
        Staff are emailed when a ticket is assigned to them or their group, when a requester replies on a ticket
        that is theirs, and when an SLA target on one of their tickets is breached or at risk. Each person chooses on their profile; their own switches start on. Nobody is emailed about
        their own action. These settings decide for everyone: "Use each person's choice" leaves it to them,
        "On for everyone" and "Off for everyone" ignore what they chose. On an install upgraded from 1.2 all four
        start as "Off for everyone", so no staff email is sent until you change them. Only has an effect once email
        is set up.
      </div>
      {STAFF_EMAIL_SWITCHES.map(({ key, name, help }) => (
        <SettingRow key={key} label={name} description={help}>
          <Select
            className="w-52"
            aria-label={`${name}: administrator override`}
            value={str(key) || 'user'}
            onChange={(e) => setStr(key, e.target.value)}
          >
            <option value="user">Use each person's choice</option>
            <option value="on">On for everyone</option>
            <option value="off">Off for everyone</option>
          </Select>
        </SettingRow>
      ))}
    </Section>
  )
}
