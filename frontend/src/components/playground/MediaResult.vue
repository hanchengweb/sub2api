<template>
  <figure class="media-result" :aria-busy="loading">
    <div v-if="loading" class="media-status" role="status">
      <LoadingSpinner size="sm" />
      <span>{{ t('playground.loadingMedia') }}</span>
    </div>
    <div v-else-if="error" class="media-status" role="alert">
      <Icon name="exclamationCircle" size="md" />
      <p>{{ error }}</p>
      <button type="button" class="btn btn-secondary" @click="load">
        <Icon name="refresh" size="sm" />{{ t('playground.retryMedia') }}
      </button>
    </div>
    <template v-else-if="objectUrl">
      <video v-if="isVideo" :src="objectUrl" controls preload="metadata" class="result-image" @error="decodeFailed" />
      <button v-else type="button" class="block w-full" :aria-label="t('playground.openOriginal')" @click="preview?.showModal()">
        <img :src="objectUrl" :alt="t('playground.generatedImage')" class="result-image" @load="readDimensions" @error="decodeFailed" />
      </button>
      <figcaption class="flex items-center gap-2 border-t border-gray-100 px-3 py-2 dark:border-dark-700">
        <span class="min-w-0 flex-1 text-xs tabular-nums text-gray-500">{{ dimensions || (isVideo ? t('playground.modeVideo') : t('playground.generatedImage')) }}</span>
        <button v-if="!isVideo" type="button" class="media-tool" :title="t('playground.openOriginal')" :aria-label="t('playground.openOriginal')" @click="preview?.showModal()"><Icon name="externalLink" size="sm" /></button>
        <a :href="objectUrl" :download="filename" class="media-tool" :title="t('playground.downloadMedia')" :aria-label="t('playground.downloadMedia')"><Icon name="download" size="sm" /></a>
        <button type="button" class="media-tool" :title="t('playground.retryMedia')" :aria-label="t('playground.retryMedia')" @click="load"><Icon name="refresh" size="sm" /></button>
      </figcaption>
    </template>
    <dialog ref="preview" class="media-dialog" :aria-label="t('playground.openOriginal')" @click.self="preview?.close()">
      <div class="flex items-center justify-end gap-2 p-2">
        <a :href="objectUrl" :download="filename" class="media-tool" :aria-label="t('playground.downloadMedia')" :title="t('playground.downloadMedia')"><Icon name="download" size="sm" /></a>
        <button type="button" class="media-tool" :aria-label="t('common.close')" :title="t('common.close')" @click="preview?.close()"><Icon name="x" size="sm" /></button>
      </div>
      <img v-if="objectUrl && !isVideo" :src="objectUrl" :alt="t('playground.generatedImage')" class="max-h-[80dvh] w-full object-contain" />
    </dialog>
  </figure>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { fetchMediaBlob } from '@/api/playground'

const props = defineProps<{ src: string; kind?: 'image' | 'video'; name: string }>()
const { t } = useI18n()
const objectUrl = ref('')
const loading = ref(false)
const error = ref('')
const mime = ref('')
const dimensions = ref('')
const preview = ref<HTMLDialogElement | null>(null)
let request: AbortController | undefined
const isVideo = computed(() => mime.value.startsWith('video/') || props.kind === 'video')
const filename = computed(() => `${props.name.replace(/[^a-z0-9_-]/gi, '_')}.${mime.value.split('/')[1]?.replace('quicktime', 'mov') || 'png'}`)

function release() {
  if (objectUrl.value) URL.revokeObjectURL(objectUrl.value)
  objectUrl.value = ''
}

async function load() {
  request?.abort()
  const current = new AbortController()
  request = current
  preview.value?.close()
  release()
  error.value = ''
  dimensions.value = ''
  loading.value = true
  try {
    const blob = await fetchMediaBlob(props.src, current.signal)
    if (current.signal.aborted) return
    mime.value = blob.type
    objectUrl.value = URL.createObjectURL(blob)
  } catch (err) {
    if (current.signal.aborted) return
    error.value = t((err as { status?: number }).status === 401 ? 'playground.mediaAuthRequired' : 'playground.mediaLoadFailed')
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}

function readDimensions(event: Event) {
  const img = event.target as HTMLImageElement
  dimensions.value = `${img.naturalWidth} × ${img.naturalHeight}`
}
function decodeFailed() { error.value = t('playground.mediaLoadFailed') }
watch(() => props.src, load, { immediate: true })
onBeforeUnmount(() => { request?.abort(); release() })
</script>

<style scoped>
.media-result { @apply m-0 min-w-0 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900; }
.result-image { display: block; width: 100%; max-height: 440px; object-fit: contain; background: #f1f3f5; }
.media-status { @apply flex min-h-48 flex-col items-center justify-center gap-3 p-4 text-center text-sm text-gray-500; }
.media-tool { @apply inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100 focus-visible:outline-primary-500 dark:text-gray-300 dark:hover:bg-dark-700; }
.media-dialog { width: min(1100px, 94vw); max-height: 94dvh; padding: 0; border: 0; border-radius: 8px; }
.media-dialog::backdrop { background: rgb(0 0 0 / 70%); }
</style>
