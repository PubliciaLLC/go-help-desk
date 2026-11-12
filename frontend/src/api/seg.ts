// One path segment, safe to put in a request URL. Every variable an API helper
// writes into a path goes through here (a test scans for the ones that do not).
//
// encodeURIComponent alone is not enough: it leaves "." and ".." untouched, and
// a browser resolves those as directory moves, so a bare ".." walks the request
// up to another route. They, and the empty string (which collapses two
// segments into one), are refused rather than encoded.
export function seg(v: string): string {
  if (v === '' || v === '.' || v === '..') {
    throw new Error(`invalid path segment: ${JSON.stringify(v)}`)
  }
  return encodeURIComponent(v)
}
