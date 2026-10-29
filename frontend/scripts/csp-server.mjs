// Serves the production build (dist/) with the backend's real Content-Security-Policy,
// so e2e-csp/ can check that what ships still works under it. The policy string is
// copied from backend/internal/server/security_headers.go (defaultCSP);
// backend/internal/server/security_headers_worker_test.go fails if that drifts.
import http from 'http'
import fs from 'fs'
import path from 'path'

const dist = path.resolve(process.argv[2] ?? 'dist')
const port = Number(process.argv[3] ?? 5199)
const CSP =
  "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
const types = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon', '.woff2': 'font/woff2' }

http
  .createServer((req, res) => {
    res.setHeader('Content-Security-Policy', CSP)
    res.setHeader('X-Content-Type-Options', 'nosniff')
    res.setHeader('X-Frame-Options', 'DENY')
    const p = decodeURIComponent(new URL(req.url, 'http://x').pathname)
    let f = path.join(dist, p)
    // Not f.startsWith(dist): that also accepts a sibling such as dist-private.
    if (!f.startsWith(dist + path.sep) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) f = path.join(dist, 'index.html')
    res.setHeader('Content-Type', types[path.extname(f)] ?? 'application/octet-stream')
    fs.createReadStream(f).pipe(res)
  })
  .listen(port, '127.0.0.1')
