import { Layout } from '@/components/Layout'
import { EmailTemplatesTab } from '@/pages/admin/settings/EmailTemplatesTab'

// Temporary: the Templates tab of System -> Email (#674) now holds the body of
// this page. It stays routed at /admin/email-templates, in its old Layout and
// h1, until the routes move and this file is deleted (plan task 7).
export function EmailTemplatesPage() {
  return (
    <Layout>
      <div className="space-y-6">
        <h1 className="text-2xl font-bold text-gray-900">Email templates</h1>
        <EmailTemplatesTab />
      </div>
    </Layout>
  )
}
