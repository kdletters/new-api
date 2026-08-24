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
import { ChartNoAxesCombined, DatabaseZap, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { usePerformanceDimensions } from '@/features/performance-metrics/hooks/use-performance-dimensions'
import {
  formatUptimePct,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import type {
  PerformanceDimension,
  PerformanceDimensionItem,
} from '@/features/performance-metrics/types'
import { cn } from '@/lib/utils'

const PERFORMANCE_WINDOW_HOURS = 24

const DIMENSIONS: Array<{
  value: PerformanceDimension
  labelKey: string
}> = [
  { value: 'channel', labelKey: 'Channels' },
  { value: 'user', labelKey: 'Users' },
  { value: 'token', labelKey: 'API Keys' },
]

function isPerformanceDimension(value: string): value is PerformanceDimension {
  return DIMENSIONS.some((dimension) => dimension.value === value)
}

function formatRequestCount(count: number): string {
  if (!Number.isFinite(count) || count < 0) return '—'
  return Math.trunc(count).toLocaleString()
}

function formatCacheRate(rate: number, denominator: number): string {
  if (!Number.isFinite(denominator) || denominator <= 0) return '—'
  return formatUptimePct(rate)
}

export function DimensionPerformancePanel() {
  const { t } = useTranslation()
  const [dimension, setDimension] = useState<PerformanceDimension>('channel')
  const { query: metricsQuery, items } = usePerformanceDimensions(
    dimension,
    PERFORMANCE_WINDOW_HOURS
  )

  const handleDimensionChange = (value: string) => {
    if (isPerformanceDimension(value)) setDimension(value)
  }

  let content = <DimensionPerformanceResults items={items} />
  if (metricsQuery.isLoading) {
    content = <DimensionPerformanceLoading />
  } else if (metricsQuery.isError) {
    content = (
      <DimensionPerformanceError onRetry={() => void metricsQuery.refetch()} />
    )
  } else if (items.length === 0) {
    content = <DimensionPerformanceEmpty />
  }

  return (
    <section className='bg-card overflow-hidden rounded-2xl border shadow-xs'>
      <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:px-5'>
        <div className='flex min-w-0 items-center gap-2'>
          <IconBadge tone='info' size='sm'>
            <ChartNoAxesCombined />
          </IconBadge>
          <div className='min-w-0'>
            <h3 className='truncate text-sm font-semibold'>
              {t('Performance dimensions')}
            </h3>
            <span className='text-muted-foreground text-xs'>
              {t('Performance metrics for the last 24 hours')}
            </span>
          </div>
        </div>

        <Tabs
          value={dimension}
          onValueChange={handleDimensionChange}
          className='sm:ml-auto'
        >
          <TabsList aria-label={t('Performance dimensions')}>
            {DIMENSIONS.map((item) => (
              <TabsTrigger key={item.value} value={item.value}>
                {t(item.labelKey)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>

      <div className='p-4 sm:p-5'>{content}</div>
    </section>
  )
}

function DimensionPerformanceLoading() {
  const { t } = useTranslation()

  return (
    <div role='status' aria-label={t('Loading...')} className='space-y-2'>
      {['first', 'second', 'third'].map((key) => (
        <Skeleton key={key} className='h-14 w-full rounded-lg' />
      ))}
    </div>
  )
}

function DimensionPerformanceError(props: { onRetry: () => void }) {
  const { t } = useTranslation()

  return (
    <Empty role='alert' className='min-h-44 border'>
      <EmptyHeader>
        <EmptyMedia variant='icon'>
          <RefreshCw />
        </EmptyMedia>
        <EmptyTitle>{t('Failed to load performance data')}</EmptyTitle>
      </EmptyHeader>
      <Button variant='outline' size='sm' onClick={props.onRetry}>
        <RefreshCw data-icon='inline-start' />
        {t('Retry')}
      </Button>
    </Empty>
  )
}

function DimensionPerformanceEmpty() {
  const { t } = useTranslation()

  return (
    <Empty className='min-h-44 border'>
      <EmptyHeader>
        <EmptyMedia variant='icon'>
          <DatabaseZap />
        </EmptyMedia>
        <EmptyTitle>{t('No performance data available')}</EmptyTitle>
        <EmptyDescription>
          {t('Performance metrics for the last 24 hours')}
        </EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function DimensionPerformanceResults(props: {
  items: PerformanceDimensionItem[]
}) {
  return (
    <>
      <div
        data-testid='dimension-performance-table'
        className='hidden overflow-hidden rounded-xl border sm:block'
      >
        <DimensionPerformanceTable items={props.items} />
      </div>
      <div
        data-testid='dimension-performance-mobile-list'
        className='grid gap-2 sm:hidden'
      >
        {props.items.map((item) => (
          <DimensionPerformanceCard key={item.id} item={item} />
        ))}
      </div>
    </>
  )
}

function DimensionPerformanceTable(props: {
  items: PerformanceDimensionItem[]
}) {
  const { t } = useTranslation()

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Name')}</TableHead>
          <TableHead className='text-right'>{t('Requests')}</TableHead>
          <TableHead className='text-right'>{t('Success rate')}</TableHead>
          <TableHead className='text-right'>
            {t('Request cache hit rate')}
          </TableHead>
          <TableHead className='text-right'>
            {t('Token cache hit rate')}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.items.map((item) => (
          <TableRow key={item.id}>
            <TableCell>
              <DimensionName item={item} />
            </TableCell>
            <TableCell className='text-right font-mono'>
              {formatRequestCount(item.request_count)}
            </TableCell>
            <TableCell
              className={cn(
                'text-right font-mono font-semibold',
                getSuccessRateTextClass(item.success_rate)
              )}
            >
              {formatUptimePct(item.success_rate)}
            </TableCell>
            <TableCell className='text-right font-mono'>
              {formatCacheRate(item.cache_hit_rate, item.cache_eligible_count)}
            </TableCell>
            <TableCell className='text-right font-mono'>
              {formatCacheRate(item.cached_token_rate, item.input_tokens)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function DimensionPerformanceCard(props: { item: PerformanceDimensionItem }) {
  const { t } = useTranslation()
  const item = props.item

  return (
    <article className='bg-muted/30 rounded-xl border p-3'>
      <DimensionName item={item} />
      <dl className='mt-3 grid grid-cols-2 gap-x-3 gap-y-2'>
        <MobileMetric
          label={t('Requests')}
          value={formatRequestCount(item.request_count)}
        />
        <MobileMetric
          label={t('Success rate')}
          value={formatUptimePct(item.success_rate)}
          valueClassName={getSuccessRateTextClass(item.success_rate)}
        />
        <MobileMetric
          label={t('Request cache hit rate')}
          value={formatCacheRate(
            item.cache_hit_rate,
            item.cache_eligible_count
          )}
        />
        <MobileMetric
          label={t('Token cache hit rate')}
          value={formatCacheRate(item.cached_token_rate, item.input_tokens)}
        />
      </dl>
    </article>
  )
}

function DimensionName(props: { item: PerformanceDimensionItem }) {
  return (
    <div className='flex min-w-0 items-center gap-2'>
      <span className='truncate text-sm font-medium' title={props.item.name}>
        {props.item.name}
      </span>
      <span className='text-muted-foreground shrink-0 font-mono text-xs'>
        #{props.item.id}
      </span>
    </div>
  )
}

function MobileMetric(props: {
  label: string
  value: string
  valueClassName?: string
}) {
  return (
    <div className='min-w-0'>
      <dt className='text-muted-foreground truncate text-[11px]'>
        {props.label}
      </dt>
      <dd
        className={cn(
          'mt-0.5 font-mono text-xs font-semibold tabular-nums',
          props.valueClassName
        )}
      >
        {props.value}
      </dd>
    </div>
  )
}
