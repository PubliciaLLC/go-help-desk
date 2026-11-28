import { api } from './client'
import type { User } from './types'

export async function getSetupStatus(): Promise<{ needed: boolean }> {
  const res = await api.get<{ needed: boolean }>('/setup/status')
  return res.data
}

export async function setupAdmin(
  email: string,
  displayName: string,
  password: string,
  /**
   * The instance's first category. Sent with setup rather than left for
   * later because a ticket cannot be filed without one, and setup is the only
   * moment anybody is being asked to configure anything — before this, a
   * fresh instance answered `400 category_id is required` to the first ticket
   * its new administrator tried to file (#323).
   *
   * Optional here: the server names it for you when it is blank.
   */
  category?: string,
  /**
   * Whether "Check for new releases" was ticked (#170). Pass it only
   * when ticked: absent means off, and an unticked box sends nothing.
   */
  updateCheck?: boolean,
): Promise<User> {
  const res = await api.post<User>('/setup', {
    email,
    display_name: displayName,
    password,
    category,
    update_check: updateCheck,
  })
  return res.data
}
