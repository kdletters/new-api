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
import { useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getGroups, updateUser } from '../api'
import type { User } from '../types'

interface UsersBulkGroupDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  users: User[]
  onSuccess: () => void
}

export function UsersBulkGroupDialog(props: UsersBulkGroupDialogProps) {
  const { t } = useTranslation()
  const groupFieldId = useId()
  const [group, setGroup] = useState('')
  const [saving, setSaving] = useState(false)

  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 5 * 60 * 1000,
    enabled: props.open,
  })
  const groups = groupsData?.data ?? []

  const handleOpenChange = (open: boolean) => {
    if (!open) setGroup('')
    props.onOpenChange(open)
  }

  const handleConfirm = async () => {
    if (!group || props.users.length === 0) return
    setSaving(true)
    try {
      const failures: unknown[] = []
      let updated = 0
      await Promise.all(
        props.users.map(async (user) => {
          try {
            const result = await updateUser({
              id: user.id,
              username: user.username,
              display_name: user.display_name,
              group,
              remark: user.remark ?? '',
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
          t('Failed to update group for {{count}} user(s)', {
            count: failures.length,
          })
        )
      } else {
        toast.success(
          t('Updated group for {{count}} user(s)', { count: updated })
        )
      }
      if (updated > 0) props.onSuccess()
      handleOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={t('Set group')}
      description={t('Apply a group to the selected users.')}
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
          <Button onClick={handleConfirm} disabled={saving || !group}>
            {saving ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label htmlFor={groupFieldId}>{t('Group')}</Label>
        <Combobox
          id={groupFieldId}
          options={groups.map((item) => ({ value: item, label: item }))}
          value={group}
          onValueChange={(value) => setGroup(value ?? '')}
          className='w-full'
          placeholder={t('Select a group')}
        />
      </div>
    </Dialog>
  )
}
