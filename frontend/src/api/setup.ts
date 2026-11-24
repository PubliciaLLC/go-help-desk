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
): Promise<User> {
  const res = await api.post<User>('/setup', {
    email,
    display_name: displayName,
    password,
  })
  return res.data
}
