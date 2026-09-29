import { create } from 'zustand'
import { persist } from 'zustand/middleware'

import type { User } from './types'

interface AuthState {
  user: User | null
  orgId: string | null // the organization currently selected in the UI (persisted across sessions)
  signIn: (user: User) => void
  setOrg: (orgId: string) => void
  signOut: () => void
  // Token is no longer stored here; it's in an httpOnly cookie sent automatically by the browser.
  get token(): string | null // kept for backward compatibility, always returns null
}

// Session is now stored in an httpOnly cookie that's automatically sent with each request.
// This prevents XSS attacks from stealing the token, and ensures it's never accessible to JavaScript.
// Only orgId is persisted to localStorage so we remember the user's selected organization.
export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      orgId: null,
      signIn: (user) => set({ user }),
      setOrg: (orgId) => set({ orgId }),
      signOut: () => set({ user: null, orgId: null }),
      get token() {
        return null // token is in httpOnly cookie now
      },
    }),
    {
      name: 'relaya-auth',
      partialize: (state) => ({ orgId: state.orgId }), // only persist orgId
    },
  ),
)
