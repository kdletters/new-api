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
import { render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { Dashboard } from '../index'

const pageState = vi.hoisted(() => ({
  role: 10,
  section: 'models',
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    getRouteApi: () => ({
      useParams: () => ({ section: pageState.section }),
    }),
    useNavigate: () => vi.fn(),
  }
})

vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (
    selector: (state: { auth: { user: { role: number } } }) => unknown
  ) => selector({ auth: { user: { role: pageState.role } } }),
}))

vi.mock('@/components/layout', () => {
  const Slot = (props: { children: ReactNode }): ReactNode => props.children
  const SectionPageLayout = (props: { children: ReactNode }): ReactNode =>
    props.children
  SectionPageLayout.Title = Slot
  SectionPageLayout.Content = Slot
  return { SectionPageLayout }
})

vi.mock('@/components/page-transition', () => ({
  FadeIn: (props: { children: ReactNode }): ReactNode => props.children,
}))

vi.mock('../components/models/models-chart-preferences', () => ({
  ModelsChartPreferences: () => null,
}))

vi.mock('../components/models/models-filter-dialog', () => ({
  ModelsFilter: () => null,
}))

vi.mock('../components/models/log-stat-cards', () => ({
  LogStatCards: () => <div data-testid='model-statistics' />,
}))

vi.mock('../components/models/performance-overview', () => ({
  PerformanceOverview: () => <div data-testid='model-performance' />,
}))

vi.mock('../components/models/consumption-distribution-chart', () => ({
  ConsumptionDistributionChart: () => null,
}))

vi.mock('../components/models/model-charts', () => ({
  ModelCharts: () => null,
}))

vi.mock('../components/overview/overview-dashboard', () => ({
  OverviewDashboard: () => <div data-testid='overview-dashboard' />,
}))

vi.mock('../components/overview/dimension-performance-panel', () => ({
  DimensionPerformancePanel: () => (
    <div data-testid='dimension-performance-panel' />
  ),
}))

describe('dimension performance panel placement', () => {
  beforeEach(() => {
    pageState.role = 10
    pageState.section = 'models'
  })

  test('shows the dimension panel to administrators on the data dashboard', () => {
    render(<Dashboard />)

    expect(screen.getByTestId('dimension-performance-panel')).toBeVisible()
    expect(screen.queryByTestId('overview-dashboard')).not.toBeInTheDocument()
  })

  test('does not show the administrator dimension panel to regular users', () => {
    pageState.role = 1

    render(<Dashboard />)

    expect(
      screen.queryByTestId('dimension-performance-panel')
    ).not.toBeInTheDocument()
  })

  test('keeps the dimension panel out of the overview page', () => {
    pageState.section = 'overview'

    render(<Dashboard />)

    expect(screen.getByTestId('overview-dashboard')).toBeVisible()
    expect(
      screen.queryByTestId('dimension-performance-panel')
    ).not.toBeInTheDocument()
  })
})
