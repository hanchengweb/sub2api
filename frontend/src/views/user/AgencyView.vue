<template>
  <AppLayout>
    <div class="agency-page">
      <section class="agency-hero" aria-labelledby="agency-headline">
        <h1 id="agency-headline">
          {{ t('agency.headline') }}<br />
          <span>{{ t('agency.headlineAccent') }}</span>
        </h1>
        <img src="/images/agency/partnership-bridge.webp" alt="" width="677" height="260" class="agency-bridge" />
      </section>

      <div class="agency-workspace">
        <fieldset class="agency-directions">
          <legend>{{ t('agency.chooseDirection') }}</legend>
          <label v-for="direction in directions" :key="direction.id" class="agency-choice" :class="{ selected: selected === direction.id }">
            <input v-model="selected" type="radio" name="cooperation-direction" :value="direction.id" />
            <span class="agency-choice-icon" aria-hidden="true"><Icon :name="direction.icon" size="lg" /></span>
            <span class="agency-choice-copy">
              <strong>{{ t(`agency.directions.${direction.id}.title`) }}</strong>
              <span>{{ t(`agency.directions.${direction.id}.description`) }}</span>
            </span>
            <Icon :name="selected === direction.id ? 'checkCircle' : 'chevronRight'" size="sm" class="agency-choice-check" aria-hidden="true" />
          </label>
        </fieldset>

        <form ref="formElement" class="agency-form" @submit.prevent="generateBrief" @input="clearValidity">
          <h2>{{ t('agency.intent') }}</h2>
          <div class="agency-field">
            <label for="agency-name">{{ t('agency.name') }}</label>
            <input id="agency-name" v-model="form.name" name="contactName" autocomplete="name" required maxlength="80" :placeholder="t('agency.namePlaceholder')" />
          </div>
          <div class="agency-field">
            <label for="agency-email">{{ t('agency.email') }}</label>
            <input id="agency-email" v-model="form.email" type="email" name="email" autocomplete="email" required maxlength="254" :placeholder="t('agency.emailPlaceholder')" />
          </div>
          <div class="agency-field">
            <label for="agency-company">{{ t('agency.company') }} <span>{{ t('agency.optional') }}</span></label>
            <input id="agency-company" v-model="form.company" name="company" autocomplete="organization" maxlength="120" :placeholder="t('agency.companyPlaceholder')" />
          </div>
          <div class="agency-field">
            <label for="agency-scenario">{{ t('agency.scenario') }}</label>
            <textarea id="agency-scenario" v-model="form.scenario" name="scenario" required maxlength="1000" rows="3" :placeholder="t(`agency.directions.${selected}.placeholder`)" />
          </div>
          <div class="agency-actions">
            <button type="submit" class="agency-primary"><Icon name="document" size="sm" aria-hidden="true" />{{ t('agency.generate') }}</button>
            <button type="button" class="agency-secondary" @click="openDialog('contact')"><Icon name="chat" size="sm" aria-hidden="true" />{{ t('agency.contact') }}</button>
          </div>
        </form>
      </div>

      <dialog ref="dialogElement" class="agency-dialog" aria-labelledby="agency-dialog-title" @close="copyState = ''">
        <div class="agency-dialog-header">
          <h2 id="agency-dialog-title">{{ t(dialogMode === 'brief' ? 'agency.brief' : 'agency.contact') }}</h2>
          <button type="button" class="agency-close" :aria-label="t('common.close')" @click="dialogElement?.close()"><Icon name="x" size="md" /></button>
        </div>
        <template v-if="dialogMode === 'brief'">
          <textarea :value="brief" readonly class="agency-brief" :aria-label="t('agency.brief')" rows="10" />
          <p class="agency-dialog-hint">{{ t('agency.briefHint') }}</p>
        </template>
        <p v-if="dialogMode === 'contact' || contactInfo" class="agency-contact-info">{{ contactInfo || t('agency.noContact') }}</p>
        <p v-if="copyState" class="agency-copy-status" role="status">{{ t(copyState) }}</p>
        <div class="agency-dialog-actions">
          <button v-if="dialogMode === 'brief' || contactInfo" type="button" class="agency-primary" @click="copyText"><Icon name="copy" size="sm" aria-hidden="true" />{{ t(dialogMode === 'brief' ? 'agency.copyBrief' : 'agency.copyContact') }}</button>
          <button type="button" class="agency-secondary" @click="dialogElement?.close()">{{ t(dialogMode === 'brief' ? 'agency.backToEdit' : 'common.close') }}</button>
        </div>
      </dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()
