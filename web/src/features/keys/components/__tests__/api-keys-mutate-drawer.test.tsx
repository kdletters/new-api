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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test } from 'vitest'

import type { ApiKey } from '../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysMutateDrawer } = await import('../api-keys-mutate-drawer')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
  put: ApiMethod
}
type RenderedDrawer = {
  queryClient: InstanceType<typeof QueryClient>
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
const originalPut = apiClient.put
let renderedDrawer: RenderedDrawer | null = null

type UpdateApiFixture = {
  apiKey: ApiKey
  updatedPayloads: Array<Record<string, unknown>>
}

function installApiFixtures(
  createdPayloads: Array<Record<string, unknown>>,
  updateFixture?: UpdateApiFixture
) {
  apiClient.get = async (url) => {
    switch (url) {
      case '/api/status':
        return { data: { data: { default_use_auto_group: true } } }
      case '/api/user/models':
        return { data: { success: true, data: [] } }
      case '/api/user/self/groups':
        return {
          data: {
            success: true,
            data: {
              auto: { desc: 'Automatic routing', ratio: 'auto' },
              default: { desc: 'Standard access', ratio: 1 },
              vip: { desc: 'Priority access', ratio: 2 },
            },
          },
        }
      case '/api/token/auto-groups':
        return {
          data: {
            success: true,
            data: { groups: ['vip', 'default'], max_count: 3 },
          },
        }
      default:
        if (updateFixture && url === `/api/token/${updateFixture.apiKey.id}`) {
          return { data: { success: true, data: updateFixture.apiKey } }
        }
        throw new Error(`Unexpected GET ${url}`)
    }
  }
  apiClient.post = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    createdPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
  if (updateFixture) {
    apiClient.put = async (url, data) => {
      expect(url).toBe('/api/token/')
      expect(data && typeof data === 'object').toBeTruthy()
      updateFixture.updatedPayloads.push(data as Record<string, unknown>)
      return { data: { success: true, data: updateFixture.apiKey } }
    }
  }
}

async function renderDrawer(currentRow?: ApiKey): Promise<void> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(
    ['status'],
    { default_use_auto_group: true },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-models'],
    { success: true, data: [] },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-groups'],
    {
      success: true,
      data: {
        auto: { desc: 'Automatic routing', ratio: 'auto' },
        default: { desc: 'Standard access', ratio: 1 },
        vip: { desc: 'Priority access', ratio: 2 },
      },
    },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['token-auto-groups'],
    {
      success: true,
      data: { groups: ['vip', 'default'], max_count: 3 },
    },
    { updatedAt: freshAt }
  )
  renderedDrawer = { queryClient }

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            onOpenChange={() => undefined}
            currentRow={currentRow}
          />
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  await waitFor(
    () => {
      const saveButton = screen.getByRole('button', { name: 'Save changes' })
      expect(saveButton).toBeEnabled()
    },
    { timeout: 1500 }
  )
}

function findButton(text: string, required: true): HTMLButtonElement
function findButton(text: string, required: false): HTMLButtonElement | null
function findButton(text: string, required = true): HTMLButtonElement | null {
  const button = screen
    .queryAllByRole<HTMLButtonElement>('button')
    .find((candidate) => candidate.textContent?.includes(text))
  if (required && !button) {
    throw new Error(`Expected button containing "${text}"`)
  }
  return button ?? null
}

function getControlByLabel(labelText: 'Name' | 'Quantity'): HTMLInputElement
function getControlByLabel(labelText: 'Group'): HTMLButtonElement
function getControlByLabel(labelText: 'Auto group order'): HTMLElement
function getControlByLabel(labelText: 'Custom Seconds'): HTMLInputElement
function getControlByLabel(
  labelText: 'Unlimited Quota' | 'Reset Cycle'
): HTMLElement
function getControlByLabel(labelText: string): HTMLElement {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  if (!label) {
    throw new Error(`Expected label "${labelText}"`)
  }

  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>(
        '[data-slot="form-control"], input, textarea, button[role="combobox"], [role="group"]'
      )
  if (!control) {
    throw new Error(`Expected control for label "${labelText}"`)
  }
  return control
}

function changeInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

function selectComboboxOption(
  trigger: HTMLButtonElement,
  optionDescription: string
): void {
  fireEvent.click(trigger)
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(optionDescription))
  if (!option) {
    throw new Error(`Expected option containing "${optionDescription}"`)
  }
  fireEvent.click(option)
}

async function selectOption(
  trigger: HTMLElement,
  optionDescription: string
): Promise<void> {
  const user = userEvent.setup()
  await user.click(trigger)
  await user.click(
    await screen.findByRole('option', {
      name: optionDescription,
    })
  )
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  apiClient.put = originalPut
  localStorage.clear()
  if (renderedDrawer) {
    renderedDrawer.queryClient.clear()
    renderedDrawer = null
  }
})

