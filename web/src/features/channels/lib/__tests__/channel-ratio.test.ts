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
import { describe, expect, it } from 'vitest'

import { buildSettingJSON, CHANNEL_FORM_DEFAULT_VALUES } from '../channel-form'

describe('channel ratio settings', () => {
  it('writes a configured channel ratio into the stored setting JSON', () => {
    const setting = JSON.parse(
      buildSettingJSON({ ...CHANNEL_FORM_DEFAULT_VALUES, channel_ratio: 1.25 })
    ) as Record<string, unknown>

    expect(setting.channel_ratio).toBe(1.25)
  })

  it('keeps the neutral ratio so saving the drawer never drops the key', () => {
    const setting = JSON.parse(
      buildSettingJSON({ ...CHANNEL_FORM_DEFAULT_VALUES })
    ) as Record<string, unknown>

    expect(setting.channel_ratio).toBe(1)
  })
})
