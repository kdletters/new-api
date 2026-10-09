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

import { useDataTable } from '@/components/data-table'
import { api } from '@/lib/api'

import type { User } from '../../types'
import { DataTableBulkActions } from '../data-table-bulk-actions'
import { UsersProvider } from '../users-provider'

const users: User[] = [
  {
    id: 11,
    username: 'alice',
    display_name: 'Alice',
    quota: 100,
    used_quota: 0,
    request_count: 0,
    group: 'default',
    status: 1,
    role: 1,
    remark: '',
  },
  {
    id: 22,
    username: 'bob',
    display_name: 'Bob',
    quota: 200,
    used_quota: 0,
    request_count: 0,
    group: 'default',
    status: 1,
    role: 1,
    remark: '',
  },
]

const clients: QueryClient[] = []

function UserList() {
  const { table } = useDataTable({
    data: users,
    columns: [{ accessorKey: 'username' }],
    enableRowSelection: true,
    getRowId: (row) => String(row.id),
  })
  return (
    <>
      {table.getRowModel().rows.map((row) => (
        <label key={row.id}>
          <input
            type='checkbox'
            checked={row.getIsSelected()}
            onChange={row.getToggleSelectedHandler()}
          />
          {row.original.username}
        </label>
      ))}
      <DataTableBulkActions table={table} />
    </>
  )
}

function setup() {
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: ['default', 'vip'] },
  }))
  const put = vi
    .spyOn(api, 'put')
    .mockImplementation(async () => ({ data: { success: true } }))
  const post = vi
    .spyOn(api, 'post')
    .mockImplementation(async () => ({ data: { success: true } }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UserList />
      </UsersProvider>
      <Toaster />
    </QueryClientProvider>
  )
  return { user: userEvent.setup(), put, post }
}

async function selectBothUsers() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('checkbox', { name: 'alice' }))
  await user.click(screen.getByRole('checkbox', { name: 'bob' }))
  return user
}

afterEach(() => {
  toast.dismiss()
  for (const client of clients) client.clear()
  clients.length = 0
  vi.restoreAllMocks()
})

test('bulk group update applies the chosen group to every selected user', async () => {
  const { put } = setup()
  const user = await selectBothUsers()

  await user.click(screen.getByRole('button', { name: 'Set group' }))
  await user.click(await screen.findByRole('combobox', { name: 'Group' }))
  await user.click(await screen.findByRole('option', { name: 'vip' }))
  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  const payloads = put.mock.calls.map(
    (call) => call[1] as { id: number; group?: string }
  )
  expect(payloads.map((payload) => payload.id).sort()).toEqual([11, 22])
  expect(payloads.every((payload) => payload.group === 'vip')).toBe(true)
})

test('bulk quota adjustment applies the amount to every selected user', async () => {
  const { post } = setup()
  const user = await selectBothUsers()

  await user.click(screen.getByRole('button', { name: 'Set quota' }))
  await user.type(
    await screen.findByRole('spinbutton', { name: /amount/i }),
    '5'
  )
  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  const payloads = post.mock.calls.map(
    (call) =>
      call[1] as {
        id: number
        action: string
        mode: string
        value: number
      }
  )
  expect(payloads.map((payload) => payload.id).sort()).toEqual([11, 22])
  expect(
    payloads.every(
      (payload) =>
        payload.action === 'add_quota' &&
        payload.mode === 'add' &&
        payload.value > 0
    )
  ).toBe(true)
})
