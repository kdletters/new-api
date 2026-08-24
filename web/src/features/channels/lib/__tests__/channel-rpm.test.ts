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
import { describe, expect, test } from 'vitest'

import type { Channel } from '../../types'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
} from '../channel-form'

describe('channel RPM limit', () => {
  test('round-trips setting.rpm through the channel form', () => {
    const channel = {
      id: 7,
      type: 1,
      status: 1,
      created_time: 0,
      test_time: 0,
      response_time: 0,
      other: '',
      balance: 0,
      balance_updated_time: 0,
      models: 'gpt-5',
      group: 'default',
      used_quota: 0,
      setting: JSON.stringify({ rpm: 120 }),
      remark: '',
      max_input_tokens: 0,
      channel_info: {
        is_multi_key: false,
        multi_key_size: 0,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
      settings: '{}',
      name: 'limited channel',
    } as Channel

    const defaults = transformChannelToFormDefaults(channel)

    expect(defaults.rpm).toBe(120)
    expect(JSON.parse(buildSettingJSON(defaults))).toMatchObject({ rpm: 120 })
  })

  test('accepts zero as unlimited and rejects negative limits', () => {
    const validForm = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'limited channel',
      key: 'test-key',
      models: 'gpt-5',
    }
    expect(channelFormSchema.safeParse({ ...validForm, rpm: 0 }).success).toBe(
      true
    )
    expect(channelFormSchema.safeParse({ ...validForm, rpm: -1 }).success).toBe(
      false
    )
  })
})
