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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { adjustUserQuota } from '../api'
import type { QuotaAdjustMode, User } from '../types'

interface UsersBulkQuotaDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  users: User[]
  onSuccess: () => void
}

export function UsersBulkQuotaDialog(props: UsersBulkQuotaDialogProps) {
  const { t } = useTranslation()
  const amountFieldId = useId()
  const [mode, setMode] = useState<QuotaAdjustMode>('add')
  const [amount, setAmount] = useState('')
  const [saving, setSaving] = useState(false)

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const parsedAmount = Number.parseFloat(amount)
  const hasAmount = amount.trim() !== '' && Number.isFinite(parsedAmount)
  const quotaValue = hasAmount
    ? parseQuotaFromDollars(Math.abs(parsedAmount))
    : 0
  const canConfirm =
    hasAmount && (mode === 'override' ? parsedAmount >= 0 : quotaValue > 0)

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      setMode('add')
      setAmount('')
    }
    props.onOpenChange(open)
  }

  const handleConfirm = async () => {
    if (!canConfirm || props.users.length === 0) return
    setSaving(true)
    try {
      const value =
        mode === 'override' ? parseQuotaFromDollars(parsedAmount) : quotaValue
      const failures: unknown[] = []
      let updated = 0
      await Promise.all(
        props.users.map(async (user) => {
          try {
            const result = await adjustUserQuota({
              id: user.id,
              action: 'add_quota',
              mode,
              value,
            })
            if (result.success) {
              updated += 1
            } else {
              failures.push(result)
            }
          } catch (error) {
            failures.push(error)
          }
        })
      )
      if (failures.length > 0) {
        handleServerError(
          failures[0],
          t('Failed to adjust quota for {{count}} user(s)', {
            count: failures.length,
          })
        )
      } else {
        toast.success(
          t('Adjusted quota for {{count}} user(s)', { count: updated })
        )
      }
      if (updated > 0) props.onSuccess()
      handleOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  const placeholder = tokensOnly
    ? t('Enter amount in tokens')
    : t('Enter amount in {{currency}}', { currency: currencyLabel })

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={t('Set quota')}
      description={t('Adjust the quota of the selected users.')}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => handleOpenChange(false)}
            disabled={saving}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={saving || !canConfirm}>
            {saving ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Mode')}</Label>
          <div className='flex gap-1'>
            {(['add', 'subtract', 'override'] as const).map((item) => (
              <Button
                key={item}
                type='button'
                variant='outline'
                size='sm'
                className={cn(
                  mode === item &&
                    'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'
                )}
                onClick={() => {
                  setMode(item)
                  setAmount('')
                }}
              >
                {item === 'add' && t('Add')}
                {item === 'subtract' && t('Subtract')}
                {item === 'override' && t('Override')}
              </Button>
            ))}
          </div>
        </div>

        <div className='space-y-2'>
          <Label htmlFor={amountFieldId}>
            {t('Amount')} ({currencyLabel})
          </Label>
          <Input
            id={amountFieldId}
            type='number'
            step={tokensOnly ? 1 : 0.000001}
            min={mode === 'override' ? undefined : 0}
            placeholder={placeholder}
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') void handleConfirm()
            }}
          />
        </div>
      </div>
    </Dialog>
  )
}
