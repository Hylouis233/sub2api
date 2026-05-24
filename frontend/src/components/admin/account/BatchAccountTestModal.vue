<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.bulkTest.title')"
    width="wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-600 dark:bg-dark-700/60">
        <div class="flex items-center gap-3">
          <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary-500 text-white">
            <Icon name="play" size="sm" :stroke-width="2" />
          </div>
          <div>
            <div class="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {{ t('admin.accounts.bulkTest.selected', { count: accountIds.length }) }}
            </div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ platformSummary }}
            </div>
          </div>
        </div>

        <div v-if="hasResults" class="flex items-center gap-2 text-xs">
          <span class="rounded-full bg-green-100 px-2 py-1 font-medium text-green-700 dark:bg-green-500/20 dark:text-green-300">
            {{ t('admin.accounts.bulkTest.successCount', { count: successCount }) }}
          </span>
          <span class="rounded-full bg-red-100 px-2 py-1 font-medium text-red-700 dark:bg-red-500/20 dark:text-red-300">
            {{ t('admin.accounts.bulkTest.failedCount', { count: failedCount }) }}
          </span>
        </div>
      </div>

      <div class="grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.testModel') }}
          </label>
          <Select
            v-model="selectedModelId"
            :options="availableModels"
            :disabled="loadingModels || isTesting"
            value-key="id"
            label-key="display_name"
            :placeholder="loadingModels ? t('admin.accounts.bulkTest.loadingModels') : t('admin.accounts.bulkTest.modelPlaceholder')"
            searchable
            creatable
            :creatable-prefix="t('admin.accounts.bulkTest.useModel')"
          />
        </div>
        <button
          type="button"
          class="btn btn-primary flex h-10 items-center gap-2 px-4"
          :disabled="isTesting || !canStart"
          @click="startBatchTest"
        >
          <Icon v-if="isTesting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          <Icon v-else name="play" size="sm" :stroke-width="2" />
          <span>{{ isTesting ? t('admin.accounts.bulkTest.testing') : startButtonLabel }}</span>
        </button>
      </div>

      <div
        v-if="status === 'error'"
        class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
      >
        {{ errorMessage }}
      </div>

      <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
        <div class="max-h-[360px] overflow-y-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
            <thead class="sticky top-0 bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-700 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2 text-left font-semibold">{{ t('admin.accounts.bulkTest.columns.account') }}</th>
                <th class="px-3 py-2 text-left font-semibold">{{ t('admin.accounts.bulkTest.columns.type') }}</th>
                <th class="px-3 py-2 text-left font-semibold">{{ t('admin.accounts.bulkTest.columns.status') }}</th>
                <th class="px-3 py-2 text-left font-semibold">{{ t('admin.accounts.bulkTest.columns.latency') }}</th>
                <th class="px-3 py-2 text-left font-semibold">{{ t('admin.accounts.bulkTest.columns.message') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
              <tr v-for="item in displayRows" :key="item.account_id">
                <td class="px-3 py-2">
                  <div class="font-medium text-gray-900 dark:text-gray-100">{{ item.name || `#${item.account_id}` }}</div>
                  <div class="text-xs text-gray-500 dark:text-gray-400">#{{ item.account_id }}</div>
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">
                  <div class="flex flex-wrap gap-1">
                    <span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-700">{{ item.platform || '-' }}</span>
                    <span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-700">{{ item.type || '-' }}</span>
                  </div>
                </td>
                <td class="px-3 py-2">
                  <span :class="['inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-medium', statusClass(item)]">
                    <Icon v-if="itemStatus(item) === 'testing'" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
                    <Icon v-else-if="item.success" name="check" size="sm" :stroke-width="2" />
                    <Icon v-else-if="itemStatus(item) === 'failed'" name="x" size="sm" :stroke-width="2" />
                    {{ statusLabel(item) }}
                  </span>
                </td>
                <td class="px-3 py-2 font-mono text-xs text-gray-600 dark:text-gray-300">
                  {{ formatLatency(item.latency_ms) }}
                </td>
                <td class="max-w-[280px] px-3 py-2">
                  <div
                    class="truncate text-gray-600 dark:text-gray-300"
                    :title="item.error || item.response_text || ''"
                  >
                    {{ item.error || item.response_text || '-' }}
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button
          type="button"
          class="rounded-lg bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
          @click="handleClose"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import type { Account, ClaudeModel } from '@/types'
import type { BatchAccountTestItem } from '@/api/admin/accounts'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  accountIds: number[]
  accounts: Account[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

type ModalStatus = 'idle' | 'testing' | 'done' | 'error'

const status = ref<ModalStatus>('idle')
const availableModels = ref<ClaudeModel[]>([])
const selectedModelId = ref('')
const loadingModels = ref(false)
const errorMessage = ref('')
const results = ref<BatchAccountTestItem[]>([])

const selectedAccounts = computed(() => {
  const byID = new Map(props.accounts.map((account) => [account.id, account]))
  return props.accountIds.map((id) => byID.get(id)).filter((account): account is Account => !!account)
})

const platformSummary = computed(() => {
  const platforms = Array.from(new Set(selectedAccounts.value.map((account) => account.platform))).filter(Boolean)
  return platforms.length > 0 ? platforms.join(' / ') : t('admin.accounts.bulkTest.noPlatformSummary')
})

const isTesting = computed(() => status.value === 'testing')
const canStart = computed(() => props.accountIds.length > 0 && selectedModelId.value.trim().length > 0)
const hasResults = computed(() => results.value.length > 0 && status.value !== 'testing')
const successCount = computed(() => results.value.filter((item) => item.success).length)
const failedCount = computed(() => results.value.filter((item) => !item.success).length)
const startButtonLabel = computed(() => (results.value.length > 0 ? t('admin.accounts.retry') : t('admin.accounts.startTest')))

const displayRows = computed<BatchAccountTestItem[]>(() => {
  if (results.value.length > 0) return results.value

  const byID = new Map(selectedAccounts.value.map((account) => [account.id, account]))
  return props.accountIds.map((id) => {
    const account = byID.get(id)
    return {
      account_id: id,
      name: account?.name,
      platform: account?.platform,
      type: account?.type,
      success: false,
      status: isTesting.value ? 'testing' : 'pending'
    }
  })
})

const resetState = () => {
  status.value = 'idle'
  results.value = []
  errorMessage.value = ''
  selectedModelId.value = ''
  availableModels.value = []
}

const normalizeModels = (models: ClaudeModel[]) => {
  return models.map((model) => ({
    ...model,
    display_name: model.display_name || model.id
  }))
}

const pickDefaultModel = (models: ClaudeModel[]) => {
  const sonnetModel = models.find((model) => model.id.includes('sonnet'))
  return sonnetModel?.id || models[0]?.id || ''
}

const loadModelSuggestions = async () => {
  const firstAccount = selectedAccounts.value[0]
  if (!firstAccount) return

  loadingModels.value = true
  try {
    const models = await adminAPI.accounts.getAvailableModels(firstAccount.id)
    availableModels.value = normalizeModels(models)
    selectedModelId.value = pickDefaultModel(availableModels.value)
  } catch (error) {
    console.error('Failed to load batch test model suggestions:', error)
    availableModels.value = []
  } finally {
    loadingModels.value = false
  }
}

watch(
  () => props.show,
  async (shown) => {
    if (shown) {
      resetState()
      await loadModelSuggestions()
    }
  },
  { immediate: true }
)

const startBatchTest = async () => {
  if (!canStart.value || isTesting.value) return

  status.value = 'testing'
  errorMessage.value = ''
  results.value = []

  try {
    const result = await adminAPI.accounts.batchTest([...props.accountIds], selectedModelId.value.trim())
    results.value = result.items
    status.value = 'done'
  } catch (error) {
    console.error('Failed to batch test accounts:', error)
    status.value = 'error'
    errorMessage.value = error instanceof Error ? error.message : String(error || t('common.error'))
  }
}

const itemStatus = (item: BatchAccountTestItem) => {
  if (isTesting.value && (item.status === 'pending' || item.status === 'testing')) return 'testing'
  if (item.success) return 'success'
  return item.status || 'pending'
}

const statusLabel = (item: BatchAccountTestItem) => {
  const current = itemStatus(item)
  if (current === 'testing') return t('admin.accounts.bulkTest.status.testing')
  if (current === 'success') return t('admin.accounts.bulkTest.status.success')
  if (current === 'failed') return t('admin.accounts.bulkTest.status.failed')
  return t('admin.accounts.bulkTest.status.pending')
}

const statusClass = (item: BatchAccountTestItem) => {
  const current = itemStatus(item)
  if (current === 'testing') return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-500/20 dark:text-yellow-300'
  if (current === 'success') return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-300'
  if (current === 'failed') return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}

const formatLatency = (latency?: number) => {
  return typeof latency === 'number' && latency > 0 ? `${latency}ms` : '-'
}

const handleClose = () => {
  emit('close')
}
</script>
