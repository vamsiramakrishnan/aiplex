// Serves the built Console (console/built) and proxies API paths to a running
// AIPlex API, the way `aiplex console` does, but WITHOUT injecting a token
// unless --inject-token is given. The browser script signs in by storing a
// token, so it can act as different callers against one server.
//
//   node e2e/serve.mjs --api http://127.0.0.1:18080 --port 19090 [--dist built] [--inject-token TOKEN]
import http from 'node:http'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const args = Object.fromEntries(process.argv.slice(2).reduce((acc, a, i, all) => (a.startsWith('--') ? [...acc, [a.slice(2), all[i + 1]]] : acc), []))
const api = new URL(args.api ?? 'http://127.0.0.1:18080')
const port = Number(args.port ?? 19090)
const dist = path.resolve(args.dist ?? path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'built'))
const inject = args['inject-token'] ?? ''
const proxied = ['/api/', '/auth/', '/healthz', '/readyz', '/v0.1/', '/events/', '/oauth2/', '/ap/']
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.json': 'application/json', '.png': 'image/png', '.ico': 'image/x-icon' }

http.createServer((req, res) => {
  if (proxied.some((p) => req.url.startsWith(p))) {
    const headers = { ...req.headers, host: api.host }
    if (inject) headers.authorization = `Bearer ${inject}`
    const up = http.request({ host: api.hostname, port: api.port, path: req.url, method: req.method, headers }, (r) => {
      res.writeHead(r.statusCode ?? 502, r.headers)
      r.pipe(res)
    })
    up.on('error', (e) => { res.writeHead(502); res.end(String(e)) })
    req.pipe(up)
    return
  }
  const clean = decodeURIComponent(req.url.split('?')[0])
  let file = path.join(dist, clean)
  if (!file.startsWith(dist) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(dist, 'index.html')
  res.writeHead(200, { 'content-type': types[path.extname(file)] ?? 'application/octet-stream' })
  fs.createReadStream(file).pipe(res)
}).listen(port, '127.0.0.1', () => console.log(`console on http://127.0.0.1:${port} -> ${api.href}`))
