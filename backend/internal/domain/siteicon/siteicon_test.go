package siteicon

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckSide(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want SizeKind // zero means accepted
	}{
		{"smallest", 512, 512, 0},
		{"largest", 4096, 4096, 0},
		{"1024", 1024, 1024, 0},
		{"one under the minimum", 511, 511, TooSmall},
		{"one over the maximum", 4097, 4097, TooLarge},
		{"one pixel wider", 513, 512, NotSquare},
		{"one pixel taller", 512, 513, NotSquare},
		// Shape is judged first: a 600 x 400 image is not square, and saying it
		// is too small would send the administrator to the wrong fix.
		{"landscape that is also under the minimum", 600, 400, NotSquare},
		{"zero", 0, 0, TooSmall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSide(tc.w, tc.h)
			if tc.want == 0 {
				require.NoError(t, err)
				return
			}
			var se *SizeError
			require.True(t, errors.As(err, &se), "got %v", err)
			require.Equal(t, tc.want, se.Kind)
			require.Equal(t, tc.w, se.W)
			require.Equal(t, tc.h, se.H)
		})
	}
}

func TestParseBackground(t *testing.T) {
	ok := []struct{ in, want string }{
		{"#ffffff", "#ffffff"},
		{"#FFFFFF", "#ffffff"},
		{"#1a2B3c", "#1a2b3c"},
		{"#000000", "#000000"},
	}
	for _, tc := range ok {
		got, err := ParseBackground(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got)
	}
	for _, in := range []string{"", "#fff", "ffffff", "#gggggg", "white", "#ffffff ", " #ffffff", "#ffffff\n", "#fffffff", "#ffff"} {
		_, err := ParseBackground(in)
		require.ErrorIs(t, err, ErrInvalidBackground, "%q", in)
	}
}

func TestContentTypeAndSizes(t *testing.T) {
	cases := []struct {
		name  string
		ctype string
		sizes []int
	}{
		{"favicon.ico", "image/x-icon", []int{16, 32, 48}},
		{"favicon-16.png", "image/png", []int{16}},
		{"favicon-32.png", "image/png", []int{32}},
		{"apple-touch-icon.png", "image/png", []int{180}},
		{"icon-192.png", "image/png", []int{192}},
		{"icon-512.png", "image/png", []int{512}},
		{"icon-maskable-512.png", "image/png", []int{512}},
	}
	require.Len(t, Served, len(cases))
	for i, tc := range cases {
		require.Equal(t, tc.name, Served[i], "the served order is part of the version digest")
		ct, ok := ContentType(tc.name)
		require.True(t, ok)
		require.Equal(t, tc.ctype, ct)
		require.Equal(t, tc.sizes, Sizes(tc.name))
	}
	for _, name := range []string{Master, "foo.png", "", "../favicon.ico"} {
		_, ok := ContentType(name)
		require.False(t, ok, "%q is not served", name)
	}
}

func TestVersion(t *testing.T) {
	files := func() Files {
		f := Files{Master: []byte("master")}
		for _, n := range Served {
			f[n] = []byte("data of " + n)
		}
		return f
	}

	base := Version(files())
	require.Regexp(t, `^[0-9a-f]{16}$`, base)
	require.Equal(t, base, Version(files()), "deterministic, whatever the map order")

	for _, n := range Served {
		f := files()
		f[n] = append(f[n], 'x')
		require.NotEqual(t, base, Version(f), "changing %s must change the version", n)
	}

	f := files()
	f[Master] = []byte("another master")
	require.Equal(t, base, Version(f), "the master is never served, so it is not part of the version")

	// The digest binds a file to its name: swapping two files' contents is a
	// different set of files.
	f = files()
	f["icon-192.png"], f["icon-512.png"] = f["icon-512.png"], f["icon-192.png"]
	require.NotEqual(t, base, Version(f))
}
