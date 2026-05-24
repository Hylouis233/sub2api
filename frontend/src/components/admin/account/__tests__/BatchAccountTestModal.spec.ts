import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BatchAccountTestModal from '../BatchAccountTestModal.vue'

const { getAvailableModels, batchTest } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  batchTest: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels,
      batchTest
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (params?.count !== undefined) return `${key}:${params.count}`
        return key
      }
    })
  }
})

function mountModal() {
  return mount(BatchAccountTestModal, {
    props: {
      show: true,
      accountIds: [1, 2],
      accounts: [
        { id: 1, name: 'Account One', platform: 'openai', type: 'oauth', status: 'active' },
        { id: 2, name: 'Account Two', platform: 'openai', type: 'apikey', status: 'active' }
      ]
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: `
            <select data-test="model-select" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
              <option v-for="option in options" :key="option.id" :value="option.id">{{ option.display_name }}</option>
            </select>
          `
        },
        Icon: true
      }
    }
  })
}

describe('BatchAccountTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockReset()
    batchTest.mockReset()
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT 5.4' },
      { id: 'gpt-5.5', display_name: 'GPT 5.5' }
    ])
    batchTest.mockResolvedValue({
      total: 2,
      success: 1,
      failed: 1,
      items: [
        { account_id: 1, name: 'Account One', platform: 'openai', type: 'oauth', success: true, status: 'success', response_text: 'ok', latency_ms: 120 },
        { account_id: 2, name: 'Account Two', platform: 'openai', type: 'apikey', success: false, status: 'failed', error: '401 unauthorized', latency_ms: 80 }
      ]
    })
  })

  it('loads model suggestions and posts selected accounts with the chosen model', async () => {
    const wrapper = mountModal()
    await flushPromises()

    expect(getAvailableModels).toHaveBeenCalledWith(1)
    await wrapper.get('[data-test="model-select"]').setValue('gpt-5.5')

    const startButton = wrapper.findAll('button').find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()

    expect(batchTest).toHaveBeenCalledWith([1, 2], 'gpt-5.5')
    expect(wrapper.text()).toContain('Account One')
    expect(wrapper.text()).toContain('401 unauthorized')
  })
})
