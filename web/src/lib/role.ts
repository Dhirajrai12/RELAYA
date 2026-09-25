import { useAuth } from './auth'
import { useMe } from './queries'
import type { Role } from './types'

const rank: Record<Role, number> = { member: 1, admin: 2, owner: 3 }

/** The signed-in user's role in the selected organization. */
export function useRole(): Role | undefined {
  const orgId = useAuth((s) => s.orgId)
  return useMe().data?.orgs.find((o) => o.id === orgId)?.role
}

export function useHasRole(min: Role): boolean {
  const role = useRole()
  return !!role && rank[role] >= rank[min]
}

/** Admins and owners can create and change configuration. */
export const useCanManage = () => useHasRole('admin')
