// Runs the aiplex CLI against the harness as one of its callers:
//   node e2e/cli.mjs harness.json admin usage records -o json
import fs from 'node:fs'
import { spawnSync } from 'node:child_process'

const [file, who, ...rest] = process.argv.slice(2)
const h = JSON.parse(fs.readFileSync(file, 'utf8'))
const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token }[who]
const r = spawnSync(process.env.AIPLEX_BIN ?? 'aiplex', rest, { env: { ...process.env, AIPLEX_URL: h.api_url, AIPLEX_TOKEN: token }, encoding: 'utf8' })
process.stdout.write(r.stdout)
process.stderr.write(r.stderr)
process.exitCode = r.status ?? 1
