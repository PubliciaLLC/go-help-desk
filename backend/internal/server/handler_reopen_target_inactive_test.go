package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #419: the reopen target is a status picked by name. If it has been
// deactivated since, every way of reopening lands in "New" (the documented
// default) rather than in a retired status, and administrators are told.

func reporterOpenTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Laptop will not boot", Description: "black screen", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	return tk
}

func statusOfString(t *testing.T, h *harness, id string) string {
	t.Helper()
	return statusOf(t, h, uuid.MustParse(id))
}

const retiredTarget = "Awaiting parts"

// retiredReopenTarget makes a custom status, points the reopen setting at it
// and deactivates it. active=true leaves it active, as the control case.
func retiredReopenTarget(t *testing.T, h *harness, active bool) string {
	t.Helper()
	id := mkStatus(t, h, retiredTarget)
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyReopenTargetStatusName, retiredTarget))
	if !active {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+id, map[string]any{"active": false})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	}
	return id
}

func resolveAsAdmin(t *testing.T, h *harness, id string) {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+id+"/resolve", map[string]any{"notes": "done"})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestReopenTarget_InactiveFallsBackToNew_OnEveryPath(t *testing.T) {
	paths := []struct {
		name string
		// reopen runs the path against a fresh ticket and returns its id.
		reopen func(t *testing.T, h *harness) string
	}{
		{"forced reopen of a Closed ticket", func(t *testing.T, h *harness) string {
			setReopenPolicy(t, h, ticket.ReopenPolicyStaffAdmin)
			tk := reporterClosedTicket(t, h)
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/reopen", nil)
			require.Equal(t, http.StatusOK, res.StatusCode, readBody(t, res))
			res.Body.Close()
			return tk.ID.String()
		}},
		{"a requester's reply on a Resolved ticket", func(t *testing.T, h *harness) string {
			tk := reporterOpenTicket(t, h)
			resolveAsAdmin(t, h, tk.ID.String())
			res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "still broken"})
			require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
			res.Body.Close()
			return tk.ID.String()
		}},
		{"a guest's reply on a Resolved ticket", func(t *testing.T, h *harness) string {
			tk, _ := seedGuestTicket(t, h)
			resolveAsAdmin(t, h, tk.ID.String())
			// Resolving rotates the guest's link; this is the one they would
			// have been mailed.
			token, err := h.ticketSvc.IssueGuestToken(context.Background(), tk.ID)
			require.NoError(t, err)
			res := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "still broken"})
			require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
			res.Body.Close()
			return tk.ID.String()
		}},
	}
	for _, p := range paths {
		t.Run(p.name+"/inactive target reopens into New", func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			retiredReopenTarget(t, h, false)
			id := p.reopen(t, h)
			require.Equal(t, ticket.StatusNameNew, statusOfString(t, h, id))
		})
		t.Run(p.name+"/active target is still honoured", func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			retiredReopenTarget(t, h, true)
			id := p.reopen(t, h)
			require.Equal(t, retiredTarget, statusOfString(t, h, id))
		})
	}
}

func TestReopenTarget_SecurityWarningNamesAnInactiveTarget(t *testing.T) {
	get := func(t *testing.T, h *harness) map[string]any {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		m := respMap(t, res)
		rt, ok := m["reopen_target"].(map[string]any)
		require.True(t, ok, "reopen_target missing: %v", m)
		return rt
	}
	t.Run("inactive", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		retiredReopenTarget(t, h, false)
		rt := get(t, h)
		require.Equal(t, true, rt["inactive"])
		require.Equal(t, retiredTarget, rt["name"])
	})
	t.Run("active", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		retiredReopenTarget(t, h, true)
		require.Equal(t, false, get(t, h)["inactive"])
	})
	t.Run("default", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		require.Equal(t, false, get(t, h)["inactive"])
	})
}

func TestReopenTarget_DeactivatingTheTargetWarnsInTheResponse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	target := retiredReopenTarget(t, h, true)
	other := mkStatus(t, h, "Something else")

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+other, map[string]any{"active": false})
	require.Nil(t, respMap(t, res)["warning"], "deactivating a status that is not the target says nothing")

	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+target, map[string]any{"active": false})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Equal(t, false, got["active"], "the status itself still comes back at the top level")
	w, _ := got["warning"].(string)
	require.Contains(t, w, retiredTarget)
	require.Contains(t, w, "New", "it says where reopened tickets go meanwhile")
}

// The saved change is the answer. If the follow-up check for the warning
// fails, the status is still saved, so the caller gets it back (no 500 for a
// change that happened) and the failure goes to the log.
func TestReopenTarget_WarningCheckFailingDoesNotFailASavedStatus(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := retiredReopenTarget(t, h, true)
	h.srv.FailReopenTargetCheckForTest()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+id, map[string]any{"active": false})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Equal(t, false, got["active"], "the change was saved and is returned")
	require.NotContains(t, got, "warning", "no warning can be given when the check failed")
}

// One unreadable status list must not hide the other warnings.
func TestReopenTarget_WarningCheckFailingKeepsTheOtherSecurityWarnings(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.srv.FailReopenTargetCheckForTest()

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Contains(t, got, "insecure_secrets")
	require.Contains(t, got, "attachment_scanning")
	require.Contains(t, got, "attachment_storage")
	rt, ok := got["reopen_target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, rt["inactive"])
}

// An inactive target is one condition, and the log says it once, accurately:
// the status exists; it is retired.
func TestReopenTarget_InactiveIsLoggedOnceAndNotAsMissing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	retiredReopenTarget(t, h, false)
	setReopenPolicy(t, h, ticket.ReopenPolicyStaffAdmin)
	tk := reporterClosedTicket(t, h)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/reopen", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "reopen target"), "logged once:\n%s", out)
	require.Contains(t, out, "inactive")
	require.NotContains(t, out, "does not exist")
}
