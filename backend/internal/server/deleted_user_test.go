package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Deleting a staff member puts their open tickets back in the queue.
//
// The assignee column kept pointing at the soft-deleted row, which no longer
// appears in the user list. So the ticket rendered as "Unassigned" on the
// page — the lookup found nobody — was NOT in the unassigned queue, because
// the column was not null, and was in nobody's "assigned to me". It sat in
// the gap between the two lists with nothing to prompt anyone to pick it up.
//
// Disabling an account does not have this problem, because a disabled user is
// still in the list; deleting one is where the ticket disappears.
func TestDeleteUser_ReturnsTheirOpenTicketsToTheQueue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	leaver, err := h.userSvc.Create(ctx, userInput("leaver@test.local", "A Leaver"))
	require.NoError(t, err)

	open, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Still open", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Assign(ctx, open.ID, &leaver.ID, nil, ticket.SystemActor)
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/users/"+leaver.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	after, err := h.ticketSvc.GetByID(ctx, open.ID)
	require.NoError(t, err)
	require.Nil(t, after.AssigneeUserID,
		"the ticket is still assigned to somebody who is not in the user list, "+
			"so it shows as unassigned and is missing from the unassigned queue")

	// And it is genuinely back in the queue staff work from.
	list := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=unassigned", nil)
	defer list.Body.Close()
	require.Equal(t, http.StatusOK, list.StatusCode)
	var tickets []ticket.Ticket
	require.NoError(t, json.NewDecoder(list.Body).Decode(&tickets))
	var found bool
	for _, tk := range tickets {
		if tk.ID == open.ID {
			found = true
		}
	}
	require.True(t, found, "the ticket is not in the unassigned queue")
}

// An address belonging to another account is a conflict, not a server fault.
//
// The unique constraint came back as an unmapped error, so an administrator
// typing an address that already exists was told "an internal error
// occurred". The message now says what is actually wrong — and mentions
// deleted accounts, because the row stays and keeps its address, which is the
// case nobody expects.
func TestCreateUser_ATakenAddressIsAConflict(t *testing.T) {
	// A harness each, because a refused insert aborts the transaction this
	// suite runs every test inside — the second attempt would fail on the
	// poisoned transaction rather than on the constraint, and pass for
	// entirely the wrong reason.
	t.Run("a live account", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		_, err := h.userSvc.Create(context.Background(), userInput("taken@test.local", "Already Here"))
		require.NoError(t, err)

		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "taken@test.local", "display_name": "Someone Else",
			"role": "user", "password": "a-real-passphrase",
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode)
	})

	t.Run("a deleted one, whose row keeps the address", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()

		existing, err := h.userSvc.Create(ctx, userInput("taken@test.local", "Already Here"))
		require.NoError(t, err)
		require.NoError(t, h.userSvc.SoftDelete(ctx, existing.ID))

		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "taken@test.local", "display_name": "The Returner",
			"role": "user", "password": "a-real-passphrase",
		})
		body, _ := readAllBody(res)
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode,
			"re-hiring somebody should say the address is taken, not report a server fault: %s", body)
		require.Contains(t, body, "deleted",
			"the message should mention the case nobody expects")
	})
}

// An address in angle brackets is stored as the bare address.
//
// mail.ParseAddress accepts "<someone@example.com>", and the parsed answer was
// thrown away — so the brackets were stored verbatim. Login looks up the bare
// address, so the account could only be signed into by typing the brackets,
// which nobody does.
func TestCreateUser_StoresTheBareAddress(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "<bracketed@test.local>", "display_name": "Bracketed",
		"role": "user", "password": "a-real-passphrase",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, "bracketed@test.local", created.Email,
		"the brackets were stored, so this account cannot be logged into")
}

// userInput is a minimal, valid new account.
func userInput(email, name string) user.CreateUserInput {
	return user.CreateUserInput{
		Email: email, DisplayName: name,
		Role: user.RoleStaff, Password: "a-real-passphrase",
	}
}
