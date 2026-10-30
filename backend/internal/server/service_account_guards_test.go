package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Two guards that landed with the service-user work and are not reachable by
// the ordinary routes, so each is driven directly (#420).

// round-robin and the default group are for people: assignment refuses a
// service user, so saving its id would make the rotation skip it forever.
func TestServiceAccount_RefusedInAutoAssignUserIDs(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	// Something valid is saved first, so "writes nothing" means something.
	status, body := patchSettings(t, h, map[string]any{"auto_assign_user_ids": []string{h.staffID.String()}})
	require.Equal(t, http.StatusNoContent, status, body)

	res := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"auto_assign_user_ids": []string{h.staffID.String(), c.UserID.String()}})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "invalid_auto_assign", errorCode(t, res))

	require.Equal(t, []uuid.UUID{h.staffID}, h.adminSvc.AutoAssignUserIDs(ctx),
		"a refused save writes nothing")
}

// fixedSessions is a session store that hands back one session as if it had
// loaded. The real store refuses a service user's session, which is the point
// of it; this stands in for "the session layer let one through" so the
// handlers' own guard is what is under test.
type fixedSessions struct{ data auth.SessionData }

func (f fixedSessions) Get(_ *http.Request, name string) (*sessions.Session, error) {
	s := sessions.NewSession(f, name)
	s.IsNew = false
	s.Values[auth.SessionDataKey] = f.data
	return s, nil
}
func (f fixedSessions) New(_ *http.Request, name string) (*sessions.Session, error) {
	return sessions.NewSession(f, name), nil
}
func (f fixedSessions) Save(*http.Request, http.ResponseWriter, *sessions.Session) error {
	return nil
}

func TestServiceAccount_PasskeyCeremoniesRefuseIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	// FactorVerified, so registration is not stopped earlier by the factor
	// check: it is accountFor that has to answer.
	store := fixedSessions{data: auth.SessionData{
		UserID: c.UserID, Role: user.RoleStaff, MFAPassed: true, FactorVerified: true,
	}}
	for name, handler := range h.srv.PasskeyStartHandlersForTest() {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			authmw.SessionAuth(store)(handler).ServeHTTP(rec, req)

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "service_account")
		})
	}

	// The control: the same harness and handlers serve an ordinary account, so a
	// 409 above is the service user and not the setup.
	ordinary := fixedSessions{data: auth.SessionData{
		UserID: h.staffID, Role: user.RoleStaff, MFAPassed: true, FactorVerified: true,
	}}
	rec := httptest.NewRecorder()
	authmw.SessionAuth(ordinary)(h.srv.PasskeyStartHandlersForTest()["login start"]).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	require.NotEqual(t, http.StatusConflict, rec.Code, rec.Body.String())
}
