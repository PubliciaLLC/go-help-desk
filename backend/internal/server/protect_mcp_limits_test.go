package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// #481 (body size) and #495 (SSE sessions bound to their actor), over the real
// transports behind the real ProtectMCP chain.

// repeatReader yields n bytes of 'a' without ever holding them, so the test's
// own memory does not drown out the server's.
type repeatReader struct{ n int64 }

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		p[i] = 'a'
	}
	r.n -= int64(len(p))
	return len(p), nil
}

// rpcOfSize is a valid initialize request padded to exactly size bytes, so
// "just under the limit" is a request the server must process, not merely
// one it must not refuse.
func rpcOfSize(t *testing.T, size int) []byte {
	t.Helper()
	mk := func(pad int) []byte {
		b, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
				"clientInfo": map[string]any{"name": strings.Repeat("a", pad), "version": "1"},
			},
		})
		require.NoError(t, err)
		return b
	}
	base := len(mk(0))
	require.Less(t, base, size)
	b := mk(size - base)
	require.Len(t, b, size)
	return b
}

func postRaw(t *testing.T, url, token string, body io.Reader, contentLength int64) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	require.NoError(t, err)
	req.ContentLength = contentLength // -1 = chunked
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "ApiKey "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func requireTooLarge(t *testing.T, resp *http.Response) {
	t.Helper()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, string(raw))
	var env struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	require.Equal(t, "request_too_large", env.Error.Code, string(raw))
	require.NotEmpty(t, env.Error.Message)
}

func TestMCPBodyLimit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	limit := mcp.MaxRequestBody

	transports := []struct {
		name string
		url  func(t *testing.T) string
		ok   int // status for a request that is processed
	}{
		{"streamable", func(t *testing.T) string { return serveMCP(t, h) + "/mcp" }, http.StatusOK},
		{"sse", func(t *testing.T) string {
			c := openMCP(t, h, h.apiKey)
			// Registered after openMCP's ts.Close, so it runs first: Close
			// blocks while the stream is open.
			t.Cleanup(c.closeBody)
			return c.messageURL
		}, http.StatusAccepted},
	}
	for _, tr := range transports {
		t.Run(tr.name+" just under the limit is processed", func(t *testing.T) {
			body := rpcOfSize(t, limit)
			resp := postRaw(t, tr.url(t), h.apiKey, bytes.NewReader(body), int64(len(body)))
			require.Equal(t, tr.ok, resp.StatusCode)
		})
		t.Run(tr.name+" one byte over is refused, declared length", func(t *testing.T) {
			body := rpcOfSize(t, limit+1)
			requireTooLarge(t, postRaw(t, tr.url(t), h.apiKey, bytes.NewReader(body), int64(len(body))))
		})
		t.Run(tr.name+" one byte over is refused, chunked", func(t *testing.T) {
			body := rpcOfSize(t, limit+1)
			requireTooLarge(t, postRaw(t, tr.url(t), h.apiKey, bytes.NewReader(body), -1))
		})
		for _, declared := range []bool{true, false} {
			name := tr.name + " 64 MiB is refused without being read into memory"
			if declared {
				name += ", declared length"
			} else {
				name += ", chunked"
			}
			t.Run(name, func(t *testing.T) {
				const huge = 64 << 20
				url := tr.url(t)
				cl := int64(-1)
				if declared {
					cl = huge
				}
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				resp := postRaw(t, url, h.apiKey, &repeatReader{n: huge}, cl)
				runtime.ReadMemStats(&after)
				requireTooLarge(t, resp)
				// 64 MiB read into a buffer is 64 MiB or more (#481 measured
				// 340 to 712 MB). Allow generous slack for the rest of the
				// process, but far below the body.
				require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(16<<20),
					"allocated while refusing a 64 MiB body")
			})
		}
	}
}

func TestMCPSSE_SessionBoundToActor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	owner := openMCP(t, h, h.apiKey)
	t.Cleanup(owner.closeBody)
	ping := func(t *testing.T, url, token string) *http.Response {
		t.Helper()
		body := []byte(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
		return postRaw(t, url, token, bytes.NewReader(body), int64(len(body)))
	}
	refusedBody := func(t *testing.T, resp *http.Response) string {
		t.Helper()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
		return string(raw)
	}

	t.Run("the actor who opened the stream is accepted", func(t *testing.T) {
		require.Equal(t, http.StatusAccepted, ping(t, owner.messageURL, h.apiKey).StatusCode)
	})

	var mismatched string
	t.Run("a different actor is refused", func(t *testing.T) {
		for name, tok := range map[string]string{"admin": h.adminKey, "reporting user": h.userKey} {
			mismatched = refusedBody(t, ping(t, owner.messageURL, tok))
			require.NotEmpty(t, mismatched, name)
		}
	})
	t.Run("a different actor's message is not processed", func(t *testing.T) {
		// The owner's own ping (first subtest) is answered on the stream. A
		// refused ping from someone else must add no second reply.
		replies := make(chan string, 10)
		go func() {
			for {
				line, err := owner.events.ReadString('\n')
				if err != nil {
					return
				}
				if strings.Contains(line, `"id":7`) {
					replies <- line
				}
			}
		}()
		select {
		case <-replies:
		case <-time.After(5 * time.Second):
			t.Fatal("the owner's own ping was not answered")
		}
		_ = refusedBody(t, ping(t, owner.messageURL, h.adminKey))
		select {
		case line := <-replies:
			t.Fatalf("a refused message was processed: %q", line)
		case <-time.After(500 * time.Millisecond):
		}
	})
	t.Run("an unauthenticated caller is refused", func(t *testing.T) {
		resp := ping(t, owner.messageURL, "")
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
	t.Run("an unknown sessionId is refused, with the same body as a mismatch", func(t *testing.T) {
		unknown := strings.Split(owner.messageURL, "sessionId=")[0] + "sessionId=00000000-0000-0000-0000-000000000000"
		require.Equal(t, mismatched, refusedBody(t, ping(t, unknown, h.apiKey)))
	})
	t.Run("a missing sessionId is refused", func(t *testing.T) {
		noID := strings.Split(owner.messageURL, "?")[0]
		_ = refusedBody(t, ping(t, noID, h.apiKey))
	})
	t.Run("a closed stream's sessionId is refused", func(t *testing.T) {
		owner.closeBody()
		require.Eventually(t, func() bool {
			return ping(t, owner.messageURL, h.apiKey).StatusCode == http.StatusForbidden
		}, 5*time.Second, 50*time.Millisecond)
	})
}
