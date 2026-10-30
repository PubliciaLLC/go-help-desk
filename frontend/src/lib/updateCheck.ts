// The one question #170 asks, worded once so setup, the post-upgrade wizard and
// Settings cannot drift apart. It says who is contacted and that nothing about
// this server is sent. The request is a GET with a fixed User-Agent
// ("go-help-desk", no version) and a constant Accept header; a backend test
// pins the exact headers, so a change there must change this wording.
export const UPDATE_CHECK_LABEL =
  'Check for new releases (contacts GitHub once a week; sends nothing about your server)'

// For the places with room to say more.
export const UPDATE_CHECK_DETAIL =
  'Once a week this server asks GitHub which release is the latest and compares it with the one you run. ' +
  'The request is a plain page fetch with a fixed name (go-help-desk) and no version number, instance ' +
  'identifier or usage data. GitHub does see the server\u2019s IP address, as any website would. Nothing is ' +
  'ever installed for you. When a newer release exists, administrators see a small notice that can be dismissed. ' +
  'Off unless you turn it on; while off, no request is made.'
