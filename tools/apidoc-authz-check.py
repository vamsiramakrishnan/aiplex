#!/usr/bin/env python3
"""Check the API reference's authorization claims against a running API.

For every route listed in docs-site/docs/api/*.md, call it as a signed-in caller
with no role and compare the answer with what the page says. Run it against
`aiplex dev` (a development stack; write routes are called with an empty body):

    aiplex dev --detach --quota && eval "$(aiplex dev env)"
    python3 tools/apidoc-authz-check.py "$AIPLEX_URL" "$(aiplex dev token --user bob)" "$AIPLEX_TOKEN" docs-site/docs/api

AUTHZ lines are pages that promise access the API refuses; CODE lines name the
wrong 403 error code. NO403 lines are routes with no per-route error table.
"""
import glob, json, os, re, sys, urllib.error, urllib.request

if len(sys.argv) != 5:
    sys.exit(__doc__)
url, bob, admin, d = sys.argv[1:5]
row = re.compile(r'^\|\s*(GET|POST|PUT|PATCH|DELETE)\s*\|\s*`([^`]+)`\s*\|([^|]*)\|([^|]*)\|')
sec = re.compile(r'^(#{2,4})\s+(GET|POST|PUT|PATCH|DELETE)\s+`?(/[^`\s]+)`?')
err = re.compile(r'^\|\s*(\d{3})\s*\|\s*`?([A-Za-z_]+)`?\s*\|')
def call(method, path, token):
    p = re.sub(r'\{[^}]+\}', 'zz-doc-check', path)
    p = p.replace('*', 'x')
    data = b'{}' if method in ('POST', 'PUT', 'PATCH') else None
    req = urllib.request.Request(url + p, data=data, method=method, headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, ''
    except urllib.error.HTTPError as e:
        body = e.read()[:2000]
        try: code = json.loads(body).get('code', '')
        except Exception: code = ''
        return e.code, code
    except Exception as e:
        return 0, type(e).__name__
out = []
for f in sorted(glob.glob(os.path.join(d, '*.md'))):
    lines = open(f).read().split('\n')
    routes = {}
    for l in lines:
        m = row.match(l)
        if m and m.group(2).startswith('/'):
            routes[(m.group(1), m.group(2))] = m.group(4).strip()
    documented = {}
    cur = None
    for l in lines:
        m = sec.match(l)
        if m: cur = (m.group(2), m.group(3)); curlevel = len(m.group(1)); documented.setdefault(cur, set()); continue
        h = re.match(r'^(#{1,6})\s', l)
        if h and cur and len(h.group(1)) <= curlevel: cur = None
        m = err.match(l)
        if m and cur and m.group(1) == '403': documented[cur].add(m.group(2))
    for (method, path), authz in routes.items():
        if method == 'DELETE' and '{' not in path: continue
        bs, bc = call(method, path, bob)
        if bs == 403:
            doc = documented.get((method, path), set())
            says_open = bool(re.search(r'verified identity|authenticated|any signed', authz, re.I)) and not re.search(r'admin|deployer|operator', authz, re.I)
            if says_open:
                out.append(('AUTHZ', os.path.basename(f), method, path, f'page says "{authz}" but a caller with no role gets 403 {bc}'))
            elif doc and bc not in doc:
                out.append(('CODE', os.path.basename(f), method, path, f'page lists 403 {sorted(doc)}; API returns 403 {bc}'))
            elif not doc:
                out.append(('NO403', os.path.basename(f), method, path, f'no 403 row; API returns 403 {bc} ("{authz}")'))
        elif bs in (200, 201, 204, 400, 404, 409, 412, 422) and re.search(r'admin|deployer|operator', authz, re.I) and not re.search(r'verified|authenticated|caller|owner|own', authz, re.I):
            a_s, _ = call(method, path, admin) if method == 'GET' else (None, None)
            out.append(('OPEN?', os.path.basename(f), method, path, f'page says "{authz}" but a caller with no role gets {bs} {bc}'))
for kind in ('AUTHZ', 'OPEN?', 'CODE', 'NO403'):
    rows = [o for o in out if o[0] == kind]
    print(f'## {kind}: {len(rows)}')
    for o in rows: print('  ', o[1], o[2], o[3], '::', o[4])
