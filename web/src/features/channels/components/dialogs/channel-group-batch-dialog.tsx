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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getGroups } from '../../api'
import { handleBatchSetGroup } from '../../lib'

interface ChannelGroupBatchDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelIds: number[]
  onSuccess: () => void
}

export function ChannelGroupBatchDialog(props: ChannelGroupBatchDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const groupFieldId = useId()
  const [groups, setGroups] = useState<string[]>([])
  const [isSaving, setIsSaving] = useState(false)

  const { data: groupsData, isLoading } = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    enabled: props.open,
  })

  const groupOptions = useMemo(
    () =>
      (groupsData?.data ?? []).map((group) => ({
        value: group,
        label: group,
      })),
    [groupsData]
  )

  const handleOpenChange = (open: boolean) => {
    if (!open) setGroups([])
    props.onOpenChange(open)
  }

  const handleConfirm = async () => {
    if (props.channelIds.length === 0 || groups.length === 0) return
    setIsSaving(true)
    try {
      await handleBatchSetGroup(
        props.channelIds,
        groups.join(','),
        queryClient,
        () => {
          props.onSuccess()
          handleOpenChange(false)
        }
      )
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={t('Set group')}
      description={t('Apply a group to the selected channels.')}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => handleOpenChange(false)}
            disabled={isSaving}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={isSaving || groups.length === 0}
          >
            {t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label htmlFor={groupFieldId}>{t('Groups')}</Label>
        {isLoading ? (
          <Skeleton className='h-10 w-full' />
        ) : (
          <MultiSelect
            id={groupFieldId}
            options={groupOptions}
            selected={groups}
            onChange={setGroups}
            placeholder={t('Select groups')}
          />
        )}
        <p className='text-muted-foreground text-xs'>
          {t('User groups that can access the selected channels.')}
        </p>
      </div>
    </Dialog>
  )
}
