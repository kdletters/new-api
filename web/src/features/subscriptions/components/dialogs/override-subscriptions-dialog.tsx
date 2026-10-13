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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToEditableAmount,
} from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { overridePlanSubscriptions } from '../../api'
import { useSubscriptions } from '../subscriptions-provider'

export function OverrideSubscriptionsDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useSubscriptions()
  const [amount, setAmount] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const isOpen = open === 'override-subscriptions'
  const plan = currentRow?.plan
  const planLabel = plan?.title || (plan?.id ? `#${plan.id}` : '-')
  const { meta: currencyMeta } = getCurrencyDisplay()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const currencyLabel = getCurrencyLabel()

  useEffect(() => {
    if (isOpen) {
      setAmount(
        String(quotaUnitsToEditableAmount(Number(plan?.total_amount || 0)))
      )
    }
  }, [isOpen, plan?.total_amount])

  const parsedAmount = Number(amount)
  const isValidAmount =
    amount.trim() !== '' && Number.isFinite(parsedAmount) && parsedAmount >= 0

  const handleConfirm = async () => {
    if (!plan?.id || !isValidAmount) return
    setSubmitting(true)
    try {
      const res = await overridePlanSubscriptions(plan.id, {
        amount_total: parseQuotaFromDollars(parsedAmount),
      })
      if (res.success) {
        toast.success(
          t('Override {{count}} active subscriptions', {
            count: res.data?.updated_count || 0,
          })
        )
        triggerRefresh()
        setOpen(null)
      } else {
        handleServerError(res)
      }
    } catch (error) {
      handleServerError(error, t('Operation failed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <ConfirmDialog
      open={isOpen}
      onOpenChange={(nextOpen) => !nextOpen && setOpen(null)}
      title={t('Override subscription quota')}
      desc={t(
        'Set the total quota for all active subscriptions under {{plan}}.',
        {
          plan: planLabel,
        }
      )}
      confirmText={t('Override quota')}
      handleConfirm={handleConfirm}
      disabled={!plan?.id || !isValidAmount}
      isLoading={submitting}
    >
      <div className='grid gap-2'>
        <Label htmlFor='override-subscriptions-amount'>
          {t('Total Quota')}
        </Label>
        <Input
          id='override-subscriptions-amount'
          type='number'
          min={0}
          step={getEditableQuotaStep()}
          value={amount}
          placeholder={
            tokensOnly
              ? t('Enter amount in tokens')
              : t('Enter amount in {{currency}}', { currency: currencyLabel })
          }
          onChange={(e) => setAmount(e.target.value)}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Total quota included in the plan, usable per billing period. 0 means unlimited.'
          )}
        </p>
      </div>
    </ConfirmDialog>
  )
}
