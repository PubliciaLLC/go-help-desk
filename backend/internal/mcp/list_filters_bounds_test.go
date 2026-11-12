package mcp

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A list argument is bounded before anything is done with it. mcp-go does not
// enforce the schema's maxItems and the body limit lets ~100,000 short strings
// through, so any work per value that grows with what came before it (a
// de-duplication scan, say) costs seconds of CPU per call, for any caller.
func TestBuildListFilter_LongListsAreRefusedBeforeAnyWork(t *testing.T) {
	huge := make([]any, 100_000)
	for i := range huge {
		huge[i] = uuid.NewString()
	}

	for _, key := range []string{"status_ids", "status_id", "source_channel", "priority"} {
		t.Run(key, func(t *testing.T) {
			start := time.Now()
			_, err := buildListFilter(map[string]any{key: huge}, ticket.VisibilityAll, uuid.New())
			elapsed := time.Since(start)

			require.ErrorIs(t, err, ticket.ErrValidation)
			require.Contains(t, err.Error(), "too many "+key+" values (max 20)")
			require.Less(t, elapsed, 50*time.Millisecond, "refusing 100,000 values must not cost more than reading them")

			_, err = buildListFilter(map[string]any{key: huge[:21]}, ticket.VisibilityAll, uuid.New())
			require.ErrorIs(t, err, ticket.ErrValidation, "21 is one too many")
			require.Contains(t, err.Error(), "too many "+key+" values (max 20)")
		})
	}

	t.Run("20 is allowed", func(t *testing.T) {
		_, err := buildListFilter(map[string]any{"status_ids": huge[:20]}, ticket.VisibilityAll, uuid.New())
		require.NoError(t, err)
	})
	t.Run("status_id and status_ids together may not exceed 20 either", func(t *testing.T) {
		_, err := buildListFilter(map[string]any{"status_id": uuid.NewString(), "status_ids": huge[:20]}, ticket.VisibilityAll, uuid.New())
		require.ErrorIs(t, err, ticket.ErrValidation)
		require.Contains(t, err.Error(), "too many status_id values (max 20)")
	})
}

// Values reach the parser as values. A character that means something in a
// query string means nothing here, so no argument can add, replace or drop
// another one.
func TestBuildListFilter_ValuesStayLiteral(t *testing.T) {
	cases := []struct {
		name  string
		args  map[string]any
		check func(t *testing.T, f ticket.Filter)
	}{
		{"an ampersand does not start another parameter", map[string]any{"requester": "a&scope=all"},
			func(t *testing.T, f ticket.Filter) { require.Equal(t, "a&scope=all", f.Requester) }},
		{"a q cannot smuggle a requester", map[string]any{"q": "x&requester=whitfield"},
			func(t *testing.T, f ticket.Filter) {
				require.Empty(t, f.Requester)
				require.Equal(t, "x&requester=whitfield", f.Query)
			}},
		{"a q cannot smuggle a reporter", map[string]any{"q": "x&reporter_id=" + uuid.NewString()},
			func(t *testing.T, f ticket.Filter) { require.Nil(t, f.ReporterID) }},
		{"a percent escape is not decoded", map[string]any{"q": "100%26requester=whitfield"},
			func(t *testing.T, f ticket.Filter) {
				require.Empty(t, f.Requester)
				require.Equal(t, "100%26requester=whitfield", f.Query)
			}},
		{"a plus is not a space", map[string]any{"q": "a+b"},
			func(t *testing.T, f ticket.Filter) { require.Equal(t, "a+b", f.Query) }},
		{"an equals sign is part of the value", map[string]any{"q": "a=b"},
			func(t *testing.T, f ticket.Filter) { require.Equal(t, "a=b", f.Query) }},
		{"include_closed cannot be switched by a value", map[string]any{"q": "x&include_closed=false"},
			func(t *testing.T, f ticket.Filter) { require.False(t, f.ExcludeClosed) }},
		{"sort cannot be switched by a value", map[string]any{"q": "x&sort=updated"},
			func(t *testing.T, f ticket.Filter) { require.Empty(t, f.Sort) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := buildListFilter(tc.args, ticket.VisibilityAll, uuid.New())
			require.NoError(t, err)
			tc.check(t, f)
		})
	}

	// In a list, one element is one value, whatever it contains.
	for _, args := range []map[string]any{
		{"source_channel": []any{"web&source_channel=api"}},
		{"priority": []any{"high&priority=low"}},
		{"status_ids": []any{uuid.NewString() + "&status_id=" + uuid.NewString()}},
	} {
		_, err := buildListFilter(args, ticket.VisibilityAll, uuid.New())
		require.ErrorIs(t, err, ticket.ErrValidation, "%v", args)
	}
}

func TestBuildListFilter_NullLimitAndOffsetAreAbsent(t *testing.T) {
	f, err := buildListFilter(map[string]any{"limit": nil, "offset": nil}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, 20, f.Limit)
	require.Equal(t, 0, f.Offset)
}
