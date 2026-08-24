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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getPerfMetricsDimensions } from '../api'

const apiMocks = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({
  api: { get: apiMocks.get },
}))

describe('performance dimensions API', () => {
  beforeEach(() => {
    apiMocks.get.mockReset()
  })

  test('requests one selected dimension with the requested time window', async () => {
    const response = {
      success: true,
      data: { dimension: 'channel', hours: 24, items: [] },
    }
    apiMocks.get.mockResolvedValue({ data: response })

    const result = await getPerfMetricsDimensions('channel', 24)

    expect(apiMocks.get).toHaveBeenCalledTimes(1)
    expect(apiMocks.get).toHaveBeenCalledWith('/api/perf-metrics/dimensions', {
      params: { dimension: 'channel', hours: 24 },
    })
    expect(result).toEqual(response)
  })
})
