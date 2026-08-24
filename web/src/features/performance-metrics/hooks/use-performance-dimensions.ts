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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getPerfMetricsDimensions } from '../api'
import type { PerformanceDimension, PerformanceDimensionItem } from '../types'

export function usePerformanceDimensions(
  dimension: PerformanceDimension,
  hours = 24
) {
  const query = useQuery({
    queryKey: ['perf-metrics-dimensions', dimension, hours],
    queryFn: async () => {
      const result = await getPerfMetricsDimensions(dimension, hours)
      if (!result.success) {
        throw new Error(result.message || 'Failed to load performance data')
      }
      return result
    },
    staleTime: 60 * 1000,
    retry: false,
  })
  const items = useMemo(() => query.data?.data.items ?? [], [query.data])
  const metricsById = useMemo(
    () =>
      new Map<number, PerformanceDimensionItem>(
        items.map((item) => [item.id, item])
      ),
    [items]
  )

  return { query, items, metricsById }
}
