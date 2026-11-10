// Generates realistic traffic against a running harness (tools/consolee2e):
// JSON and streaming chat, MCP tool calls, a denied request, an unknown model
// and requests from a second caller. Usage:
//   node e2e/traffic.mjs harness.json
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'

const h = JSON.parse(fs.readFileSync(process.argv[2] ?? 'harness.json', 'utf8'))
const A = { Authorization: `Bearer ${h.admin_token}`, 'Content-Type': 'application/json' }
const B = { Authorization: `Bearer ${h.norole_token}`, 'Content-Type': 'application/json' }
const post = async (path, body, headers = A) => {
  const r = await fetch(h.api_url + path, { method: 'POST', headers, body: typeof body === 'string' ? body : JSON.stringify(body) })
  const text = await r.text()
  return { status: r.status, text }
}
const chat = (ap, model, stream = false, headers = A, content = 'Hello') =>
  post(`/ap/${ap}/v1/chat/completions`, { model, stream, ...(stream ? { stream_options: { include_usage: true } } : {}), messages: [{ role: 'user', content }] }, headers)
const mcp = async (ap, method, params, headers = A, id = 1) =>
  post(`/ap/${ap}/mcp`, { jsonrpc: '2.0', id, method, params }, { ...headers, Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-06-18' })

const cli = (...args) => execFileSync(process.env.AIPLEX_BIN ?? 'aiplex', args, { env: { ...process.env, AIPLEX_URL: h.api_url, AIPLEX_TOKEN: h.admin_token }, encoding: 'utf8' })
cli('agents', 'user-scopes', 'set', h.norole_principal, '--scopes', 'llm:model:mock-gpt,llm:surface:local-models:*,mcp:tools:*')

const results = []
const note = (label, r) => { results.push(`${label}: ${r.status}`); return r }
for (let i = 0; i < 8; i++) note('chat json', await chat('local', 'mock-gpt', false, A, `Question ${i}: explain parabolic motion`))
for (let i = 0; i < 4; i++) note('chat stream', await chat('local', 'mock-gpt', true, A, `Stream ${i}`))
note('alias fast', await chat('team-research', 'fast'))
note('alias smart (anthropic surface)', await post('/ap/team-research/v1/messages', { model: 'smart', max_tokens: 16, messages: [{ role: 'user', content: 'hi' }] }, { ...A, 'anthropic-version': '2023-06-01' }))
note('gemini', await post('/ap/team-research/v1beta/models/gemini-2.5-flash:generateContent', { contents: [{ role: 'user', parts: [{ text: 'hi' }] }] }))
note('unknown model', await chat('local', 'no-such-model'))
note('denied access point', await chat('private', 'mock-gpt', false, B))
note('batch-pool request', await chat('batch-pool', 'mock-gpt', false, A, 'bulk'))
await mcp('local', 'initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
for (let i = 0; i < 3; i++) note('tool echo', await mcp('local', 'tools/call', { name: 'local-tools__echo', arguments: { text: `hello ${i}` } }, A, 10 + i))
note('tool add', await mcp('local', 'tools/call', { name: 'local-tools__add', arguments: { a: 2, b: 3 } }, A, 20))
note('tool missing', await mcp('local', 'tools/call', { name: 'local-tools__nope', arguments: {} }, A, 21))
// Second caller (bob, no role).
note('bob chat', await chat('local', 'mock-gpt', false, B, 'bob here'))
note('bob stream', await chat('local', 'mock-gpt', true, B, 'bob streams'))
await mcp('local', 'initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'bob', version: '1' } }, B)
note('bob tool', await mcp('local', 'tools/call', { name: 'local-tools__echo', arguments: { text: 'bob' } }, B, 30))
note('bob denied model', await chat('local', 'claude-haiku-4-5', false, B))
console.log(results.join('\n'))
