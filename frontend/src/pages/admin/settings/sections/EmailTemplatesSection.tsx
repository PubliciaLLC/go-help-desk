import { Link } from '@tanstack/react-router'
import { Section } from '@/components/admin/Section'

export function EmailTemplatesSection() {
  return (
    <Section title="Email templates">
      <div className="px-5 py-4 text-sm text-gray-500">
        The wording of every email this help desk sends, and a header and footer for all of them, can be edited.{' '}
        <Link to="/admin/email-templates" className="font-medium text-blue-600 underline">
          Edit email templates
        </Link>
      </div>
    </Section>
  )
}
