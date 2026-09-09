import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import PlaygroundView from '../PlaygroundView.vue'

const { listModels, streamChat, createImageTask, getImageTask, createVideoTask, getVideoTask, getModelPlaza, refreshUser } =
  vi.hoisted(() => ({
    listModels: vi.fn(),
    streamChat: vi.fn(),
    createImageTask: vi.fn(),
    getImageTask: vi.fn(),
    createVideoTask: vi.fn(),
    getVideoTask: vi.fn(),
    getModelPlaza: vi.fn(),
    refreshUser: vi.fn(),
  }))

vi.mock('@/api/playground', async () => {
  const actual = await vi.importActual<typeof import('@/api/playground')>('@/api/playground')
  return {
    ...actual,
    listModels,
    streamChat,
    createImageTask,
    getImageTask,
    createVideoTask,
    getVideoTask,
  }
})

vi.mock('@/api/modelPlaza', () => ({ getModelPlaza }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { balance: 1000 }, refreshUser }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const IconStub = { props: ['name', 'size'], template: '<i />' }
const SpinnerStub = { props: ['size'], template: '<span />' }

function mountView() {
  return mount(PlaygroundView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        Icon: IconStub,
        LoadingSpinner: SpinnerStub,
      },
    },
  })
}

/** 走一遍「输入 → 发送」，返回 wrapper。 */
async function send(wrapper: ReturnType<typeof mountView>, text: string) {
  await wrapper.find('textarea').setValue(text)
  await wrapper.find('button[aria-label="playground.send"]').trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  refreshUser.mockResolvedValue({})
  // 照线上组 4 的真实形态构造：/v1/models 有 7 个，广场只覆盖到其中 6 个,
  // 视频模型 t-grok-video-1.5 在广场里没有定价行。
  listModels.mockResolvedValue([
    'deepseek-v4-flash',
    't-gpt-image-2',
    't-grok-video-1.5',
  ])
  getModelPlaza.mockResolvedValue({
    description: '',
    groups: [
      {
        id: 4,
        models: [
          { name: 'deepseek-v4-flash', platform: 'anthropic', pricing: { billing_mode: 'token' } },
          { name: 't-gpt-image-2', platform: 'openai', pricing: { billing_mode: 'image' } },
        ],
      },
    ],
  })
})

describe('PlaygroundView', () => {
  it('按计费模式和模型名把模型分到三种模式下', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('option').map((o) => o.text())).toEqual(['deepseek-v4-flash'])

    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('option').map((o) => o.text())).toEqual(['t-gpt-image-2'])

    // 关键回归：视频模型在广场里没有定价行，只靠广场这一档会是空的。
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeVideo'))!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('option').map((o) => o.text())).toEqual(['t-grok-video-1.5'])
  })

  /**
   * 回归：流式增量必须在**流still进行中**就渲染出来。
   *
   * 曾经把 push 进响应式数组的原始对象留在局部变量里，`reply.content += delta`
   * 改的是原始对象，代理的依赖不会被触发。注意这个 bug 不会让文字彻底不出现——
   * 请求结束时 busy 翻转会带来一次重渲染，把攒下的整段文字一次性刷出来。
   * 所以断言必须打在流结束之前，否则测试对这个 bug 免疫。
   */
  it('流进行中就把增量渲染进消息流', async () => {
    let emit!: (text: string) => void
    let finish!: () => void
    streamChat.mockImplementation(
      (_model: string, _messages: unknown, callbacks: { onDelta: (t: string) => void }) => {
        emit = callbacks.onDelta
        return new Promise<void>((resolve) => {
          finish = resolve
        })
      }
    )

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('textarea').setValue('在吗')
    await wrapper.find('button[aria-label="playground.send"]').trigger('click')
    await flushPromises()

    // 流还没结束，busy 仍为 true——此时的重渲染只可能来自增量本身。
    emit('你好')
    await nextTick()
    expect(wrapper.text()).toContain('你好')

    emit('，世界')
    await nextTick()
    expect(wrapper.text()).toContain('你好，世界')

    finish()
    await flushPromises()
  })

  it('同步返回的生图结果直接渲染，不再轮询', async () => {
    createImageTask.mockResolvedValue({ data: [{ url: 'https://cdn.example/a.png' }] })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()
    await send(wrapper, '一只猫')

    expect(wrapper.find('img').attributes('src')).toBe('https://cdn.example/a.png')
    expect(getImageTask).not.toHaveBeenCalled()
  })

  it('请求结束后回刷余额', async () => {
    streamChat.mockResolvedValue(undefined)

    const wrapper = mountView()
    await flushPromises()
    await send(wrapper, '在吗')

    expect(refreshUser).toHaveBeenCalled()
  })

  it('调用失败时把错误显示在消息流里，而不是静默吞掉', async () => {
    streamChat.mockRejectedValue(new Error('余额不足'))

    const wrapper = mountView()
    await flushPromises()
    await send(wrapper, '在吗')

    expect(wrapper.text()).toContain('余额不足')
  })
})
