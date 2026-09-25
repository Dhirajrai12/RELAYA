import { create } from 'zustand'
import { persist } from 'zustand/middleware'

import type { User } from './types'

interface AuthState {
  token: string | null
  user: User | null
  orgId: string | null // the organization currently selected in the UI
  signIn: (token: string, user: User) => void
  setOrg: (orgId: string) => void
  signOut: () => void
}

// Persisted to localStorage so a refresh keeps you signed in.
// TODO before public launch: move the session to an httpOnly cookie.
export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      orgId: null,
      signIn: (token, user) => set({ token, user }),
      setOrg: (orgId) => set({ orgId }),
      signOut: () => set({ token: null, user: null, orgId: null }),
    }),
    { name: 'relaya-auth' },
  ),
)
