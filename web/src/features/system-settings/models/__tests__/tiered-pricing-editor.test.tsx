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
import { render, screen } from '@testing-library/react'
import { createRef } from 'react'
import { describe, expect, test, vi } from 'vitest'

import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
} from '../model-pricing-sheet'
import { TieredPricingEditor } from '../tiered-pricing-editor'

// Hand-authored and LLM-assisted billing expressions can use token variables the
// visual editor cannot represent (here: audio input tokens). Opening such a model
// must keep the stored expression verbatim instead of replacing it with the
// empty `p * 0 + c * 0` template, which used to zero the price on save.
const UNPARSEABLE_EXPRESSION = 'p * 1 + c * 2 + ai * 3'

function renderEditor(billingExpr: string) {
  const onBillingExprChange = vi.fn()
  const onRequestRuleExprChange = vi.fn()

  render(
    <TieredPricingEditor
      modelName='gpt-6-astra'
      billingExpr={billingExpr}
      requestRuleExpr=''
      onBillingExprChange={onBillingExprChange}
      onRequestRuleExprChange={onRequestRuleExprChange}
    />
  )

  return { onBillingExprChange, onRequestRuleExprChange }
}

describe('tiered pricing editor', () => {
  test('keeps an expression the visual editor cannot parse', () => {
    const { onBillingExprChange, onRequestRuleExprChange } = renderEditor(
      UNPARSEABLE_EXPRESSION
    )

    expect(onBillingExprChange).not.toHaveBeenCalled()
    expect(onRequestRuleExprChange).not.toHaveBeenCalled()
    expect(screen.getByDisplayValue(UNPARSEABLE_EXPRESSION)).toBeVisible()
  })

  test('commits an unparseable expression unchanged', async () => {
    const ref = createRef<ModelPricingEditorPanelHandle>()
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelPricingEditorPanel
          ref={ref}
          editData={{
            name: 'gpt-6-astra',
            billingMode: 'tiered_expr',
            billingExpr: UNPARSEABLE_EXPRESSION,
            requestRuleExpr: '',
          }}
          isSaving={false}
        />
      </QueryClientProvider>
    )

    const data = await ref.current?.commitDraft()

    expect(data?.billingExpr).toBe(UNPARSEABLE_EXPRESSION)
  })
})
