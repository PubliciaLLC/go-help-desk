// Stand-in for a mail server, used only by scripts/screenshots.sh.
//
// Since #483 the server refuses to turn on self-signup or guest submission
// while email is not configured, so the seed cannot run against a server with
// no SMTP_HOST. This accepts every message and writes it to FAKE_SMTP_DIR as
// 1.eml, 2.eml, ... The seed reads the guest ticket link out of one of them:
// the server stores only the hash of that link's token, so the mail is the only
// place the raw token exists.
//
// It is plain SMTP with no STARTTLS and no AUTH, on 127.0.0.1. The server
// allows that, and only that, to a private address when no SMTP_PASSWORD is set
// (internal/server/notify/email.go, "The TLS rule"). The messages hold working
// links, so the directory is created mode 0700 and the files 0600.
//
//   FAKE_SMTP_PORT=13325 FAKE_SMTP_DIR=/tmp/mail node frontend/scripts/screenshots/fake-smtp.mjs

import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

const port = Number(process.env.FAKE_SMTP_PORT || 13325)
const dir = process.env.FAKE_SMTP_DIR
if (!dir) {
  console.error('FAKE_SMTP_DIR is required')
  process.exit(2)
}
fs.mkdirSync(dir, { recursive: true, mode: 0o700 })

let count = 0

const server = net.createServer((sock) => {
  let buf = ''
  let data = null // the message so far, while in DATA
  sock.on('error', () => {})
  sock.write('220 fake-smtp ready\r\n')
  sock.on('data', (d) => {
    buf += d.toString('latin1')
    for (;;) {
      if (data !== null) {
        const end = buf.indexOf('\r\n.\r\n')
        if (end < 0) {
          return
        }
        data += buf.slice(0, end)
        buf = buf.slice(end + 5)
        // Undo dot-stuffing so the saved text is the message as sent.
        const msg = data.replace(/^\.\./gm, '.')
        data = null
        count++
        fs.writeFileSync(path.join(dir, `${count}.eml`), msg, { mode: 0o600, encoding: 'latin1' })
        sock.write('250 queued\r\n')
        continue
      }
      const nl = buf.indexOf('\r\n')
      if (nl < 0) return
      const line = buf.slice(0, nl)
      buf = buf.slice(nl + 2)
      const cmd = line.slice(0, 4).toUpperCase()
      if (cmd === 'EHLO' || cmd === 'HELO') sock.write('250 fake-smtp\r\n')
      else if (cmd === 'MAIL' || cmd === 'RCPT' || cmd === 'RSET' || cmd === 'NOOP') sock.write('250 ok\r\n')
      else if (cmd === 'DATA') {
        data = ''
        sock.write('354 end with <CRLF>.<CRLF>\r\n')
      } else if (cmd === 'QUIT') {
        sock.end('221 bye\r\n')
        return
      } else sock.write('502 not implemented\r\n')
    }
  })
})

server.listen(port, '127.0.0.1')
