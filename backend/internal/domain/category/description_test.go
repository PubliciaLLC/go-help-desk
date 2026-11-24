package category_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
)

// A description is plain text of at most 500 characters. The rule is the same
// at all three levels and on both create and update, so one table drives all of
// them: each case says what goes in and what is stored (or that it is refused).
func TestService_Description(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty means none", in: "", want: ""},
		{name: "whitespace only means none", in: " \n\t ", want: ""},
		{name: "trimmed", in: "  Laptops and docks.  \n", want: "Laptops and docks."},
		{name: "newlines inside are kept", in: "Line one\nLine two", want: "Line one\nLine two"},
		{name: "html-looking text is stored verbatim", in: `<b>x</b> & <script>alert(1)</script>`, want: `<b>x</b> & <script>alert(1)</script>`},
		{name: "markdown-looking text is stored verbatim", in: "**bold** [a](http://x)", want: "**bold** [a](http://x)"},
		{name: "500 characters is allowed", in: strings.Repeat("a", 500), want: strings.Repeat("a", 500)},
		{name: "501 characters is refused", in: strings.Repeat("a", 501), wantErr: true},
		{name: "500 two-byte characters is allowed (characters, not bytes)", in: strings.Repeat("é", 500), want: strings.Repeat("é", 500)},
		{name: "501 two-byte characters is refused", in: strings.Repeat("é", 501), wantErr: true},
		{name: "500 four-byte characters is allowed", in: strings.Repeat("😀", 500), want: strings.Repeat("😀", 500)},
		{name: "501 four-byte characters is refused", in: strings.Repeat("😀", 501), wantErr: true},
		{name: "trailing space does not count towards the limit", in: strings.Repeat("a", 500) + "   ", want: strings.Repeat("a", 500)},
		{name: "a NUL cannot be stored", in: "a\x00b", wantErr: true},
		// The browser's trim() and Go's TrimSpace disagree on these two; the server
		// strips both, and the form does the same (ctiDescription.ts), so a text
		// that passes there passes here. The byte order mark case is the regression
		// test (it failed before the server stripped it). The next-line case is a
		// pin only: Go's TrimSpace already stripped U+0085, so it passed before the
		// change and exists to keep it that way.
		{name: "a byte order mark at either end is trimmed", in: "\uFEFF" + strings.Repeat("a", 500) + "\uFEFF", want: strings.Repeat("a", 500)},
		{name: "a next-line character at either end is trimmed", in: "\u0085 text \u0085", want: "text"},
	}

	ctx := context.Background()
	type op func(svc *category.Service, desc string) (string, error)
	ops := map[string]op{
		"category/create": func(svc *category.Service, d string) (string, error) {
			c, err := svc.CreateCategoryWithDescription(ctx, "C", d, 1)
			if err != nil {
				return "", err
			}
			got, err := svc.GetCategory(ctx, c.ID)
			return got.Description, err
		},
		"category/update": func(svc *category.Service, d string) (string, error) {
			c, _ := svc.CreateCategory(ctx, "C", 1)
			c.Description = d
			if err := svc.UpdateCategory(ctx, c); err != nil {
				return "", err
			}
			got, err := svc.GetCategory(ctx, c.ID)
			return got.Description, err
		},
		"type/create": func(svc *category.Service, d string) (string, error) {
			c, _ := svc.CreateCategory(ctx, "C", 1)
			tp, err := svc.CreateTypeWithDescription(ctx, c.ID, "T", d, 1)
			if err != nil {
				return "", err
			}
			got, err := svc.GetType(ctx, tp.ID)
			return got.Description, err
		},
		"type/update": func(svc *category.Service, d string) (string, error) {
			c, _ := svc.CreateCategory(ctx, "C", 1)
			tp, _ := svc.CreateType(ctx, c.ID, "T", 1)
			tp.Description = d
			if err := svc.UpdateType(ctx, tp); err != nil {
				return "", err
			}
			got, err := svc.GetType(ctx, tp.ID)
			return got.Description, err
		},
		"item/create": func(svc *category.Service, d string) (string, error) {
			c, _ := svc.CreateCategory(ctx, "C", 1)
			tp, _ := svc.CreateType(ctx, c.ID, "T", 1)
			it, err := svc.CreateItemWithDescription(ctx, tp.ID, "I", d, 1)
			if err != nil {
				return "", err
			}
			got, err := svc.GetItem(ctx, it.ID)
			return got.Description, err
		},
		"item/update": func(svc *category.Service, d string) (string, error) {
			c, _ := svc.CreateCategory(ctx, "C", 1)
			tp, _ := svc.CreateType(ctx, c.ID, "T", 1)
			it, _ := svc.CreateItem(ctx, tp.ID, "I", 1)
			it.Description = d
			if err := svc.UpdateItem(ctx, it); err != nil {
				return "", err
			}
			got, err := svc.GetItem(ctx, it.ID)
			return got.Description, err
		},
	}

	for name, run := range ops {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				got, err := run(category.NewService(newFakeCategoryStore()), tc.in)
				if tc.wantErr {
					require.ErrorIs(t, err, category.ErrValidation)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			})
		}
	}
}

// The plain constructors still exist and make a node with no description, so
// nothing that creates one today (setup, the tests) changes.
func TestService_CreateWithoutDescriptionIsNone(t *testing.T) {
	ctx := context.Background()
	svc := category.NewService(newFakeCategoryStore())
	c, err := svc.CreateCategory(ctx, "C", 1)
	require.NoError(t, err)
	tp, err := svc.CreateType(ctx, c.ID, "T", 1)
	require.NoError(t, err)
	it, err := svc.CreateItem(ctx, tp.ID, "I", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"", "", ""}, []string{c.Description, tp.Description, it.Description})
}
