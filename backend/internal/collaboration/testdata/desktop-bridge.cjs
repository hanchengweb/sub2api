'use strict'

const assert = require('node:assert/strict')
const { randomUUID } = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')
const { setTimeout: delay } = require('node:timers/promises')

async function main() {
  assert(process.env.WXM_DESKTOP_COLLABORATION_CLIENT, 'desktop source opt-in required')
  const endpoint = new URL(process.env.WXM_COLLABORATION_TEST_ENDPOINT)
  assert.equal(endpoint.hostname, '127.0.0.1')
  assert.equal(endpoint.protocol, 'http:')
  const directory = process.env.WXM_COLLABORATION_TEST_DIRECTORY
  assert(path.isAbsolute(directory))
  const { createClient } = require(process.env.WXM_DESKTOP_COLLABORATION_CLIENT)
  let requests = 0
  const fetchImpl = async (target, init) => {
    const requested = new URL(target)
    assert.equal(requested.origin, 'https://windhub.online')
    assert(requested.pathname.startsWith('/v1/desktop/collaboration/'))
    assert.equal(init.redirect, 'error')
    assert.equal(init.credentials, 'omit')
    requests++
    return fetch(new URL(requested.pathname + requested.search, endpoint), init)
  }
  const client = number => createClient({ connection: () => ({ key: `fixture-${number}`, mode: 'direct', baseUrl: 'https://windhub.online' }), fetchImpl })
  const owner = client(1), member = client(2)
  const ownerStatus = await owner.call({ action: 'status' })
  const memberStatus = await member.call({ action: 'status' })
  assert.equal(ownerStatus.billing, 'requester')
  assert.equal(ownerStatus.cloudExecution, true)
  assert.deepEqual(ownerStatus.models, [{ id: 'deepseek-chat', name: 'DeepSeek' }])
  assert.notEqual(ownerStatus.accountKey, memberStatus.accountKey)
  assert.notEqual(ownerStatus.user.id, memberStatus.user.id)
  const call = (c, status, action, input = {}) => c.call({ action, accountKey: status.accountKey, clientRequestId: randomUUID(), ...input })
  const a = (action, input) => call(owner, ownerStatus, action, input)
  const b = (action, input) => call(member, memberStatus, action, input)

  const room = await a('create', { name: 'Cross-runtime room', projectName: 'Cross-runtime project' })
  assert(room.id && room.projectId)
  assert.equal(room.ownerId, ownerStatus.user.id)
  assert.equal((await a('rooms')).items[0].id, room.id)
  await assert.rejects(b('detail', { roomId: room.id }), error => error.code === 'HTTP_403')
  const invitation = await a('invite', { roomId: room.id })
  assert(invitation.id && invitation.token && invitation.expiresAt)
  const joined = await b('join', { token: invitation.token })
  assert.equal(joined.id, room.id)
  assert.equal(joined.role, 'member')
  const detail = await b('detail', { roomId: room.id })
  assert.equal(detail.members.length, 2)
  assert.equal(detail.project.name, 'Cross-runtime project')

  const agent = await a('addAgent', { roomId: room.id, name: '同名助手', instructions: '只讨论本群共享信息', model: 'deepseek-chat' })
  const otherAgent = await a('addAgent', { roomId: room.id, name: '同名助手', instructions: '根据群内资料回答', model: 'deepseek-chat' })
  assert.notEqual(agent.id, otherAgent.id)
  const plain = await b('send', { roomId: room.id, text: '@同名助手 这是一条普通消息', mentionIds: [] })
  assert.equal(plain.runs.length, 0)
  const requestId = randomUUID()
  const question = { roomId: room.id, text: '请两位助手分别回复', mentionIds: [agent.id, otherAgent.id], clientRequestId: requestId }
  const sent = await b('send', question)
  assert.equal(sent.runs.length, 2)
  const replayed = await b('send', question)
  assert.equal(replayed.message.id, sent.message.id)
  const deadline = Date.now() + 10000
  let runs
  do {
    runs = (await b('runs', { roomId: room.id })).items
    if (runs.length === 2 && runs.every(run => run.status === 'completed')) break
    await delay(100)
  } while (Date.now() < deadline)
  assert.equal(runs.length, 2)
  assert(runs.every(run => run.status === 'completed'))
  const messages = await b('messages', { roomId: room.id, after: 0 })
  assert.equal(messages.items.length, 4)
  assert.equal(messages.items.filter(message => message.kind === 'agent').length, 2)
  assert(messages.items.filter(message => message.kind === 'agent').every(message => message.replyToId === sent.message.id))
  assert.equal((await a('messages', { roomId: room.id, after: messages.cursor })).items.length, 0)

  const task = await a('createTask', { roomId: room.id, title: '跨端任务' })
  const updated = await b('updateTask', { roomId: room.id, taskId: task.id, status: 'doing', revision: task.revision })
  assert.equal(updated.status, 'doing')
  assert.equal(updated.revision, task.revision + 1)
  await assert.rejects(a('updateTask', { roomId: room.id, taskId: task.id, status: 'done', revision: task.revision }), error => error.code === 'HTTP_409')
  assert.equal((await b('tasks', { roomId: room.id })).items[0].revision, updated.revision)

  const uploadPath = path.join(directory, 'shared-notes.md')
  const content = '# 共享资料\n这份文件来自真实桌面上传实现。\n'
  fs.writeFileSync(uploadPath, content)
  const asset = await member.upload({ action: 'upload', accountKey: memberStatus.accountKey, roomId: room.id, clientRequestId: randomUUID() }, async () => ({ canceled: false, filePaths: [uploadPath] }))
  assert(asset.id && asset.name === 'shared-notes.md')
  assert.equal(asset.sizeBytes, Buffer.byteLength(content))
  assert.equal((await a('assets', { roomId: room.id })).items[0].id, asset.id)
  const attached = await b('send', { roomId: room.id, text: '', mentionIds: [], attachmentIds: [asset.id] })
  assert.equal(attached.message.attachments[0].id, asset.id)
  const refreshed = await a('messages', { roomId: room.id, after: messages.cursor })
  assert.equal(refreshed.items[0].attachments[0].contextNote, attached.message.attachments[0].contextNote)
  const downloaded = path.join(directory, 'downloaded-notes.md')
  const saved = await owner.download({ action: 'download', accountKey: ownerStatus.accountKey, roomId: room.id, assetId: asset.id, name: asset.name }, async name => {
    assert.equal(name, 'shared-notes.md')
    return { canceled: false, filePath: downloaded }
  })
  assert.equal(saved.saved, true)
  assert.equal(fs.readFileSync(downloaded, 'utf8'), content)

  await a('revokeInvite', { roomId: room.id, inviteId: invitation.id })
  await a('removeAgent', { roomId: room.id, agentId: otherAgent.id })
  assert.equal((await a('detail', { roomId: room.id })).agents.length, 1)
  await a('removeMember', { roomId: room.id, userId: memberStatus.user.id })
  await assert.rejects(b('messages', { roomId: room.id, after: 0 }), error => error.code === 'HTTP_403')
  await assert.rejects(member.download({ action: 'download', accountKey: memberStatus.accountKey, roomId: room.id, assetId: asset.id, name: asset.name }, async () => { throw Error('revoked download must not open save dialog') }), error => error.code === 'HTTP_403')
  await assert.rejects(b('join', { token: invitation.token }), error => error.code === 'HTTP_400')
  await assert.rejects(member.call({ action: 'rooms', accountKey: ownerStatus.accountKey }), error => error.code === 'ACCOUNT_CHANGED')
  assert(requests < 150, 'contract test must remain bounded')
  process.stdout.write(JSON.stringify({ bridgeContract: 'passed', requests, accounts: 2, syntheticRuns: 2, taskPatch: 'passed', attachmentRoundTrip: 'passed', revokedAccess: 'passed' }) + '\n')
}

main().catch(error => { process.stderr.write(error.stack + '\n'); process.exitCode = 1 })
