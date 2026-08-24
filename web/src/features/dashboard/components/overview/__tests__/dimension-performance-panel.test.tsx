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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest'

import type {
  PerformanceDimension,
  PerformanceDimensionItem,
  PerformanceDimensionsData,
} from '@/features/performance-metrics/types'

import { DimensionPerformancePanel } from '../dimension-performance-panel'

const apiMocks = vi.hoisted(() => ({
  getPerfMetricsDimensions: vi.fn(),
}))

vi.mock('@/features/performance-metrics/api', () => ({
  getPerfMetricsDimensions: apiMocks.getPerfMetricsDimensions,
}))

function makeItem(
  overrides: Partial<PerformanceDimensionItem> = {}
): PerformanceDimensionItem {
  return {
    id: 2,
    name: 'Gateway A',
    request_count: 10,
    success_count: 9,
    failure_count: 1,
    success_rate: 90,
    cache_eligible_count: 8,
    cache_hit_count: 4,
    cache_hit_rate: 50,
    input_tokens: 1000,
    cached_tokens: 400,
    cached_token_rate: 40,
    ...overrides,
  }
}

function makeResponse(
  dimension: PerformanceDimension,
  items: PerformanceDimensionItem[]
): PerformanceDimensionsData {
  return {
    success: true,
    data: { dimension, hours: 24, items },
  }
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const result = render(
    <QueryClientProvider client={queryClient}>
      <DimensionPerformancePanel />
    </QueryClientProvider>
  )
  return { ...result, queryClient }
}

describe('dimension performance panel', () => {
  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', {
      'API Keys': 'API Keys',
      Channels: 'Channels',
      'Failed to load performance data': 'Failed to load performance data',
      'Loading...': 'Loading...',
      Name: 'Name',
      'No performance data available': 'No performance data available',
      'Performance dimensions': 'Performance dimensions',
      'Performance metrics for the last 24 hours':
        'Performance metrics for the last 24 hours',
      'Request cache hit rate': 'Request cache hit rate',
      Requests: 'Requests',
      Retry: 'Retry',
      'Success rate': 'Success rate',
      'Token cache hit rate': 'Token cache hit rate',
      Users: 'Users',
    })
  })

  beforeEach(() => {
    apiMocks.getPerfMetricsDimensions.mockReset()
  })

  test('shows a loading status while the selected dimension is pending', () => {
    apiMocks.getPerfMetricsDimensions.mockReturnValue(new Promise(() => {}))

    renderPanel()

    expect(screen.getByRole('status', { name: 'Loading...' })).toBeVisible()
  })

  test('shows the empty state when the selected dimension has no samples', async () => {
    apiMocks.getPerfMetricsDimensions.mockResolvedValue(
      makeResponse('channel', [])
    )

    renderPanel()

    expect(
      await screen.findByText('No performance data available')
    ).toBeVisible()
  })

  test('retries a failed dimension request from the error state', async () => {
    apiMocks.getPerfMetricsDimensions
      .mockRejectedValueOnce(new Error('network error'))
      .mockResolvedValueOnce(makeResponse('channel', [makeItem()]))
    const user = userEvent.setup()

    renderPanel()
    await screen.findByRole('alert')
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findAllByText('Gateway A')).toHaveLength(2)
    expect(apiMocks.getPerfMetricsDimensions).toHaveBeenCalledTimes(2)
  })

  test('switches to API Key metrics and renders desktop and mobile layouts without exposing a raw key', async () => {
    apiMocks.getPerfMetricsDimensions.mockImplementation(
      async (dimension: PerformanceDimension) => {
        if (dimension === 'token') {
          const item = {
            ...makeItem({ id: 42, name: 'Production key' }),
            key: 'sk-raw-secret',
          } as PerformanceDimensionItem
          return makeResponse('token', [item])
        }
        return makeResponse('channel', [makeItem()])
      }
    )
    const user = userEvent.setup()

    renderPanel()
    await screen.findAllByText('Gateway A')
    await user.click(screen.getByRole('tab', { name: 'API Keys' }))
    await waitFor(() =>
      expect(apiMocks.getPerfMetricsDimensions).toHaveBeenLastCalledWith(
        'token',
        24
      )
    )

    expect(await screen.findAllByText('Production key')).toHaveLength(2)
    expect(screen.getAllByText('#42')).toHaveLength(2)
    expect(screen.queryByText('sk-raw-secret')).not.toBeInTheDocument()
    expect(screen.getByTestId('dimension-performance-table')).toHaveClass(
      'hidden',
      'sm:block'
    )
    expect(screen.getByTestId('dimension-performance-mobile-list')).toHaveClass(
      'grid',
      'sm:hidden'
    )
  })
})
