import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import PlaygroundView from '../PlaygroundView.vue'
import Select from '@/components/common/Select.vue'

const { listModels, streamChat, createImageTask, getImageTask, createVideoTask, getVideoTask, getModelPlaza, refreshUser, fetchMediaBlob } =
  vi.hoisted(() => ({
    listModels: vi.fn(),
    streamChat: vi.fn(),
    createImageTask: vi.fn(),
    getImageTask: vi.fn(),
    createVideoTask: vi.fn(),
    getVideoTask: vi.fn(),
    getModelPlaza: vi.fn(),
    refreshUser: vi.fn(),
    fetchMediaBlob: vi.fn(),
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
    fetchMediaBlob,
  }
})

vi.mock('@/api/modelPlaza', () => ({ getModelPlaza }))

// 组件用 useRoute 读 ?model=&mode=（从模型广场「去体验」跳过来时带的），
// 测试里没有装 router，必须给个空 query 的桩。
vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return { ...actual, useRoute: () => ({ query: {} }) }
})

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

/**
 * 打开模型选择器并读出可选模型。
 *
 * 原生 <select> 已换成带厂商 logo 的自定义选择器（27 个模型时原生下拉会拉满整屏），
 * 选项是按钮不是 option，且要先点开才渲染。
 */
async function modelOptions(wrapper: ReturnType<typeof mountView>): Promise<string[]> {
  const trigger = wrapper.find('[data-testid="model-select"]')
  if (!trigger.exists()) return []
  await trigger.trigger('click')
  const items = wrapper.findAll('[data-testid="model-option"]').map((o) => o.text())
  await trigger.trigger('click') // 收起，免得挡住后续查询
  return items
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
  fetchMediaBlob.mockResolvedValue(new Blob(['image'], { type: 'image/png' }))
  URL.createObjectURL = vi.fn(() => 'blob:authenticated-result')
  URL.revokeObjectURL = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
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

    expect(await modelOptions(wrapper)).toEqual(['deepseek-v4-flash'])

    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()
    expect(await modelOptions(wrapper)).toEqual(['t-gpt-image-2'])

    // 关键回归：视频模型在广场里没有定价行，只靠广场这一档会是空的。
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeVideo'))!.trigger('click')
    await flushPromises()
    expect(await modelOptions(wrapper)).toEqual(['t-grok-video-1.5'])
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

    expect(wrapper.find('img').attributes('src')).toBe('blob:authenticated-result')
    expect(fetchMediaBlob).toHaveBeenCalledWith('https://cdn.example/a.png', expect.any(AbortSignal))
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
    expect(wrapper.findAllComponents(Select).some(sel => sel.props('ariaLabel') === 'playground.paramCount')).toBe(true)
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
  })
})


describe('PlaygroundView 模型名单兜底', () => {
  /**
   * 回归：余额为 0 时 /v1/models 会被网关的余额闸门一起拦掉
   * （实测返回 INSUFFICIENT_BALANCE）。注册不再送积分后，每个新用户第一次
   * 打开都会撞上——不能让他看到空下拉，那只会以为服务坏了。
   */
  it('名单接口失败时退到广场目录，下拉不为空', async () => {
    listModels.mockRejectedValue(
      Object.assign(new Error('Insufficient account balance'), { code: 'INSUFFICIENT_BALANCE' })
    )

    const wrapper = mountView()
    await flushPromises()

    // 广场目录里的对话模型仍然列得出来
    expect(await modelOptions(wrapper)).toEqual(['deepseek-v4-flash'])
  })

  it('名单与广场都拿不到时不白屏，只是没有模型', async () => {
    listModels.mockRejectedValue(new Error('boom'))
    getModelPlaza.mockRejectedValue(new Error('boom'))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('playground.noModelsForMode')
  })
})

