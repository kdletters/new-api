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
import i18next from 'i18next'
import { beforeAll, describe, expect, test } from 'vitest'

import type { PerformanceDimensionItem } from '../../types'
import { DimensionMetricsCell } from '../dimension-metrics-cell'

const metrics: PerformanceDimensionItem = {
  id: 1,
  name: 'Gateway',
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
}

describe('dimension metrics cell', () => {
  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', {
      'Success rate': 'Success rate',
      'Request cache hit rate': 'Request cache hit rate',
      'Token cache hit rate': 'Token cache hit rate',
    })
  })

  test('shows all three rates when each metric has a denominator', () => {
    render(<DimensionMetricsCell metrics={metrics} />)

    expect(screen.getByText('90.00%')).toBeInTheDocument()
    expect(screen.getByText('50.00%')).toBeInTheDocument()
    expect(screen.getByText('40.00%')).toBeInTheDocument()
  })

  test('shows an em dash for missing samples and zero cache denominators', () => {
    render(
      <DimensionMetricsCell
        metrics={{
          ...metrics,
          request_count: 0,
          cache_eligible_count: 0,
          input_tokens: 0,
        }}
      />
    )

    expect(screen.getAllByText('—')).toHaveLength(3)
    expect(screen.queryByText('0.00%')).not.toBeInTheDocument()
  })
})
