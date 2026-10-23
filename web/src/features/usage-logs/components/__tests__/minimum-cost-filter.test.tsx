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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { buildApiParams } from '../../lib/utils'
import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

function FilterFixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <UsageLogsProvider>
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilter(initialEntry = '/usage-logs/common') {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self/groups') {
      return { data: { success: true, data: {} } }
    }
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: {
            display_in_currency: true,
            quota_display_type: 'CNY',
            quota_per_unit: 500000,
            usd_exchange_rate: 7,
          },
        },
      }
    }
    return { data: { success: true, data: { quota: 0, rpm: 0, tpm: 0 } } }
  })

  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: FilterFixture,
    validateSearch: (search: Record<string, unknown>) => ({
      ...search,
      minQuota: search.minQuota ? Number(search.minQuota) : undefined,
      page: search.page ? Number(search.page) : undefined,
    }),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: /^Expand/ })
  await waitFor(() =>
    expect(
      useSystemConfigStore.getState().config.currency.quotaDisplayType
    ).toBe('CNY')
  )
  return router
}

beforeEach(async () => {
  const originalMatchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...originalMatchMedia(query),
    matches: false,
  }))
  localStorage.clear()
  await useSystemConfigStore.persist.rehydrate()
  useSystemConfigStore.setState(
    (state) => ({
      config: {
        ...state.config,
        currency: {
          ...DEFAULT_CURRENCY_CONFIG,
          quotaDisplayType: 'CNY',
          usdExchangeRate: 7,
        },
      },
    }),
    false
  )
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
})

it('restores and submits a four-decimal CNY amount without double conversion', async () => {
  const user = userEvent.setup()
  const router = await renderFilter(
    '/usage-logs/common?page=3&minQuota=5018207'
  )
  const expandButton = screen.getByRole('button', { name: /^Expand/ })
  await user.click(expandButton)
  expect(expandButton).toHaveAttribute('aria-expanded', 'true')

  const input = screen.getByLabelText('Minimum cost (CNY)')
  expect(input).toHaveValue(70.2549)

  await user.clear(input)
  await user.type(input, '70.2549')
  await user.keyboard('{Enter}')

  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      minQuota: 5018207,
      page: 1,
    })
  )
  expect(input).toHaveValue(70.2549)
})

it('reconverts the minimum cost without dropping other unsaved filters when the display currency changes', async () => {
  const user = userEvent.setup()
  await renderFilter('/usage-logs/common?minQuota=5000000')
  await user.click(screen.getByRole('button', { name: /^Expand/ }))
  const modelInput = screen.getByPlaceholderText('Model Name')
  await user.type(modelInput, 'gemini')

  expect(screen.getByLabelText('Minimum cost (CNY)')).toHaveValue(70)

  act(() => {
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'USD',
        usdExchangeRate: 1,
      },
    })
  })

  expect(screen.getByLabelText('Minimum cost (USD)')).toHaveValue(10)
  expect(modelInput).toHaveValue('gemini')
})

it('round-trips a one-quota-unit minimum at the configured precision', async () => {
  const user = userEvent.setup()
  const router = await renderFilter('/usage-logs/common?minQuota=1')
  await user.click(screen.getByRole('button', { name: /^Expand/ }))

  const input = screen.getByLabelText('Minimum cost (CNY)')
  expect(input).toHaveValue(0.000014)

  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      minQuota: 1,
      page: 1,
    })
  )
})

it('submits a custom-currency amount as raw quota from the Search button', async () => {
  const user = userEvent.setup()
  const router = await renderFilter()
  act(() => {
    useSystemConfigStore.setState(
      (state) => ({
        config: {
          ...state.config,
          currency: {
            ...DEFAULT_CURRENCY_CONFIG,
            quotaDisplayType: 'CUSTOM',
            customCurrencySymbol: '¤',
            customCurrencyExchangeRate: 0.5,
          },
        },
      }),
      false
    )
  })
  await user.click(screen.getByRole('button', { name: /^Expand/ }))

  await user.type(await screen.findByLabelText('Minimum cost (¤)'), '3.75')
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      minQuota: 3750000,
      page: 1,
    })
  )
})

it('maps the raw minimum quota to the list API parameter', () => {
  const params = buildApiParams({
    page: 2,
    pageSize: 20,
    searchParams: { minQuota: 5017857 },
    isAdmin: false,
  })

  expect(params).toMatchObject({
    p: 2,
    page_size: 20,
    min_quota: 5017857,
  })
})

it('removes the minimum-cost filter when the amount is cleared', async () => {
  const user = userEvent.setup()
  const router = await renderFilter(
    '/usage-logs/common?page=3&minQuota=5017857'
  )
  await user.click(screen.getByRole('button', { name: /^Expand/ }))

  const input = screen.getByLabelText('Minimum cost (CNY)')
  await user.clear(input)
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() =>
    expect(router.state.location.search).not.toHaveProperty('minQuota')
  )
})