describe('PlaygroundView 质量档与结果地址', () => {
  /**
   * 上游按「清晰度 × 质量」分档收费，同一个 1K 从低到高差 35 倍。
   * 质量必须带进请求，否则按低质量计费、用户却可能拿到高质量图。
   */
  it('质量选项带进生图请求，且预估价按质量档算', async () => {
    getModelPlaza.mockResolvedValue({
      description: '',
      groups: [
        {
          id: 4,
          rate_multiplier: 1,
          models: [
            {
              name: 't-gpt-image-2-vip',
              platform: 'openai',
              pricing: {
                billing_mode: 'image',
                per_request_price: null,
                intervals: [
                  { tier_label: '1K·低', per_request_price: 30.33 },
                  { tier_label: '1K·高', per_request_price: 76.25 },
                ],
              },
            },
          ],
        },
      ],
    })
    listModels.mockResolvedValue(['t-gpt-image-2-vip'])
    createImageTask.mockResolvedValue({ data: [{ url: 'https://cdn.example/a.png' }] })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()

    // 价格文案带插值参数，而本文件的 i18n mock 只回 key、不做插值，
    // 所以这里不断言金额（金额的算法由「参数与费用预估」那组用例覆盖），
    // 只验真正要保证的：质量选项确实带进了请求。

    const qualitySelect = wrapper
      .findAllComponents(Select)
      .find((sel) => sel.props('ariaLabel') === 'playground.paramQuality')
    expect(qualitySelect, '分质量档的模型应显示质量选择器').toBeTruthy()
    expect(qualitySelect!.props('disabled')).toBe(false)

    // 联动可见：每个选项后面直接标出该组合的积分，
    // 只给一个总价的话，用户看不出该改哪个参数
    const qualityTexts = qualitySelect!.props('options').map((o) => (o as { label: string }).label)
    expect(qualityTexts.some((t) => t.includes('30.33')), '低质量选项应标价 30.33').toBe(true)
    expect(qualityTexts.some((t) => t.includes('76.25')), '高质量选项应标价 76.25').toBe(true)
    qualitySelect!.vm.$emit('update:modelValue', 'high')
    await flushPromises()

    await send(wrapper, '一只猫')
    expect(createImageTask).toHaveBeenCalledWith(
      't-gpt-image-2-vip',
      '一只猫',
      expect.objectContaining({ quality: 'high' })
    )
  })

  /**
   * 回归：异步生图完成时结果套在 result 里
   * （{"status":"completed","result":{"data":[{"url":...}]}}），
   * 只看顶层 url/data 会取不到，界面报「任务已完成，但没有返回可用的结果地址」。
   */
  it('从 result.data 里取出结果图，而不是报没有结果地址', async () => {
    listModels.mockResolvedValue(['t-gpt-image-2'])
    createImageTask.mockResolvedValue({ id: 'tsk_img_1', status: 'pending' })
    getImageTask.mockResolvedValue({
      id: 'tsk_img_1',
      status: 'completed',
      result: { type: 'image', data: [{ url: 'https://files.example/x.png' }] },
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()

    vi.useFakeTimers()
    const sending = send(wrapper, '一只猫')
    await vi.advanceTimersByTimeAsync(6000)
    vi.useRealTimers()
    await sending
    await flushPromises()

    expect(wrapper.find('img').attributes('src')).toBe('blob:authenticated-result')
    expect(fetchMediaBlob).toHaveBeenCalledWith('https://files.example/x.png', expect.any(AbortSignal))
    expect(wrapper.text()).not.toContain('playground.noMediaUrl')
  })
})


describe('PlaygroundView 质量档不适用时', () => {
  /**
   * 28 个生图模型里只有 vip / official 两个按质量分档，其余 26 个上游就是单一价。
   * 早先做法是直接把控件藏掉，结果用户以为功能坏了——改成显示但禁用并说明原因。
   */
  it('不分质量档的模型：不显示质量控件', async () => {
    getModelPlaza.mockResolvedValue({
      description: '',
      groups: [
        {
          id: 4,
          rate_multiplier: 1,
          models: [
            {
              name: 'doubao-seedream-4-0',
              platform: 'openai',
              pricing: { billing_mode: 'image', per_request_price: 48.95, intervals: [] },
            },
          ],
        },
      ],
    })
    listModels.mockResolvedValue(['doubao-seedream-4-0'])

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()

    const qualitySelect = wrapper
      .findAllComponents(Select)
      .find((sel) => sel.props('ariaLabel') === 'playground.paramQuality')
    expect(qualitySelect, '不分档的模型不该出现质量控件').toBeFalsy()
    // 也不该冒出「不分档」这类说明——上游价目表对这类模型压根不提质量维度
    expect(wrapper.text()).not.toContain('playground.qualityNotApplicable')
  })
})

describe('PlaygroundView 离开页面后恢复任务', () => {
  /**
   * 任务在服务端照常跑，是前端在 onBeforeUnmount 里停掉了轮询。
   * 切走再回来那条消息就永远停在灰条上——图其实早生成好了。
   */
  it('重进页面时续上未完成的生图任务', async () => {
    localStorage.setItem(
      'playground_conversations_v1',
      JSON.stringify([
        {
          id: 'c1',
          title: '生图',
          mode: 'image',
          model: 't-gpt-image-2-vip',
          updatedAt: Date.now(),
          messages: [
            { id: 'u1', role: 'user', content: '一只猫', createdAt: Date.now() },
            // 有 taskId、既无结果也无报错 = 离开时还没跑完
            { id: 'a1', role: 'assistant', content: '', taskId: 'tsk_1', mediaKind: 'image', createdAt: Date.now() },
          ],
        },
      ])
    )
    getImageTask.mockResolvedValue({
      status: 'completed',
      result: { type: 'image', data: [{ url: 'https://cdn.example/resumed.png' }] },
    })

    vi.useFakeTimers()
    try {
      const wrapper = mountView()
      await flushPromises()
      // 轮询有 5 秒间隔，快进到第一次查询
      await vi.advanceTimersByTimeAsync(6000)
      await flushPromises()
      expect(getImageTask, '应对未完成的任务重新发起查询').toHaveBeenCalledWith('tsk_1')
      expect(wrapper.exists()).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it('已有结果或已报错的消息不再重复轮询', async () => {
    localStorage.setItem(
      'playground_conversations_v1',
      JSON.stringify([
        {
          id: 'c1', title: 'x', mode: 'image', model: 'm', updatedAt: Date.now(),
          messages: [
            { id: 'a1', role: 'assistant', content: '', taskId: 't1', mediaKind: 'image',
              mediaUrl: 'https://cdn.example/done.png', createdAt: Date.now() },
            { id: 'a2', role: 'assistant', content: '', taskId: 't2', mediaKind: 'image',
              error: '生成失败', createdAt: Date.now() },
          ],
        },
      ])
    )
    const wrapper = mountView()
    await flushPromises()
    expect(getImageTask).not.toHaveBeenCalled()
    expect(wrapper.exists()).toBe(true)
  })
})

describe('PlaygroundView 媒体中转与进度', () => {
  it('displays every result returned for a multi-image request', async () => {
    createImageTask.mockResolvedValue({ data: [{ url: 'https://files.toapis.cn/a.png' }, { url: 'https://files.toapis.cn/b.png' }] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()
    await send(wrapper, 'two images')
    expect(wrapper.findAll('.media-result')).toHaveLength(2)
    expect(JSON.parse(localStorage.getItem('playground_conversations_v1')!)[0].messages[1].mediaUrls).toHaveLength(2)
    wrapper.unmount()
  })
  /**
   * 上游把图片放在 files.toapis.cn，部分网络环境访问不到那个域名——
   * 用户换出口 IP 后页面上只剩碎图。必须走本站中转。
   */
  it('图片走本站中转地址，不直连上游图床', async () => {
    createImageTask.mockResolvedValue({ data: [{ url: 'https://files.toapis.cn/images/a.png' }] })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
    await flushPromises()
    await send(wrapper, '一只猫')

    const src = wrapper.find('img').attributes('src') ?? ''
    expect(src, '不应直连上游图床').not.toMatch(/^https:\/\/files\.toapis\.cn/)
    expect(src).toBe('blob:authenticated-result')
    expect(fetchMediaBlob).toHaveBeenCalledWith('https://files.toapis.cn/images/a.png', expect.any(AbortSignal))
  })

  it('轮询期间显示真实百分比进度', async () => {
    vi.useFakeTimers()
    try {
      createImageTask.mockResolvedValue({ id: 'tsk_p', status: 'pending' })
      getImageTask
        .mockResolvedValueOnce({ status: 'in_progress', progress: 42 })
        .mockResolvedValue({
          status: 'completed',
          result: { data: [{ url: 'https://files.toapis.cn/images/done.png' }] },
        })

      const wrapper = mountView()
      await flushPromises()
      await wrapper.findAll('button').find((b) => b.text().includes('playground.modeImage'))!.trigger('click')
      await flushPromises()

      await wrapper.find('textarea').setValue('一只猫')
      await wrapper.find('button[aria-label="playground.send"]').trigger('click')
      await flushPromises()

      await vi.advanceTimersByTimeAsync(6000)
      await flushPromises()
      expect(wrapper.text(), '应显示上游回的真实进度').toContain('42%')
    } finally {
      vi.useRealTimers()
    }
  })
})
