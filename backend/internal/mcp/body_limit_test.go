package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The limit is enforced in Handler(), so it holds for any caller that mounts
// it, not only behind ProtectMCP (#481). The end-to-end cases, with the real
// auth chain and both transports, are in internal/server.
func TestHandler_OversizeBodyIs413WithJSONEnvelope(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	for _, path := range []string{"/mcp", "/mcp/"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("a", MaxRequestBody+1)))
			req.Header.Set("Content-Type", "application/json")
			s.Handler().ServeHTTP(rec, req)

			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			var env struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			require.Equal(t, "request_too_large", env.Error.Code)
		})
	}
}

func TestHandler_BodyAtTheLimitIsStillProcessed(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	pad := MaxRequestBody - len(strings.ReplaceAll(initializeBody, `"name":"t"`, `"name":""`))
	body := strings.ReplaceAll(initializeBody, `"name":"t"`, `"name":"`+strings.Repeat("a", pad)+`"`)
	require.Len(t, body, MaxRequestBody)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// failingBody fails the test if anything reads it.
type failingBody struct{ t *testing.T }

func (b failingBody) Read([]byte) (int, error) {
	b.t.Helper()
	b.t.Error("the body was read although Content-Length already exceeds the limit")
	return 0, io.EOF
}
func (failingBody) Close() error { return nil }

// A declared length over the limit is refused without reading a byte. Without
// this check the body is still capped by the read, so only this test notices it
// going missing.
func TestHandler_DeclaredLengthOverLimitIsRefusedUnread(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = failingBody{t}
	req.ContentLength = MaxRequestBody + 1
	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Contains(t, rec.Body.String(), `"request_too_large"`)
}

// With Expect: 100-continue the client waits for the server before sending the
// body. The first response must be the 413, not "100 Continue".
func TestHandler_Expect100ContinueGets413NotContinue(t *testing.T) {
	ts := httptest.NewServer(New(nil, nil, nil, nil, nil).Handler())
	t.Cleanup(ts.Close)
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = fmt.Fprintf(conn, "POST /mcp HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", MaxRequestBody+1)
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, "413", line)
}
