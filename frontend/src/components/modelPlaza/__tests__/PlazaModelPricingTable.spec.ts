import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PlazaModelPricingTable from '../PlazaModelPricingTable.vue'
import type { PlazaModel } from '@/api/modelPlaza'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

function tokenModel(overrides: Partial<PlazaModel> = {}): PlazaModel {
  return {
    name: 'claude-sonnet',
    platform: 'anthropic',
    pricing: {
      billing_mode: 'token',
      input_price: 3e-6,
      output_price: 1.5e-5,
      cache_write_price: 3.75e-6,
      cache_read_price: 3e-7,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: []
    },
    official_pricing: {
      input_price: 3e-6,
      output_price: 1.5e-5,
      cache_write_price: 3.75e-6,
      cache_write_1h_price: 6e-6,
      cache_read_price: 3e-7
    },
    ...overrides
  }
}

function mountTable(
  models: PlazaModel[],
  rateMultiplier: number,
  userRateMultiplier?: number | null,
  showOfficialPricing = true
) {
  // 组件里官方参考价列默认关闭(见 props.showOfficialPricing)。这里默认开启,
  // 让既有用例继续覆盖官方列的渲染与列序;默认关闭后的形态由「官方价列默认隐藏」
  // 一组用例单独断言。
  return mount(PlazaModelPricingTable, {
    props: {
      models,
      rateMultiplier,
      userRateMultiplier: userRateMultiplier ?? null,
      showOfficialPricing
    }
  })
}