describe('API keys mutate drawer Auto group integration', () => {
  test('inherits the root Auto order and sends an empty override for every batch-created key', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    const groupTrigger = getControlByLabel('Group')
    expect(groupTrigger.textContent?.includes('auto')).toBe(true)
    expect(
      document.body.textContent?.includes(
        'Using the complete global Auto order (2 groups)'
      )
    ).toBe(true)
    expect(
      [
        ...document.querySelectorAll('[data-slot="global-auto-order-name"]'),
      ].map((item) => item.textContent)
    ).toEqual(['vip', 'default'])
    expect(findButton('Restore global Auto', true).disabled).toBe(true)

    changeInput(getControlByLabel('Name'), 'batch')
    changeInput(getControlByLabel('Quantity'), '2')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(2))

    expect(createdPayloads.length).toBe(2)
    expect(createdPayloads[0]?.name).toBe('batch')
    for (const payload of createdPayloads) {
      expect(payload.group).toBe('auto')
      expect(payload.auto_groups).toEqual([])
      expect(payload.cross_group_retry).toBe(true)
    }
  })

  test('preserves an unsaved custom order and mode after Auto to ordinary to Auto changes', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    const autoOrderControl = getControlByLabel('Auto group order')
    const addGroupTrigger = autoOrderControl.querySelector<HTMLButtonElement>(
      'button[role="combobox"]'
    )
    if (!addGroupTrigger) {
      throw new Error('Expected Auto group order combobox')
    }
    selectComboboxOption(addGroupTrigger, 'Priority access')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    const groupTrigger = getControlByLabel('Group')
    selectComboboxOption(groupTrigger, 'Standard access')
    expect(document.querySelector('button[aria-label="Remove vip"]')).toBe(null)
    selectComboboxOption(groupTrigger, 'Automatic routing')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    changeInput(getControlByLabel('Name'), 'custom')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.auto_groups).toEqual(['vip'])
  })
})

describe('API keys mutate drawer user quota query switch', () => {
  test('sends false for a new key after the switch, which starts on, is turned off', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    fireEvent.click(findButton('Advanced Settings', true))
    const quotaQuerySwitch = await screen.findByRole('switch', {
      name: 'Allow querying user quota',
    })
    expect(quotaQuerySwitch).toBeChecked()
    fireEvent.click(quotaQuerySwitch)
    expect(quotaQuerySwitch).not.toBeChecked()

    changeInput(getControlByLabel('Name'), 'no-user-quota')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.allow_user_quota_query).toBe(false)
  })
})

describe('API keys mutate drawer quota reset integration', () => {
  test('configures a custom reset cycle only for limited quotas', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()
    const user = userEvent.setup()

    expect(
      screen.queryByRole('combobox', { name: 'Reset Cycle' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('switch', { name: 'Unlimited Quota' }))

    const resetCycle = screen.getByRole('combobox', { name: 'Reset Cycle' })
    expect(resetCycle).toHaveAccessibleDescription(
      'Choose how often the remaining quota is restored.'
    )
    expect(
      screen.queryByRole('spinbutton', { name: 'Custom Seconds' })
    ).not.toBeInTheDocument()

    await selectOption(resetCycle, 'Custom (seconds)')
    const customSeconds = screen.getByRole<HTMLInputElement>('spinbutton', {
      name: 'Custom Seconds',
    })
    expect(customSeconds).toHaveValue(0)
    expect(customSeconds).toHaveAccessibleDescription(
      'Enter a reset interval greater than 0 seconds.'
    )

    changeInput(screen.getByRole('textbox', { name: 'Name' }), 'scheduled')
    fireEvent.submit(
      document.querySelector<HTMLFormElement>(
        '#api-key-form'
      ) as HTMLFormElement
    )
    await waitFor(() =>
      expect(customSeconds).toHaveAttribute('aria-invalid', 'true')
    )

    changeInput(customSeconds, '3600')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))

    expect(createdPayloads[0]).toMatchObject({
      unlimited_quota: false,
      quota_reset_period: 'custom',
      quota_reset_custom_seconds: 3_600,
    })
  })

  test('clears custom seconds when an existing reset cycle changes', async () => {
    const existingApiKey: ApiKey = {
      id: 7,
      name: 'preloaded reset',
      key: 'demo********1234',
      status: 1,
      remain_quota: 500_000,
      used_quota: 250_000,
      unlimited_quota: false,
      quota_reset_period: 'custom',
      quota_reset_custom_seconds: 3_600,
      last_reset_time: 1_700_000_000,
      next_reset_time: 1_700_003_600,
      expired_time: -1,
      created_time: 1_700_000_000,
      accessed_time: 0,
      group: 'default',
      auto_groups: null,
      cross_group_retry: false,
      model_limits_enabled: false,
      model_limits: '',
      allow_ips: '',
    }
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], { apiKey: existingApiKey, updatedPayloads })
    await renderDrawer(existingApiKey)
    const user = userEvent.setup()

    const resetCycle = screen.getByRole('combobox', { name: 'Reset Cycle' })
    expect(resetCycle).toHaveTextContent('Custom (seconds)')
    expect(
      screen.getByRole('spinbutton', { name: 'Custom Seconds' })
    ).toHaveValue(3_600)

    await selectOption(resetCycle, 'Daily')
    expect(
      screen.queryByRole('spinbutton', { name: 'Custom Seconds' })
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(updatedPayloads).toHaveLength(1))

    expect(updatedPayloads[0]).toMatchObject({
      id: existingApiKey.id,
      quota_reset_period: 'daily',
      quota_reset_custom_seconds: 0,
    })
    expect(updatedPayloads[0]).not.toHaveProperty('remain_quota')
  })
})
