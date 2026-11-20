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
import { expect, test } from 'vitest'

import { channelSchema, type Channel } from '../../types'
import {
  channelNeedsAttention,
  getAttentionReason,
  getBalanceVariant,
  getKnownChannelBalance,
} from '../channel-utils'

function makeChannel(balance: number, balanceUpdatedTime: number): Channel {
  return channelSchema.parse({
    id: 1,
    type: 1,
    key: 'test-key',
    name: 'Test Channel',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance,
    balance_updated_time: balanceUpdatedTime,
  })
}

test('does not present an unqueried zero balance as a real balance', () => {
  const channel = makeChannel(0, 0)

  expect(getKnownChannelBalance(channel)).toBeNull()
  expect(channelNeedsAttention(channel)).toBe(false)
})

test('keeps a real zero balance distinguishable from an unqueried balance', () => {
  const channel = makeChannel(0, 1)

  expect(getKnownChannelBalance(channel)).toBe(0)
  expect(channelNeedsAttention(channel)).toBe(true)
})

test('preserves a negative outstanding balance', () => {
  const channel = makeChannel(-1, 1)

  expect(getKnownChannelBalance(channel)).toBe(-1)
  expect(getBalanceVariant(channel.balance)).toBe('danger')
  expect(channelNeedsAttention(channel)).toBe(true)
  expect(getAttentionReason(channel)).toBe('Low balance')
})