describe('PlazaModelPricingTable', () => {
  it('倍率为 1 时展示渠道单价原值(积分/1M),价格保底 2 位小数', () => {
    const wrapper = mountTable([tokenModel()], 1)
    const text = wrapper.text()
    expect(text).toContain('3.00 积分')
    expect(text).toContain('15.00 积分')
    // 缓存写 / 读(超过 2 位小数原样保留)
    expect(text).toContain('3.75 积分')
    expect(text).toContain('0.30 积分')
    // 倍率列
    // 倍率不再逐行重复：它是分组属性，由页头 GroupBadge 展示（含专属倍率划线）
    expect(text).not.toContain('1x')
  })

  it('倍率 ≠ 1 时价格列为折后实付价,官方价列保持原价', () => {
    const wrapper = mountTable([tokenModel()], 0.5)
    const text = wrapper.text()
    // 实付 = 3 × 0.5 / 15 × 0.5
    expect(text).toContain('1.50 积分')
    expect(text).toContain('7.50 积分')
    // 官方价原值仍在(官方列不乘倍率,单位仍是美元)
    expect(text).toContain('$3.00')
    expect(text).toContain('$15.00')
  })

  it('用户专属倍率覆盖分组倍率,并划线展示原倍率', () => {
    const wrapper = mountTable([tokenModel()], 1, 0.8)
    const text = wrapper.text()
    // 实付按 0.8:3 × 0.8 = 2.4
    expect(text).toContain('2.40 积分')
    expect(text).toContain('12.00 积分')
    // 倍率列:原倍率划线 + 专属倍率
    // 原倍率划线 + 专属倍率由页头 GroupBadge 渲染，表内只按生效倍率算价
    expect(wrapper.find('td .line-through').exists()).toBe(false)
  })

  it('模型按官方输出价从高到低排序,无官方价的排最后', () => {
    const expensive = tokenModel({
      name: 'model-expensive',
      official_pricing: {
        input_price: 1e-5,
        output_price: 7.5e-5,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })
    const cheap = tokenModel({
      name: 'model-cheap',
      official_pricing: {
        input_price: 1e-6,
        output_price: 5e-6,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })
    const noOfficial = tokenModel({ name: 'model-no-official', official_pricing: null })

    const wrapper = mountTable([cheap, noOfficial, expensive], 1)
    const names = wrapper
      .findAll('tbody tr')
      .map((tr) => tr.find('[data-testid="model-name"]').text())
    expect(names).toEqual(['model-expensive', 'model-cheap', 'model-no-official'])
  })

  it('文本分区把缓存拆成写/读两列,官方三列接在同一行', () => {
    const wrapper = mountTable([tokenModel()], 1)
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.section.text')
    expect(text).toContain('modelPlaza.table.officialPrice')
    // 模型 + 实付 4 列(缓存拆写/读) + 官方 3 列
    expect(wrapper.findAll('tbody td')).toHaveLength(8)
  })

  it('官方价包含 1h 缓存写入价;official_pricing 为 null 时官方三列显示 -', () => {
    const withOfficial = mountTable([tokenModel()], 1)
    expect(withOfficial.text()).toContain('$6.00')
    expect(withOfficial.text()).toContain('(1h')

    const withoutOfficial = mountTable([tokenModel({ official_pricing: null })], 1)
    const cells = withoutOfficial.findAll('tbody td')
    // 实付列是 模型/输入/输出/缓存写/缓存读 = 索引 0..4，官方三列从 5 起
    expect(cells[5].text().trim()).toBe('-')
    expect(cells[6].text().trim()).toBe('-')
    expect(cells[7].text().trim()).toBe('-')
  })

  it('per_request 模型按单次价 × 倍率展示,官方价列显示 -', () => {
    const model = tokenModel({
      name: 'search-tool',
      pricing: {
        billing_mode: 'per_request',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: 0.04,
        intervals: []
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.5)
    const text = wrapper.text()
    // 0.04 × 0.5 = 0.02,scale=1
    expect(text).toContain('0.02 积分')
    // 计费类型改由分区标题 + 单位后缀表达，不再逐行挂徽章
    expect(text).toContain('modelPlaza.section.image')
    // 单位后缀跟在价格后(按次 → / 次)
    expect(text).toContain('modelPlaza.table.perUnitRequest')
  })

  it('token 模型阶梯定价内联进输入/输出列,按倍率折算', () => {
    const model = tokenModel({
      pricing: {
        billing_mode: 'token',
        input_price: 3e-6,
        output_price: 1.5e-5,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: null,
        intervals: [
          {
            min_tokens: 0,
            max_tokens: 200000,
            tier_label: '',
            input_price: 3e-6,
            output_price: 1.5e-5,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: null
          },
          {
            min_tokens: 200000,
            max_tokens: null,
            tier_label: '',
            input_price: 6e-6,
            output_price: 3e-5,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: null
          }
        ]
      }
    })
    const wrapper = mountTable([model], 0.5)
    const text = wrapper.text()
    // 区间标签按 token 数生成
    expect(text).toContain('≤200K')
    expect(text).toContain('>200K')
    // 折后:输入 1.5 / 3,输出 7.5 / 15
    expect(text).toContain('1.50 积分')
    expect(text).toContain('7.50 积分')
    expect(text).toContain('15.00 积分')
  })

  it('按图模型主行展示阶梯芯片,不把 image_output_price(每 token)当按次价', () => {
    const model = tokenModel({
      name: 'gpt-image-2',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        // 每 token 图片输出价:不应被当作按次单价展示
        image_output_price: 3e-5,
        per_request_price: null,
        intervals: [
          {
            min_tokens: 0,
            max_tokens: null,
            tier_label: '1K',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: 0.01
          },
          {
            min_tokens: 0,
            max_tokens: null,
            tier_label: '2K',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: 0.02
          }
        ]
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.1)
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.section.image')
    // 芯片:1K $0.001 / 2K $0.002,单位后缀内嵌(按图 → / 张)
    expect(text).toContain('1K')
    expect(text).toContain('0.001 积分')
    expect(text).toContain('2K')
    expect(text).toContain('0.002 积分')
    expect(text).toContain('modelPlaza.table.perUnitImage')
    // 旧 bug:image_output_price × 0.1 = 0.000003 被当按次价
    expect(text).not.toContain('0.000003 积分')
  })
})

describe('PlazaModelPricingTable 官方价列默认隐藏', () => {
  // 站点按积分计价,官方参考价是 USD/token,不同量纲并排会让人误判加价倍数,
  // 且会暴露中转上游成本 —— 故组件默认不渲染官方列。
  function mountDefault(models: PlazaModel[], rateMultiplier = 1) {
    return mount(PlazaModelPricingTable, { props: { models, rateMultiplier } })
  }

  it('默认不渲染官方列,只剩模型 + 实付四列', () => {
    const wrapper = mountDefault([tokenModel()])
    expect(wrapper.text()).toContain('modelPlaza.section.text')
    expect(wrapper.text()).not.toContain('modelPlaza.table.officialPrice')
    expect(wrapper.findAll('tbody td')).toHaveLength(5)
  })

  it('默认形态下不出现美元符号,实付价一律以积分计', () => {
    const text = mountDefault([tokenModel()], 0.5).text()
    expect(text).not.toContain('$')
    expect(text).toContain('1.50 积分')
    expect(text).toContain('7.50 积分')
  })

  it('显式传 showOfficialPricing 可整列恢复,官方价仍按美元展示', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: { models: [tokenModel()], rateMultiplier: 1, showOfficialPricing: true }
    })
    expect(wrapper.text()).toContain('modelPlaza.table.officialPrice')
    expect(wrapper.findAll('tbody td')).toHaveLength(8)
    expect(wrapper.text()).toContain('$3.00')
  })
})

describe('PlazaModelPricingTable 分区', () => {
  function imageModel(name: string): PlazaModel {
    return {
      name,
      platform: 'openai',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: 0.5,
        intervals: []
      },
      official_pricing: null
    }
  }

  it('文本 / 生图 / 视频各自成区,空分区不占版面', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [tokenModel({ name: 'deepseek-v4-flash' }), imageModel('t-gpt-image-2')],
        rateMultiplier: 1
      }
    })
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.section.text')
    expect(text).toContain('modelPlaza.section.image')
    // 没有视频模型时不渲染视频区
    expect(text).not.toContain('modelPlaza.section.video')
    expect(wrapper.findAll('table')).toHaveLength(2)
  })

  /**
   * 线上视频模型的 billing_mode 配的是 image（它走图片接口那条闸门），
   * 只看 billing_mode 会把视频混进生图区,所以按模型名判。
   */
  it('视频模型按名字归入视频区,即使 billing_mode 是 image', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: { models: [imageModel('t-grok-video-1.5')], rateMultiplier: 1 }
    })
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.section.video')
    expect(text).not.toContain('modelPlaza.section.image')
    // 视频单位是每秒,不是每张
    expect(text).toContain('modelPlaza.section.unitPerSecond')
  })

  /**
   * 回归:模型名单元格必须有左内边距。原来只有 pr-4，文字贴着表格左边缘，
   * 外层又是 overflow-x-auto，首字母会被切掉。
   */
  it('模型名单元格有左内边距,不会贴着表格边缘被切', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: { models: [tokenModel()], rateMultiplier: 1 }
    })
    const firstCell = wrapper.find('tbody td')
    expect(firstCell.classes()).toContain('px-3')
  })

  it('每个模型前面带厂商标识', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: { models: [tokenModel({ name: 'deepseek-v4-flash' })], rateMultiplier: 1 }
    })
    expect(wrapper.findComponent({ name: 'ModelBrandMark' }).exists()).toBe(true)
  })
})


