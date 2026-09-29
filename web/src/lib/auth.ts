import { create } from 'zustand'
import { persist } from 'zustand/middleware'

import type { User } from './types'

interface AuthState {
  // The signed-in user, or null. The session itself is an httpOnly cookie the
  // browser sends with every request; scripts (and so XSS) can never read it.
  user: User | null
  orgId: string | null // the organization currently selected in the UI
  signIn: (user: User) => void
  setOrg: (orgId: string) => void
  signOut: () => void
}

// The user and selected org are remembered in localStorage (nothing secret) so a
// refresh keeps you in the app. If the cookie has expired, the first API call
// answers 401 and signs you out.
export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      orgId: null,
      signIn: (user) => set({ user }),
      setOrg: (orgId) => set({ orgId }),
      signOut: () => set({ user: null, orgId: null }),
    }),
    {
      name: 'relaya-auth',
      version: 2, // v1 stored the session token; dropping it signs those browsers out once
      partialize: (state) => ({ user: state.user, orgId: state.orgId }),
      migrate: () => ({ user: null, orgId: null }),
    },
  ),
)

/** True once someone is signed in (the session cookie itself is invisible to scripts). */
export const useSignedIn = () => useAuth((s) => s.user !== null)
