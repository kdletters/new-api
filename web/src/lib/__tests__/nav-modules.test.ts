/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { afterEach, describe, expect, it } from 'vitest'

import { getModuleAccessFromStatus } from '@/lib/nav-modules'
import { useAuthStore } from '@/stores/auth-store'

const originalAuth = useAuthStore.getState().auth

function statusWithGroups(groups?: string[]) {
  return {
    HeaderNavModules: JSON.stringify({
      pricing: {
        enabled: true,
        requireAuth: false,
        ...(groups ? { groups } : {}),
      },
    }),
  }
}

function signIn(group: string) {
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'member', role: 1, group },
    },
  })
}

afterEach(() => {
  useAuthStore.setState({ auth: originalAuth })
})

describe('getModuleAccessFromStatus group allowlist', () => {
  it('allows a signed-in user whose group is listed', () => {
    signIn('公司内部')

    expect(
      getModuleAccessFromStatus(statusWithGroups(['公司内部']), 'pricing')
    ).toEqual({ enabled: true, requireAuth: false })
  })

  it('hides the module from a signed-in user in another group', () => {
    signIn('taonier')

    expect(
      getModuleAccessFromStatus(statusWithGroups(['公司内部']), 'pricing')
    ).toEqual({ enabled: false, requireAuth: true })
  })

  it('requires sign-in for anonymous visitors when an allowlist exists', () => {
    useAuthStore.setState({ auth: { ...originalAuth, user: null } })

    expect(
      getModuleAccessFromStatus(statusWithGroups(['公司内部']), 'pricing')
    ).toEqual({ enabled: true, requireAuth: true })
  })

  it('keeps modules without an allowlist unchanged', () => {
    useAuthStore.setState({ auth: { ...originalAuth, user: null } })

    expect(getModuleAccessFromStatus(statusWithGroups(), 'pricing')).toEqual({
      enabled: true,
      requireAuth: false,
    })
  })
})
