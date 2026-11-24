package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The sanitiser matched "javascript:" in the raw bytes. XML lets the same text
// be written with character references, which the raw match misses entirely —
// and a browser decodes an attribute value before it follows it.
func TestSanitizeSVG_RefusesEncodedScriptURLs(t *testing.T) {
	for _, tc := range []struct{ name, svg string }{
		{
			name: "decimal reference",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg"><a xlink:href="java&#115;cript:alert(1)"><text>x</text></a></svg>`,
		},
		{
			name: "hex reference",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg"><a href="&#x6A;avascript:alert(1)"><text>x</text></a></svg>`,
		},
		{
			name: "animate that sets href",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg"><a><animate attributeName="href" values="java&#115;cript:alert(1)"/></a></svg>`,
		},
		{
			name: "plain, which always worked",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><text>x</text></a></svg>`,
		},
		{
			name: "script element",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		},
		{
			name: "event handler",
			svg:  `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, sanitizeSVG([]byte(tc.svg)), "this must be refused")
		})
	}
}

// An ordinary logo must still upload. A sanitiser that refused everything would
// pass the test above.
func TestSanitizeSVG_AcceptsAPlainLogo(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
		`<circle cx="50" cy="50" r="40" fill="#336699"/>` +
		`<text x="50" y="55" text-anchor="middle" fill="white">GHD</text></svg>`
	require.NoError(t, sanitizeSVG([]byte(svg)))
}

// Text that merely contains a reference must not be mangled into a false match.
func TestDecodeXMLRefs_LeavesOrdinaryContentAlone(t *testing.T) {
	svg := []byte(`<svg><text>Caf&#233; &amp; Bar &#x2014; open</text></svg>`)
	require.NoError(t, sanitizeSVG(svg))
	require.Contains(t, string(decodeXMLRefs(svg)), "Café")
}
