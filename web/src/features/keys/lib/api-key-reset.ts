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
import type { TFunction } from 'i18next'

import type { ApiKey } from '../types'

const RESET_POLL_MAX_INTERVAL_MS = 60_000
const RESET_POLL_DUE_INTERVAL_MS = 10_000

const DURATION_UNITS = [
  { seconds: 86_400, singular: 'day', plural: 'days' },
  { seconds: 3_600, singular: 'hour', plural: 'hours' },
  { seconds: 60, singular: 'minute', plural: 'minutes' },
  { seconds: 1, singular: 'second', plural: 'seconds' },
] as const

export function getApiKeysResetRefetchInterval(
  apiKeys: readonly ApiKey[],
  nowMs = Date.now()
): number | false {
  let nearestResetTime = Number.POSITIVE_INFINITY

  for (const apiKey of apiKeys) {
    if (
      apiKey.unlimited_quota ||
      (apiKey.quota_reset_period || 'never') === 'never' ||
      apiKey.next_reset_time <= 0
    ) {
      continue
    }
    nearestResetTime = Math.min(nearestResetTime, apiKey.next_reset_time)
  }

  if (!Number.isFinite(nearestResetTime)) {
    return false
  }

  if (nearestResetTime * 1000 <= nowMs) {
    return RESET_POLL_DUE_INTERVAL_MS
  }
  return RESET_POLL_MAX_INTERVAL_MS
}

export function formatApiKeyResetPeriod(apiKey: ApiKey, t: TFunction): string {
  switch (apiKey.quota_reset_period || 'never') {
    case 'daily':
      return t('Daily')
    case 'weekly':
      return t('Weekly')
    case 'monthly':
      return t('Monthly')
    case 'custom':
      return formatExactDuration(apiKey.quota_reset_custom_seconds, t)
    case 'never':
      return t('No Reset')
  }
}

function formatExactDuration(seconds: number, t: TFunction): string {
  if (!Number.isFinite(seconds) || seconds < 0 || !Number.isInteger(seconds)) {
    return `${seconds} ${t('seconds')}`
  }

  const parts: string[] = []
  let remainingSeconds = seconds
  for (const unit of DURATION_UNITS) {
    const count = Math.floor(remainingSeconds / unit.seconds)
    if (count === 0) continue

    parts.push(`${count} ${t(count === 1 ? unit.singular : unit.plural)}`)
    remainingSeconds -= count * unit.seconds
  }

  if (parts.length === 0) {
    return `0 ${t('seconds')}`
  }
  return parts.join(' ')
}
