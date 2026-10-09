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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster, toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ChannelGroupBatchDialog } from '../dialogs/channel-group-batch-dialog'

const clients: QueryClient[] = []

function setup() {
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: ['default', 'vip'] },
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockImplementation(async () => ({ data: { success: true, data: 2 } }))
  const onSuccess = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <ChannelGroupBatchDialog
        open
        onOpenChange={() => {}}
        channelIds={[11, 22]}
        onSuccess={onSuccess}
      />
      <Toaster />
    </QueryClientProvider>
  )
  return { user: userEvent.setup(), post, onSuccess }
}

afterEach(() => {
  toast.dismiss()
  for (const client of clients) client.clear()
  clients.length = 0
  vi.restoreAllMocks()
})

test('bulk channel group change submits the chosen groups for every channel', async () => {
  const { user, post, onSuccess } = setup()

  await user.click(
    await screen.findByRole('combobox', { name: 'Select groups' })
  )
  await user.click(await screen.findByRole('option', { name: 'vip' }))
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  const [url, body] = post.mock.calls[0]
  expect(url).toBe('/api/channel/batch/group')
  expect(body).toEqual({ ids: [11, 22], group: 'vip' })
  await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
})

test('bulk channel group change stays disabled until a group is selected', async () => {
  const { post } = setup()

  const confirm = screen.getByRole('button', { name: 'Confirm' })
  expect(confirm).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})
