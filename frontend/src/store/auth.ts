import { create } from 'zustand'
import type { User } from '../api/types'

interface AuthState {
  user: User | null
  mfaPending: boolean
  setUser: (user: User | null) => void
  setMFAPending: (pending: boolean) => void
  clear: () => void
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  mfaPending: false,
  setUser: (user) => set({ user, mfaPending: false }),
  setMFAPending: (mfaPending) => set({ mfaPending }),
  clear: () => set({ user: null, mfaPending: false }),
}))
