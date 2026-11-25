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
  var tc = document.querySelector('meta[name="theme-color"]')
  if (tc) tc.setAttribute('content', dark ? '#101828' : '#ffffff')
})()
