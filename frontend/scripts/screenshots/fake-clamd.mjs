// Stand-in for clamd, used only by scripts/screenshots.sh.
//
// The app asks clamd for two things (backend/internal/antivirus/antivirus.go):
// zPING, answered PONG, and zINSTREAM, a length-prefixed stream answered with
// "stream: <name> FOUND" or "stream: OK". This answers FOUND for the EICAR test
// string and OK for everything else, so the quarantine path can be shown
// without ClamAV installed. Listens on 127.0.0.1 only.
//
// The EICAR string is stored base64-encoded so no raw test signature sits in
// the repository; it is decoded in memory at start-up.
//
//   FAKE_CLAMD_PORT=13310 node frontend/scripts/screenshots/fake-clamd.mjs

import net from 'node:net'

const port = Number(process.env.FAKE_CLAMD_PORT || 13310)
const EICAR = Buffer.from(
  'WDVPIVAlQEFQWzRcUFpYNTQoUF4pN0NDKTd9JEVJQ0FSLVNUQU5EQVJELUFOVElWSVJVUy1URVNULUZJTEUhJEgrSCo=',
  'base64',
)

const server = net.createServer((sock) => {
  let buf = Buffer.alloc(0)
  let mode = null
  const chunks = []
  sock.on('error', () => {})
  sock.on('data', (d) => {
    buf = Buffer.concat([buf, d])
    if (mode === null) {
      const nul = buf.indexOf(0)
      if (nul < 0) return
      mode = buf.subarray(0, nul).toString()
      buf = buf.subarray(nul + 1)
      if (mode === 'zPING') {
        sock.end('PONG\0')
        return
      }
      if (mode !== 'zINSTREAM') {
        sock.end('UNKNOWN COMMAND\0')
        return
      }
    }
    while (buf.length >= 4) {
      const len = buf.readUInt32BE(0)
      if (len === 0) {
        const data = Buffer.concat(chunks)
        sock.end(data.includes(EICAR) ? 'stream: Eicar-Test-Signature FOUND\0' : 'stream: OK\0')
        return
      }
      if (buf.length < 4 + len) return
      chunks.push(buf.subarray(4, 4 + len))
      buf = buf.subarray(4 + len)
    }
  })
})

server.on('error', (err) => {
  console.error(`fake clamd: cannot listen on 127.0.0.1:${port}: ${err.message}`)
  process.exit(1)
})
server.listen(port, '127.0.0.1', () => {
  console.log(`fake clamd listening on 127.0.0.1:${port}`)
})
