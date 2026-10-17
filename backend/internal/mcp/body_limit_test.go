package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The limit is enforced in Handler(), so it holds for any caller that mounts
// it, not only behind ProtectMCP (#481). The end-to-end cases, with the real
// auth chain and both transports, are in internal/server.
func TestHandler_OversizeBodyIs413WithJSONEnvelope(t *testing.T) {
	s := New(nil, nil, nil, nil)
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
	s := New(nil, nil, nil, nil)
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
