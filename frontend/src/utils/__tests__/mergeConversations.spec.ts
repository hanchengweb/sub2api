import { describe, expect, it } from 'vitest'
import { mergeConversations, type PlaygroundConversation } from '@/utils/playgroundStore'

function conv(id: string, updatedAt: number, title = id): PlaygroundConversation {
  return { id, title, mode: 'chat', model: 'm', messages: [], updatedAt }
}

describe('mergeConversations', () => {
  // 同步上线前的历史全在 localStorage 里，服务端一条都没有。
  // 直接用服务端列表覆盖，等于把用户上线前的会话全删了。
  it('keeps local-only conversations and flags them for upload', () => {
    const { merged, localOnly } = mergeConversations([conv('a', 200), conv('b', 100)], [])

    expect(merged.map(c => c.id)).toEqual(['a', 'b'])
    expect(localOnly.map(c => c.id)).toEqual(['a', 'b'])
  })

  it('takes the newer side for the same id', () => {
    const { merged, localOnly } = mergeConversations(
      [conv('a', 300, 'local-newer')],
      [conv('a', 200, 'remote-older')]
    )

    expect(merged).toHaveLength(1)
    expect(merged[0].title).toBe('local-newer')
    expect(localOnly.map(c => c.id)).toEqual(['a'])
  })

  it('prefers the remote side when it is newer and does not re-upload it', () => {
    const { merged, localOnly } = mergeConversations(
      [conv('a', 100, 'local-older')],
      [conv('a', 400, 'remote-newer')]
    )

    expect(merged[0].title).toBe('remote-newer')
    expect(localOnly).toEqual([])
  })

  it('orders by updatedAt descending', () => {
    const { merged } = mergeConversations([conv('old', 1)], [conv('new', 999), conv('mid', 500)])
    expect(merged.map(c => c.id)).toEqual(['new', 'mid', 'old'])
  })

  // 超出上限的会话本地本来就存不住，补推上去只会被服务端裁剪再删一遍。
  it('caps at 50 and never reports a dropped conversation as pending upload', () => {
    const local = Array.from({ length: 60 }, (_, i) => conv(`l${i}`, i))
    const { merged, localOnly } = mergeConversations(local, [])

    expect(merged).toHaveLength(50)
    expect(localOnly).toHaveLength(50)
    expect(merged.every(c => c.updatedAt >= 10)).toBe(true)
  })

  it('returns an empty result for two empty sides', () => {
    expect(mergeConversations([], [])).toEqual({ merged: [], localOnly: [] })
  })
})
