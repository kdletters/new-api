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
import { useTranslation } from 'react-i18next'

import { formatUptimePct } from '../lib/format'
import type { PerformanceDimensionItem } from '../types'

function formatRate(rate: number | undefined, denominator: number): string {
  if (!Number.isFinite(denominator) || denominator <= 0) return '—'
  return formatUptimePct(Number(rate))
}

export function DimensionMetricsCell(props: {
  metrics?: PerformanceDimensionItem
}) {
  const { t } = useTranslation()
  const metrics = props.metrics
  const fields = [
    {
      label: t('Success rate'),
      value: formatRate(metrics?.success_rate, metrics?.request_count ?? 0),
    },
    {
      label: t('Request cache hit rate'),
      value: formatRate(
        metrics?.cache_hit_rate,
        metrics?.cache_eligible_count ?? 0
      ),
    },
    {
      label: t('Token cache hit rate'),
      value: formatRate(metrics?.cached_token_rate, metrics?.input_tokens ?? 0),
    },
  ]

  return (
    <dl
      data-slot='dimension-metrics-cell'
      className='grid min-w-0 grid-cols-1 gap-1 sm:min-w-72 sm:grid-cols-3 sm:gap-3'
    >
      {fields.map((field) => (
        <div key={field.label} className='min-w-0'>
          <dt className='text-muted-foreground truncate text-[10px] leading-tight'>
            {field.label}
          </dt>
          <dd className='mt-0.5 font-mono text-xs font-semibold tabular-nums'>
            {field.value}
          </dd>
        </div>
      ))}
    </dl>
  )
}
