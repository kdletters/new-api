import { render, screen } from '@testing-library/react'
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
import { createRef } from 'react'
import { describe, expect, test, vi } from 'vitest'

import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
} from '../model-pricing-sheet'
import { TieredPricingEditor } from '../tiered-pricing-editor'

// The visual editor's parser only accepts the expression shape it generates
// itself (`tier("label", p * N + c * N [+ cr * ...])`). Expressions authored by
// hand or by the LLM helper (e.g. cache terms before `c`) cannot be parsed.
const UNPARSEABLE_EXPRESSION =
  'len <= 272000 ? tier("0_272k", p * 10 + cr * 1 + cc * 12.5 + c * 50) : tier("272k_plus", p * 20 + cr * 2 + cc * 25 + c * 75)'

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

    // Regression: opening such a model used to replace the saved expression
    // with the empty template `p * 0 + c * 0`, zeroing the price on save.
    expect(onBillingExprChange).not.toHaveBeenCalled()
    expect(onRequestRuleExprChange).not.toHaveBeenCalled()
    expect(screen.getByDisplayValue(UNPARSEABLE_EXPRESSION)).toBeVisible()
  })

  test('opens a representable expression in the visual editor unchanged', () => {
    const expression = 'tier("base", p * 3 + c * 15)'
    const { onBillingExprChange } = renderEditor(expression)

    expect(onBillingExprChange).not.toHaveBeenCalled()
    expect(screen.queryByDisplayValue(expression)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add tier' })).toBeVisible()
  })

  test('commits an unparseable expression unchanged', async () => {
    const ref = createRef<ModelPricingEditorPanelHandle>()

    render(
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
    )

    const data = await ref.current?.commitDraft()

    expect(data?.billingExpr).toBe(UNPARSEABLE_EXPRESSION)
  })
})
