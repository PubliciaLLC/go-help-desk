// Sets the theme before first paint (#709). Same rules as src/lib/theme.ts;
// a file and not an inline script because the CSP is script-src 'self'.
(function () {
  var p = null
  try { p = window.localStorage.getItem('ghd.theme') } catch (e) {}
  if (p !== 'light' && p !== 'dark') p = 'device'
  var dark = p === 'dark' ||
    (p === 'device' && !!window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)
  var html = document.documentElement
  if (dark) html.classList.add('dark')
  var cs = document.querySelector('meta[name="color-scheme"]')
  if (cs) cs.setAttribute('content', dark ? 'dark' : 'light')
  // Browsers use the first theme-color meta. When the server adds a second one
  // (an uploaded icon's background, #708) it is the light colour; the first is
  // the one this script and theme.ts drive.
  var tcs = document.querySelectorAll('meta[name="theme-color"]')
  if (tcs.length) {
    var light = tcs.length > 1 ? tcs[tcs.length - 1].getAttribute('content') : '#ffffff'
    tcs[0].setAttribute('content', dark ? '#101828' : light)
  }
})()
