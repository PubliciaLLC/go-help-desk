// Saves a file the browser already holds as a URL: a PNG from a canvas, or CSV
// text encoded into a data: URL. The link is made, clicked and removed in one
// go; nothing is kept on the page.
//
// It deliberately makes no Blob and no object URL. Attachments are download-only
// (CLAUDE.md, #165), and the test that enforces that refuses both anywhere in
// the frontend, so the files this app saves are always data: URLs.
export function saveDataUrl(url: string, filename: string): void {
  const a = document.createElement('a')
  a.setAttribute('download', filename)
  a.setAttribute('href', url)
  document.body.append(a)
  a.click()
  a.remove()
}
