export type DomesticModelProvider =
  | 'deepseek'
  | 'qwen'
  | 'zhipu'
  | 'kimi'
  | 'doubao'
  | 'minimax'
  | 'wenxin'
  | 'spark'
  | 'hunyuan'
  | 'baichuan'
  | 'internlm'
  | 'yi'
  | 'stepfun'

const DOMESTIC_MODEL_MATCHERS: Array<{ provider: DomesticModelProvider; pattern: RegExp }> = [
  { provider: 'deepseek', pattern: /deepseek/i },
  { provider: 'qwen', pattern: /qwen|qwq/i },
  { provider: 'zhipu', pattern: /glm|chatglm|cogview|cogvideo/i },
  { provider: 'kimi', pattern: /kimi|moonshot/i },
  { provider: 'doubao', pattern: /doubao/i },
  { provider: 'minimax', pattern: /minimax|abab/i },
  { provider: 'wenxin', pattern: /ernie|wenxin/i },
  { provider: 'spark', pattern: /spark/i },
  { provider: 'hunyuan', pattern: /hunyuan/i },
  { provider: 'baichuan', pattern: /baichuan/i },
  { provider: 'internlm', pattern: /internlm/i },
  { provider: 'yi', pattern: /(?:^|[\/_:. -])yi(?:$|[\/_:. -])|01[-_. ]?ai/i },
  { provider: 'stepfun', pattern: /stepfun|step[-_ ]?星辰/i },
]

// ponytail: model-name heuristic, replace with provider metadata when the API exposes it.
export function getDomesticModelProvider(model: string): DomesticModelProvider | null {
  const normalized = model.trim()
  if (!normalized) return null
  return DOMESTIC_MODEL_MATCHERS.find(({ pattern }) => pattern.test(normalized))?.provider ?? null
}

export function isDomesticModel(model: string): boolean {
  return getDomesticModelProvider(model) !== null
}