describe('PlazaModelPricingTable 视频每秒价', () => {
  function videoModel(pricing: Record<string, number | null>): PlazaModel {
    return {
      name: 't-grok-video-1.5',
      platform: 'grok',
      pricing: null,
      official_pricing: null,
      video_pricing: pricing as never
    }
  }

  /**
   * 回归：视频价存在分组的 video_price_* 三列上，不在渠道定价表里，走单独字段。
   * 后端补出来了但表格没渲染，线上就会看到「视频生成」分区里单价是 -。
   */
  it('渲染视频每秒单价,而不是显示 -', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [
          videoModel({
            price_per_second_480p: 37,
            price_per_second_720p: 37,
            price_per_second_1080p: 37
          })
        ],
        rateMultiplier: 1
      }
    })
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.section.video')
    expect(text).toContain('37.00 积分')
    expect(text).toContain('modelPlaza.section.unitPerSecond')
  })

  it('三档同价时只显示一条,不重复三个一样的芯片', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [
          videoModel({
            price_per_second_480p: 37,
            price_per_second_720p: 37,
            price_per_second_1080p: 37
          })
        ],
        rateMultiplier: 1
      }
    })
    expect(wrapper.text().match(/37\.00 积分/g)).toHaveLength(1)
  })

  it('三档不同价时按清晰度分别标注', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [
          videoModel({
            price_per_second_480p: 20,
            price_per_second_720p: 37,
            price_per_second_1080p: 55
          })
        ],
        rateMultiplier: 1
      }
    })
    const text = wrapper.text()
    expect(text).toContain('480p')
    expect(text).toContain('20.00 积分')
    expect(text).toContain('1080p')
    expect(text).toContain('55.00 积分')
  })

  it('倍率对视频每秒价同样生效', () => {
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [
          videoModel({
            price_per_second_480p: 37,
            price_per_second_720p: 37,
            price_per_second_1080p: 37
          })
        ],
        rateMultiplier: 0.5
      }
    })
    expect(wrapper.text()).toContain('18.50 积分')
  })
})
