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
import { describe, expect, test } from 'vitest'

import {
  HEADER_NAV_DEFAULT,
  parseHeaderNavModules,
  serializeHeaderNavModules,
} from '../config'

describe('header navigation visible groups', () => {
  test('normalizes groups from arrays and comma-separated strings', () => {
    const parsed = parseHeaderNavModules(
      JSON.stringify({
        pricing: {
          enabled: true,
          requireAuth: true,
          groups: [' 公司内部 ', 'vip', 'vip', '', 42],
        },
        rankings: {
          enabled: true,
          requireAuth: false,
          groups: 'vip, default，公司内部\nvip',
        },
      })
    )

    expect(parsed.pricing.groups).toEqual(['公司内部', 'vip'])
    expect(parsed.rankings.groups).toEqual(['vip', 'default', '公司内部'])
  })

  test('falls back to no restriction for malformed group values', () => {
    for (const groups of [null, 123, {}, '', [], ['   '], [1, 2]]) {
      const parsed = parseHeaderNavModules(
        JSON.stringify({
          pricing: { enabled: true, requireAuth: false, groups },
        })
      )
      expect(parsed.pricing.groups).toBeUndefined()
    }

    expect(
      parseHeaderNavModules('{"pricing":true}').pricing.groups
    ).toBeUndefined()
  })

  test('omits empty groups so existing configs keep their stored shape', () => {
    const serialized = serializeHeaderNavModules({
      ...HEADER_NAV_DEFAULT,
      pricing: { enabled: true, requireAuth: true, groups: [] },
    })

    expect(serialized).toBe(
      JSON.stringify({
        ...HEADER_NAV_DEFAULT,
        pricing: { enabled: true, requireAuth: true },
      })
    )
    expect('groups' in HEADER_NAV_DEFAULT.pricing).toBe(false)
  })

  test('serializes non-empty groups together with the module access flags', () => {
    const serialized = serializeHeaderNavModules({
      ...HEADER_NAV_DEFAULT,
      rankings: { enabled: true, requireAuth: false, groups: ['vip'] },
    })

    expect(JSON.parse(serialized)).toEqual({
      ...HEADER_NAV_DEFAULT,
      rankings: { enabled: true, requireAuth: false, groups: ['vip'] },
    })
  })
})
