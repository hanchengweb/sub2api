import { describe, expect, it } from 'vitest'
import { getDomesticModelProvider, isDomesticModel } from '@/utils/domesticModels'

describe('domestic model classification', () => {
  it('recognizes domestic model families and hides foreign models', () => {
    expect(getDomesticModelProvider('deepseek/deepseek-v3')).toBe('deepseek')
    expect(getDomesticModelProvider('qwen3-235b-a22b')).toBe('qwen')
    expect(getDomesticModelProvider('glm-4.6')).toBe('zhipu')
    expect(getDomesticModelProvider('01-ai/yi-large')).toBe('yi')
    expect(isDomesticModel('claude-sonnet-4-6')).toBe(false)
    expect(isDomesticModel('gpt-5.5')).toBe(false)
    expect(isDomesticModel('gemini-3-pro-preview')).toBe(false)
    expect(isDomesticModel('grok-4.5')).toBe(false)
    expect(getDomesticModelProvider('anthropic')).toBe(null)
    expect(getDomesticModelProvider('openai')).toBe(null)
    expect(getDomesticModelProvider('gemini')).toBe(null)
    expect(getDomesticModelProvider('grok')).toBe(null)
  })
})
