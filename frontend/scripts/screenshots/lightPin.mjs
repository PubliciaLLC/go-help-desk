// The website screenshots are always light (#709). capture.mjs sets a light
// colour scheme; this adds the other two halves: the browser's stored theme is
// light before every page load, and a shot of a page that is dark anyway (an
// account whose own setting is dark wins over the stored value) is refused.

/** Stores ghd.theme=light before the application runs on every page of the context. */
export async function pinLight(ctx) {
  await ctx.addInitScript(() => {
    try {
      window.localStorage.setItem('ghd.theme', 'light')
    } catch {
      /* storage refused: the light colour scheme still applies */
    }
  })
}

/** Throws when the page is in the dark theme. */
export async function assertLight(page, name) {
  const dark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
  if (dark) throw new Error(`shot ${name} is dark: screenshots must be light`)
}
