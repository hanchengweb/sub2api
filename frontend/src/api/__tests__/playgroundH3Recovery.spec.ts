import { afterEach, describe, expect, it, vi } from 'vitest'
import { createVideoTask, getVideoTask, isFailedStatus, isTerminalStatus, mediaUrlsOf } from '../playground'

afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })
const originalId = `csh3-${'a'.repeat(56)}`
const unknownReceipt = { id: originalId, request_id: originalId, status: 'unknown', error: { code: 'h3_submission_unknown' } }
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })

describe('H3 unknown submission recovery', () => {
  it('keeps a controlled 502 receipt and queries only its original ID', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(json(unknownReceipt, 502)).mockResolvedValueOnce(json(unknownReceipt, 202))
    vi.stubGlobal('fetch', fetcher)
    const task = await createVideoTask('MiniMax-H3', 'clip', { idempotencyKey: 'h3-original' })
    expect(task).toMatchObject({ ...unknownReceipt, idempotencyKey: 'h3-original' })
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0][1].headers['Idempotency-Key']).toBe('h3-original')
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('idempotencyKey')
    expect(await getVideoTask(task.id!)).toMatchObject({ id: originalId, status: 'unknown' })
    expect(fetcher.mock.calls[1][0]).toContain(`/playground/videos/${originalId}`)
    expect(fetcher.mock.calls[1][1].method).toBeUndefined()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('does not accept missing, mismatched or unsafe IDs as pending receipts', async () => {
    for (const receipt of [
      { ...unknownReceipt, id: undefined }, { ...unknownReceipt, id: '../other-task' },
      { ...unknownReceipt, request_id: `csh3-${'b'.repeat(56)}` }, { ...unknownReceipt, status: 'failed' }
    ]) {
      const fetcher = vi.fn().mockResolvedValue(json(receipt, 502))
      vi.stubGlobal('fetch', fetcher)
      await expect(createVideoTask('MiniMax-H3', 'clip', { idempotencyKey: 'h3-original' }))
        .rejects.toMatchObject({ code: 'h3_submission_unknown', idempotencyKey: 'h3-original', noRetry: true })
      expect(fetcher).toHaveBeenCalledTimes(1)
    }
  })

  it('preserves the original network error and key without another POST', async () => {
    const error = new TypeError('Network connection lost')
    const fetcher = vi.fn().mockRejectedValue(error)
    vi.stubGlobal('fetch', fetcher)
    await expect(createVideoTask('MiniMax-H3', 'clip', { idempotencyKey: 'h3-original' }))
      .rejects.toBe(error)
    expect(error).toMatchObject({ code: 'h3_submission_unknown', idempotencyKey: 'h3-original', noRetry: true })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('preserves the key when a successful response body cannot be read', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('partial JSON', { status: 200 }))
    vi.stubGlobal('fetch', fetcher)
    await expect(createVideoTask('MiniMax-H3', 'clip', { idempotencyKey: 'h3-original' }))
      .rejects.toMatchObject({ code: 'h3_submission_unknown', idempotencyKey: 'h3-original', noRetry: true })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('gives separate intentional submissions separate keys and leaves other models unchanged', async () => {
    const fetcher = vi.fn().mockImplementation(async () => json({ id: 'provider-task' }))
    vi.stubGlobal('fetch', fetcher)
    await createVideoTask('MiniMax-H3', 'clip', {})
    await createVideoTask('MiniMax-H3', 'clip', {})
    const keys = fetcher.mock.calls.map(([, init]) => init.headers['Idempotency-Key'])
    expect(keys[0]).toMatch(/^h3-/)
    expect(keys[0]).not.toBe(keys[1])
    fetcher.mockResolvedValueOnce(json(unknownReceipt, 502))
    await expect(createVideoTask('other-video', 'clip', {})).rejects.toMatchObject({ status: 502 })
    expect(fetcher.mock.calls[2][1].headers).not.toHaveProperty('Idempotency-Key')
  })

  it('reads normalized H3 video results and recognizes blocked as final failure', () => {
    expect(mediaUrlsOf({ status: 'succeeded', video: { url: '/api/v1/playground/videos/task/content' } }))
      .toEqual(['/api/v1/playground/videos/task/content'])
    expect(isTerminalStatus('unknown')).toBe(false)
    expect(isTerminalStatus('blocked')).toBe(true)
    expect(isFailedStatus('blocked')).toBe(true)
  })
})