const directions = [
  { id: 'channel', icon: 'globe' },
  { id: 'integration', icon: 'terminal' },
  { id: 'delivery', icon: 'users' }
] as const
const selected = ref<(typeof directions)[number]['id']>('channel')
const form = reactive({ name: '', email: '', company: '', scenario: '' })
const formElement = ref<HTMLFormElement | null>(null)
const dialogElement = ref<HTMLDialogElement | null>(null)
const dialogMode = ref<'brief' | 'contact'>('brief')
const brief = ref('')
const copyState = ref('')
const contactInfo = computed(() => appStore.contactInfo.trim())

function clearValidity(event: Event) {
  const target = event.target
  if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) target.setCustomValidity('')
}

function openDialog(mode: 'brief' | 'contact') {
  dialogMode.value = mode
  copyState.value = ''
  dialogElement.value?.showModal()
}

function generateBrief() {
  const element = formElement.value
  if (!element) return
  for (const [name, value] of [['contactName', form.name], ['scenario', form.scenario]]) {
    const input = element.elements.namedItem(name) as HTMLInputElement | HTMLTextAreaElement
    input.setCustomValidity(value.trim() ? '' : t('agency.required'))
  }
  if (!element.reportValidity()) return
  brief.value = [
    t('agency.brief'),
    `${t('agency.chooseDirection')}：${t(`agency.directions.${selected.value}.title`)}`,
    `${t('agency.name')}：${form.name.trim()}`,
    `${t('agency.email')}：${form.email.trim()}`,
    ...(form.company.trim() ? [`${t('agency.company')}：${form.company.trim()}`] : []),
    `${t('agency.scenario')}：${form.scenario.trim()}`
  ].join('\n')
  openDialog('brief')
}

async function copyText() {
  try {
    const copied = await copyToClipboard(dialogMode.value === 'brief' ? brief.value : contactInfo.value)
    copyState.value = copied ? 'agency.copied' : 'agency.copyFailed'
  } catch {
    copyState.value = 'agency.copyFailed'
  }
}
</script>

