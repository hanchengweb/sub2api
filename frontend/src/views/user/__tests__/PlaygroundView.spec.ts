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
const RouterLinkStub = { props: ['to'], template: '<a :href="to"><slot /></a>' }
const IconStub = { props: ['name', 'size'], template: '<i />' }
const SpinnerStub = { props: ['size'], template: '<span />' }

function mountView() {
  return mount(PlaygroundView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        Icon: IconStub,
        LoadingSpinner: SpinnerStub,
        RouterLink: RouterLinkStub,
      },
    },
  })
}

/** 只取模型选择器里的选项——参数下拉（比例/清晰度…）也是 option，不能混。 */
function modelOptions(wrapper: ReturnType<typeof mountView>): string[] {
  return wrapper.findAll('[data-testid="model-select"] option').map((o) => o.text())
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
        rate_multiplier: 1,
        models: [
          {
            name: 'deepseek-v4-flash',
            platform: 'anthropic',
            pricing: { billing_mode: 'token', input_price: 4.2857e-4, output_price: 1.28571e-3 },
          },
          {
            name: 't-gpt-image-2',
            platform: 'openai',
            pricing: {
              billing_mode: 'image',
              per_request_price: null,
              intervals: [
                { tier_label: '1K', per_request_price: 39.5 },
                { tier_label: '2K', per_request_price: 43 },
              ],
            },
          },
          {
            name: 't-grok-video-1.5',
            platform: 'grok',
            pricing: null,
            video_pricing: {
              price_per_second_480p: 37,
              price_per_second_720p: 37,
              price_per_second_1080p: 37,
            },
          },
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
    expect(modelOptions(wrapper)).toEqual(['t-gpt-image-2'])

    // 关键回归：视频模型在广场里没有定价行，只靠广场这一档会是空的。
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeVideo'))!.trigger('click')
    await flushPromises()
    expect(modelOptions(wrapper)).toEqual(['t-grok-video-1.5'])
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

  /**
   * 注册不再自动送积分后，新用户第一次调用几乎必然撞上这条。
   * 网关回的是英文 "Insufficient account balance"，不能原样丢给用户看。
   */
  it('余额不足给中文引导和兑换入口，不是英文原文', async () => {
    const err = Object.assign(new Error('Insufficient account balance'), {
      code: 'INSUFFICIENT_BALANCE',
    })
    streamChat.mockRejectedValue(err)

    const wrapper = mountView()
    await flushPromises()
    await send(wrapper, '在吗')

    expect(wrapper.text()).toContain('playground.insufficientBalance')
    expect(wrapper.text()).not.toContain('Insufficient account balance')
    expect(wrapper.find('a[href="/redeem"]').exists()).toBe(true)
  })

  it('调用失败时把错误显示在消息流里，而不是静默吞掉', async () => {
    streamChat.mockRejectedValue(new Error('余额不足'))

    const wrapper = mountView()
    await flushPromises()
    await send(wrapper, '在吗')

    expect(wrapper.text()).toContain('余额不足')
  })
})


describe('PlaygroundView 参数与费用预估', () => {
  async function switchTo(wrapper: ReturnType<typeof mountView>, labelKey: string) {
    await wrapper.findAll('button').find((b) => b.text().includes(labelKey))!.trigger('click')
    await flushPromises()
  }

  it('参数条只显示当前模式用得上的项', async () => {
    const wrapper = mountView()
    await flushPromises()

    // 对话模式没有参数条
    expect(wrapper.text()).not.toContain('playground.paramAspect')

    await switchTo(wrapper, 'playground.modeImage')
    expect(wrapper.text()).toContain('playground.paramAspect')
    expect(wrapper.text()).toContain('playground.paramCount')
    // 时长是视频专有
    expect(wrapper.text()).not.toContain('playground.paramDuration')

    await switchTo(wrapper, 'playground.modeVideo')
    expect(wrapper.text()).toContain('playground.paramDuration')
    expect(wrapper.text()).not.toContain('playground.paramCount')
  })

  /**
   * 生图与视频的清晰度是两套值（1k/2k/4k vs 480p/720p/1080p）。
   * 切模式不校正的话会把 "1k" 当分辨率发给视频接口，上游直接拒。
   */
  it('切模式后清晰度校正到该模式的合法档位', async () => {
    createVideoTask.mockResolvedValue({ id: 'v1', status: 'queued' })
    const wrapper = mountView()
    await flushPromises()

    await switchTo(wrapper, 'playground.modeImage')
    await switchTo(wrapper, 'playground.modeVideo')
    await send(wrapper, '一只猫在跑')

    expect(createVideoTask).toHaveBeenCalledWith(
      't-grok-video-1.5',
      '一只猫在跑',
      expect.objectContaining({ resolution: '480p' })
    )
  })

  it('生图请求带上选中的比例/清晰度/张数,不是写死的', async () => {
    createImageTask.mockResolvedValue({ data: [{ url: 'https://cdn.example/a.png' }] })
    const wrapper = mountView()
    await flushPromises()
    await switchTo(wrapper, 'playground.modeImage')
    await send(wrapper, '一只猫')

    expect(createImageTask).toHaveBeenCalledWith(
      't-gpt-image-2',
      '一只猫',
      expect.objectContaining({ size: '1:1', resolution: '1k', n: 1 })
    )
  })

  it('视频费用预估 = 每秒单价 × 时长', async () => {
    const wrapper = mountView()
    await flushPromises()
    await switchTo(wrapper, 'playground.modeVideo')
    // 默认 8 秒 × 37 积分/秒 = 296
    expect(wrapper.text()).toContain('playground.costVideo')
    expect(wrapper.find('.bg-amber-50').exists()).toBe(true)
  })
})
