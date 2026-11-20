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
import { beforeEach, describe, expect, test } from 'vitest'

import type {
  ProcessedChartData,
  QuotaDataItem,
} from '@/features/dashboard/types'
import { formatChartTime, type TimeGranularity } from '@/lib/time'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { processChartData, processUserChartData } from '../charts'

const cases: Array<{
  granularity: TimeGranularity
  start: string
  interval: number
  labels: string[]
}> = [
  {
    granularity: 'hour',
    start: '2025-12-31T20:00:00',
    interval: 3600,
    labels: [
      '12-31 20:00',
      '12-31 21:00',
      '12-31 22:00',
      '12-31 23:00',
      '01-01 00:00',
      '01-01 01:00',
      '01-01 02:00',
      '01-01 03:00',
    ],
  },
  {
    granularity: 'day',
    start: '2025-12-27T12:00:00',
    interval: 86400,
    labels: [
      '12-27',
      '12-28',
      '12-29',
      '12-30',
      '12-31',
      '01-01',
      '01-02',
      '01-03',
    ],
  },
  {
    granularity: 'week',
    start: '2025-11-29T12:00:00',
    interval: 604800,
    labels: [
      '11-29 - 12-05',
      '12-06 - 12-12',
      '12-13 - 12-19',
      '12-20 - 12-26',
      '12-27 - 01-02',
      '01-03 - 01-09',
      '01-10 - 01-16',
      '01-17 - 01-23',
    ],
  },
]

function unorderedUsage(start: string, interval: number): QuotaDataItem[] {
  const first = new Date(start).getTime() / 1000
  const rows = Array.from({ length: 8 }, (_, i) => ({
    created_at: first + i * interval,
    model_name: 'model-a',
    username: 'alice',
    quota: (i + 1) * 500000,
    count: i + 1,
  }))
  // Same hour, or a second hourly record within the same day/week label.
  const duplicate = {
    ...rows[0],
    created_at: first + (interval === 3600 ? 0 : 3600),
    quota: 5000000,
    count: 10,
  }
  return [
    rows[7],
    rows[2],
    duplicate,
    rows[5],
    rows[0],
    rows[6],
    rows[1],
    rows[4],
    rows[3],
  ]
}

describe.each(cases)('dashboard $granularity chart chronology', (scenario) => {
  test('orders model buckets across New Year while aggregating repeated labels', () => {
    const data = unorderedUsage(scenario.start, scenario.interval)
    const original = structuredClone(data)
    const result = processChartData(data, scenario.granularity)

    for (const key of ['spec_line', 'spec_area', 'spec_model_line'] as const) {
      const values: Array<{ Time: string; rawQuota: number; Count: number }> =
        result[key].data[0].values
      expect(values.map((row) => row.Time)).toEqual(scenario.labels)
      expect(
        values.map((row) =>
          key === 'spec_model_line' ? row.Count : row.rawQuota / 500000
        )
      ).toEqual([11, 2, 3, 4, 5, 6, 7, 8])
    }
    expect(data).toEqual(original)
  })

  test('orders user buckets across New Year while aggregating repeated labels', () => {
    const data = unorderedUsage(scenario.start, scenario.interval)
    const original = structuredClone(data)
    const result = processUserChartData(data, scenario.granularity)
    const values: Array<{ Time: string; rawQuota: number }> =
      result.spec_user_trend.data[0].values

    expect(values.map((row) => row.Time)).toEqual(scenario.labels)
    expect(values.map((row) => row.rawQuota / 500000)).toEqual([
      11, 2, 3, 4, 5, 6, 7, 8,
    ])
    expect(data).toEqual(original)
  })

  test('keeps the existing seven padded model buckets in chronological order', () => {
    const data = [unorderedUsage(scenario.start, scenario.interval)[0]]
    const result = processChartData(data, scenario.granularity)

    for (const key of ['spec_line', 'spec_area', 'spec_model_line'] as const) {
      const values: Array<{ Time: string; rawQuota: number; Count: number }> =
        result[key].data[0].values
      expect(values.map((row) => row.Time)).toEqual(scenario.labels.slice(1))
      expect(
        values.map((row) =>
          key === 'spec_model_line' ? row.Count : row.rawQuota / 500000
        )
      ).toEqual([0, 0, 0, 0, 0, 0, 8])
    }
  })
})

function leftTickAt(
  spec: ProcessedChartData['spec_line'],
  category: string
): string {
  const row = spec.data[0].values.find(
    (point: Record<string, unknown>) => point[spec.xField] === category
  )
  expect(row, `Missing chart data for ${category}`).toBeDefined()
  const value = row[spec.yField]
  const leftAxis = spec.axes?.find(
    (axis: { orient: string }) => axis.orient === 'left'
  )
  const format = leftAxis?.label?.formatMethod
  return String(format ? format(value, { rawValue: value }) : value)
}

describe('dashboard left axis units', () => {
  const point = {
    created_at: new Date('2026-01-15T12:00:00').getTime() / 1000,
    model_name: 'model-a',
    count: 3,
    token_used: 91,
  }
  const day = formatChartTime(point.created_at)
  const currency = {
    ...DEFAULT_CURRENCY_CONFIG,
    quotaPerUnit: 400_000,
    usdExchangeRate: 7,
    customCurrencySymbol: '★',
    customCurrencyExchangeRate: 2.5,
  }

  beforeEach(() => {
    const { config } = useSystemConfigStore.getState()
    const storageKey = 'system-config-storage'
    const stored = localStorage.getItem(storageKey)
    return () => {
      useSystemConfigStore.setState({ config })
      if (stored === null) localStorage.removeItem(storageKey)
      else localStorage.setItem(storageKey, stored)
    }
  })

  describe.each([
    { chart: 'bar', key: 'spec_line' },
    { chart: 'area', key: 'spec_area' },
  ] as const)('consumption $chart', ({ key }) => {
    test.each([
      { mode: 'CNY', quota: 800_000, symbol: '¥', amount: 14 },
      { mode: 'USD', quota: 800_000, symbol: '$', amount: 2 },
      { mode: 'CUSTOM', quota: 800_000, symbol: '★', amount: 5 },
      { mode: 'TOKENS', quota: 7, symbol: '', amount: 7 },
    ] as const)(
      'shows the plotted quota in $mode',
      ({ mode, quota, symbol, amount }) => {
        useSystemConfigStore.getState().setConfig({
          currency: {
            ...currency,
            quotaDisplayType: mode,
            displayInCurrency: mode !== 'TOKENS',
          },
        })
        const charts = processChartData([{ ...point, quota }])

        const tick = leftTickAt(charts[key], day)
        if (symbol) expect.soft(tick).toContain(symbol)
        const numberText = tick.replace(symbol, '').trim()
        expect(Number(numberText.replace(',', '.'))).toBe(amount)
      }
    )
  })

  test('keeps call trend and ranking ticks in requests when quota uses CNY', () => {
    useSystemConfigStore.getState().setConfig({
      currency: { ...currency, quotaDisplayType: 'CNY' },
    })
    const charts = processChartData([{ ...point, quota: 800_000 }])

    expect(Number(leftTickAt(charts.spec_model_line, day))).toBe(point.count)
    expect(Number(leftTickAt(charts.spec_rank_bar, point.model_name))).toBe(
      point.count
    )
  })
})