<style scoped>
:deep(.bg-mesh-gradient) { display: none; }
.agency-page { max-width: 1440px; margin: 0 auto; color: #0f172a; }
.agency-hero { position: relative; display: flex; align-items: center; min-height: 310px; padding: 28px 0 42px; border-bottom: 1px solid #dce4e8; }
.agency-hero h1 { position: relative; z-index: 1; margin: 0; font-size: clamp(30px, 3.8vw, 58px); line-height: 1.42; font-weight: 750; letter-spacing: -.04em; }
.agency-hero h1 span { color: #0f766e; }
.agency-bridge { position: absolute; right: -16px; width: 61%; height: auto; mix-blend-mode: multiply; }
.agency-workspace { display: grid; grid-template-columns: 1fr 1fr; gap: 48px; padding: 36px 0 28px; }
.agency-directions { min-width: 0; margin: 0; padding: 0; border: 0; }
.agency-directions legend, .agency-form h2 { margin: 0 0 26px; padding: 0; font-size: 19px; line-height: 1.5; font-weight: 650; }
.agency-choice { position: relative; display: flex; align-items: center; min-height: 104px; gap: 18px; margin-bottom: 18px; padding: 20px; border: 1px solid #e0e6ea; border-radius: 12px; background: #fff; cursor: pointer; transition: border-color .15s, background-color .15s; }
.agency-choice:hover { border-color: #82bab5; }
.agency-choice.selected { border-color: #0f766e; background: #f1fbf9; }
.agency-choice:focus-within { outline: 3px solid #99f6e4; outline-offset: 3px; }
.agency-choice input { position: absolute; width: 1px; height: 1px; opacity: 0; }
.agency-choice-icon { display: grid; place-items: center; width: 58px; height: 58px; flex-shrink: 0; border-radius: 50%; color: #0f766e; background: #e6f7f4; }
.agency-choice-copy { min-width: 0; display: grid; gap: 7px; }
.agency-choice-copy strong { font-size: 17px; font-weight: 650; }
.agency-choice-copy > span { color: #64748b; font-size: 14px; line-height: 1.65; }
.agency-choice-check { flex-shrink: 0; margin-left: auto; color: #0f766e; }
.agency-form { min-width: 0; border-left: 1px solid #e0e6ea; padding-left: 48px; }
.agency-form h2 { margin-bottom: 24px; }
.agency-field { display: grid; gap: 8px; margin-bottom: 19px; }
.agency-field label { font-size: 14px; font-weight: 500; }
.agency-field label span { color: #64748b; font-weight: 400; }
.agency-field input, .agency-field textarea { width: 100%; min-width: 0; padding: 11px 13px; border: 1px solid #dce3e8; border-radius: 8px; outline: none; background: #fff; font: inherit; font-size: 14px; line-height: 1.5; transition: border-color .15s, box-shadow .15s; }
.agency-field textarea { resize: vertical; min-height: 98px; }
.agency-field input::placeholder, .agency-field textarea::placeholder { color: #94a3b8; }
.agency-field input:focus, .agency-field textarea:focus { border-color: #0d9488; box-shadow: 0 0 0 3px #ccfbf1; }
.agency-actions, .agency-dialog-actions { display: flex; flex-wrap: wrap; gap: 12px; }
.agency-actions > button { flex: 1; }
.agency-primary, .agency-secondary { display: inline-flex; align-items: center; justify-content: center; gap: 9px; min-height: 44px; padding: 11px 18px; border: 1px solid #0f766e; border-radius: 8px; font-size: 14px; font-weight: 600; cursor: pointer; }
.agency-primary { color: white; background: #0f766e; }
.agency-primary:hover { background: #115e59; }
.agency-secondary { color: #0f766e; background: #fff; }
.agency-secondary:hover { background: #f0fdfa; }
.agency-page button:focus-visible { outline: 3px solid #5eead4; outline-offset: 3px; }
.agency-dialog { width: min(560px, calc(100vw - 32px)); max-height: calc(100dvh - 48px); padding: 28px; border: 1px solid #e0e6ea; border-radius: 16px; color: #0f172a; background: #fff; box-shadow: 0 24px 70px #0f172a26; }
.agency-dialog::backdrop { background: #0f172a66; }
.agency-dialog-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.agency-dialog-header h2 { font-size: 20px; font-weight: 650; }
.agency-close { padding: 8px; border-radius: 8px; color: #64748b; }
.agency-brief { width: 100%; resize: vertical; padding: 16px; border: 1px solid #dce3e8; border-radius: 8px; color: inherit; background: #f8fafc; font: inherit; font-size: 14px; line-height: 1.8; }
.agency-dialog-hint, .agency-contact-info, .agency-copy-status { margin: 16px 0; font-size: 14px; line-height: 1.7; overflow-wrap: anywhere; white-space: pre-wrap; }
.agency-dialog-hint { color: #64748b; }
.agency-copy-status { color: #0f766e; }
.dark .agency-page, .dark .agency-dialog { color: #e2e8f0; }
.dark .agency-hero, .dark .agency-form { border-color: #334155; }
.dark .agency-hero h1 span { color: #5eead4; }
.dark .agency-bridge { mix-blend-mode: normal; opacity: .85; }
.dark .agency-choice, .dark .agency-field input, .dark .agency-field textarea, .dark .agency-dialog, .dark .agency-brief, .dark .agency-secondary { background: #0f172a; border-color: #334155; }
.dark .agency-choice.selected { background: #113532; border-color: #2dd4bf; }
.dark .agency-choice-icon { background: #134e4a; color: #5eead4; }
.dark .agency-choice-copy > span, .dark .agency-field label span, .dark .agency-dialog-hint { color: #94a3b8; }
.dark .agency-secondary, .dark .agency-copy-status, .dark .agency-choice-check { color: #5eead4; }
.dark .agency-field input:focus, .dark .agency-field textarea:focus { border-color: #2dd4bf; box-shadow: 0 0 0 3px #134e4a; }
@media (min-width: 1600px) { .agency-hero { min-height: 330px; } }
@media (max-width: 1200px) { .agency-workspace { gap: 28px; } .agency-form { padding-left: 28px; } .agency-choice { gap: 12px; padding: 16px; } .agency-choice-icon { width: 48px; height: 48px; } }
@media (max-width: 767px) { .agency-hero { display: grid; grid-template-columns: 1fr; gap: 24px; padding: 12px 0 28px; } .agency-hero h1 { font-size: clamp(28px, 6.3vw, 42px); } .agency-bridge { position: static; width: 100%; max-width: 580px; justify-self: end; } .agency-workspace { grid-template-columns: 1fr; gap: 20px; padding-top: 28px; } .agency-form { padding: 28px 0 0; border-left: 0; border-top: 1px solid #e0e6ea; } .agency-directions legend { margin-bottom: 18px; } .agency-choice { min-height: 94px; margin-bottom: 12px; } .agency-dialog { padding: 20px; } }
@media (prefers-reduced-motion: reduce) { .agency-choice, .agency-field input, .agency-field textarea { transition: none; } }
</style>
