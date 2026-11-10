// Tiny API probe for debugging: node e2e/api.mjs harness.json admin|deployer|norole|none GET /api/v1/iam/whoami [body]
import fs from 'node:fs'
const [file, who, method, path, body] = process.argv.slice(2)
const h = JSON.parse(fs.readFileSync(file, 'utf8'))
const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token }[who]
const r = await fetch(h.api_url + path, { method, headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), 'Content-Type': 'application/json' }, body })
const text = await r.text()
console.log(r.status, text.slice(0, Number(process.env.MAX ?? 1200)))
