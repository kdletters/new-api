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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { parseQuotaFromDollars, quotaUnitsToEditableAmount } from '@/lib/format'

import { OverrideSubscriptionsDialog } from '../override-subscriptions-dialog'

const { overridePlanSubscriptions, setOpen, triggerRefresh } = vi.hoisted(
  () => ({
    overridePlanSubscriptions: vi.fn(),
    setOpen: vi.fn(),
    triggerRefresh: vi.fn(),
  })
)

vi.mock('../../../api', () => ({ overridePlanSubscriptions }))

vi.mock('../../subscriptions-provider', () => ({
  useSubscriptions: () => ({
    open: 'override-subscriptions',
    setOpen,
    currentRow: { plan: { id: 7, title: 'Pro', total_amount: 500000 } },
    triggerRefresh,
    complianceConfirmed: true,
  }),
}))

const PLAN_TOTAL = 500000

function amountInput() {
  return screen.getByLabelText('Total Quota')
}

function confirmButton() {
  return screen.getByRole('button', { name: 'Override quota' })
}

beforeEach(() => {
  overridePlanSubscriptions.mockReset()
  setOpen.mockReset()
  triggerRefresh.mockReset()
})

describe('OverrideSubscriptionsDialog', () => {
  test('prefills the plan quota and submits the converted amount', async () => {
    overridePlanSubscriptions.mockResolvedValue({
      success: true,
      data: {
        plan_id: 7,
        amount_total: PLAN_TOTAL,
        updated_count: 3,
        user_count: 2,
      },
    })
    const user = userEvent.setup()
    render(<OverrideSubscriptionsDialog />)

    const prefilled = quotaUnitsToEditableAmount(PLAN_TOTAL)
    expect(amountInput()).toHaveValue(prefilled)

    await user.click(confirmButton())

    await waitFor(() =>
      expect(overridePlanSubscriptions).toHaveBeenCalledWith(7, {
        amount_total: parseQuotaFromDollars(prefilled),
      })
    )
    expect(triggerRefresh).toHaveBeenCalledTimes(1)
    expect(setOpen).toHaveBeenCalledWith(null)
  })

  test('disables submit while the amount is empty or negative', async () => {
    const user = userEvent.setup()
    render(<OverrideSubscriptionsDialog />)

    await user.clear(amountInput())
    expect(confirmButton()).toBeDisabled()

    await user.type(amountInput(), '-5')
    expect(confirmButton()).toBeDisabled()
    expect(overridePlanSubscriptions).not.toHaveBeenCalled()
  })
})
