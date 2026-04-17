import { api } from './client'
import type { User } from './types'

export interface LoginResponse {
  user: User
  mfa_needed: boolean
  mfa_enrollment_needed: boolean
}

export async function login(email: string, password: string): Promise<LoginResponse> {
  const res = await api.post<LoginResponse>('/auth/local/login', { email, password })
  return res.data
}

export async function logout(): Promise<void> {
  await api.post('/auth/local/logout')
}

export async function verifyMFA(code: string): Promise<void> {
  await api.post('/auth/local/mfa/verify', { code })
}

export async function getMe(): Promise<User> {
  const res = await api.get<User>('/me')
  return res.data
}

export async function changePassword(current: string, next: string): Promise<void> {
  await api.patch('/me/password', { current_password: current, new_password: next })
}

export async function enrollMFAStart(): Promise<{ secret: string; qr_url: string; qr_data_url: string }> {
  const res = await api.get('/me/mfa/enroll')
  return res.data
}

export async function enrollMFAConfirm(code: string): Promise<void> {
  await api.post('/me/mfa/enroll/confirm', { code })
}
