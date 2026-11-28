package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Writes that announced nothing (#630), through the real routes, services and
// store. h.dispatcher.record keeps what a request queued, before any worker.

func g4Queued(h *harness, kind notification.EventType) []notification.Event {
	var out []notification.Event
	for _, ev := range h.dispatcher.queued {
		if ev.Type == kind {
			out = append(out, ev)
		}
	}
	return out
}

func g4Ticket(t *testing.T, h *harness, subject string) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: subject, Description: "d", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	return tk
}

func TestRemoveLink_AnnouncesUnlinkedOnceAndAnUnknownLinkNot(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	a, b := g4Ticket(t, h, "A"), g4Ticket(t, h, "B")
	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+a.ID.String()+"/links",
		map[string]any{"target_id": b.ID, "link_type": "related_to"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	h.dispatcher.record = true
	h.dispatcher.queued = nil

	path := "/api/v1/tickets/" + a.ID.String() + "/links/" + b.ID.String() + "/related_to"
	res = h.do(t, http.MethodDelete, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Len(t, h.dispatcher.queued, 1)
	ev := h.dispatcher.queued[0]
	require.Equal(t, notification.EventTicketUnlinked, ev.Type)
	require.Equal(t, a.ID, ev.TicketID)
	require.Equal(t, &h.staffID, ev.ActorID)
	require.Equal(t, b.ID, ev.Payload["target_id"])
	require.Equal(t, ticket.LinkRelatedTo, ev.Payload["link_type"])
	require.NotNil(t, ev.Ticket)
	links, err := h.ticketSvc.ListLinks(context.Background(), a.ID)
	require.NoError(t, err)
	require.Empty(t, links)

	res = h.do(t, http.MethodDelete, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode, "still a success, as before")
	require.Len(t, h.dispatcher.queued, 1, "nothing was removed, so nothing is announced")
}

func TestFollow_RoutesAnnounceFollowedAndUnfollowedWithTheCallersID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := g4Ticket(t, h, "Followed")
	h.dispatcher.record = true

	res := h.doAsAdminSession(t, http.MethodPut, "/api/v1/tickets/"+tk.ID.String()+"/follow", nil)
	res.Body.Close()
	require.Less(t, res.StatusCode, 300)
	evs := g4Queued(h, notification.EventTicketFollowed)
	require.Len(t, evs, 1)
	require.Equal(t, h.adminID, evs[0].Payload["user_id"])
	require.Equal(t, &h.adminID, evs[0].ActorID)
	require.False(t, evs[0].MailsStaff())

	res = h.doAsAdminSession(t, http.MethodDelete, "/api/v1/tickets/"+tk.ID.String()+"/follow", nil)
	res.Body.Close()
	require.Less(t, res.StatusCode, 300)
	evs = g4Queued(h, notification.EventTicketUnfollowed)
	require.Len(t, evs, 1)
	require.Equal(t, h.adminID, evs[0].Payload["user_id"])
}

func TestUpload_AnnouncesAttachmentAdded(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeCleanScanner(t))
	defer cleanup()
	id := ticketForUpload(t, h)
	h.dispatcher.record = true

	res := uploadAttachment(t, h, id, "notes.txt", []byte("harmless"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var att ticket.Attachment
	require.NoError(t, json.NewDecoder(res.Body).Decode(&att))

	evs := g4Queued(h, notification.EventTicketAttachmentAdded)
	require.Len(t, evs, 1)
	require.Equal(t, id, evs[0].TicketID.String())
	require.Equal(t, att.ID, evs[0].Payload["attachment_id"])
	require.Equal(t, &h.staffID, evs[0].ActorID)
	require.Empty(t, evs[0].Recipient, "D39: no mail for an upload")
}

// A group delete announces each ticket to webhooks...
func TestDeleteGroup_AnnouncesEachTicketItHeld(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	g := mkGroup(t, h, "Desk")
	tk := g4Ticket(t, h, "Held")
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	h.dispatcher.record = true
	h.dispatcher.queued = nil

	res = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/groups/"+g.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Len(t, h.dispatcher.queued, 1)
	ev := h.dispatcher.queued[0]
	require.Equal(t, notification.EventTicketAssigned, ev.Type)
	require.Equal(t, tk.ID, ev.TicketID)
	require.Equal(t, &h.adminID, ev.ActorID)
	require.True(t, ev.SuppressStaffMail)
	require.Nil(t, ev.Ticket.AssigneeGroupID)
}

// ...and mails nobody: the people who follow the tickets would otherwise each
// get an "assigned" mail per ticket.
func TestDeleteGroup_MailsNobody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	g := mkGroup(t, h, "Desk")
	tk := newStaffTicket(t, h)
	// The staff member follows the ticket, so an assignment mails them: once the
	// group is gone the ticket has no group, and the follower is the one person
	// a mail for the cleared assignment could still reach.
	require.NoError(t, h.ticketSvc.Follow(ctx, tk.ID, ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}))
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t), "the assignment itself mails the follower: the control")

	res = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/groups/"+g.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Empty(t, m.recipients(t), "deleting the group mails nobody")
	require.Empty(t, m.sent.got)
}

func TestDeleteUser_AnnouncesAndMailsNobody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	// A second staff account to delete: the harness's own staff is the API key's owner.
	leaving, err := h.userSvc.Create(ctx, userInput("leaving@test.local", "Leaving"))
	require.NoError(t, err)
	tk := newStaffTicket(t, h)
	g := mkGroup(t, h, "Desk")
	require.NoError(t, h.groupSvc.AddMember(ctx, g, h.staffID))
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
		map[string]any{"assignee_user_id": leaving.ID, "assignee_group_id": g})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	m.recipients(t) // forget the assignment's own mail

	res = h.doAsAdminSession(t, http.MethodDelete, "/api/v1/admin/users/"+leaving.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Empty(t, m.recipients(t), "the group member and the followers are not mailed")
	require.Empty(t, m.sent.got)
	got, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Nil(t, got.AssigneeUserID)
	require.Equal(t, uuid.UUID(g), *got.AssigneeGroupID, "the group stays; only the person went")
}

// The history lists a follow event under its own name, and a replay of it is
// held to the opt-in rule: a hook on "*" is refused (409), one that names it is
// queued. Through the real history store and routes.
func TestWebhookRedeliver_FollowEventsNeedTheNameToBeReplayed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := g4Ticket(t, h, "Followed")
	hook := historyHook(t, h, true) // events: "*"
	id := uuid.New()
	require.NoError(t, h.authStore.RecordWebhookAttempt(context.Background(), authstore.WebhookAttempt{
		DeliveryID: id, HookID: hook.ID, EventType: "ticket.followed",
		Event:  []byte(`{"type":"ticket.followed","ticket_id":"` + tk.ID.String() + `","payload":{"user_id":"` + h.adminID.String() + `"}}`),
		Result: authstore.WebhookDelivery{At: time.Now().UTC(), Status: 200},
	}))
	path := "/api/v1/admin/webhooks/" + hook.ID.String() + "/deliveries"

	res := h.doAsAdmin(t, http.MethodGet, path, nil)
	defer res.Body.Close()
	var list []map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Equal(t, "ticket.followed", list[0]["event"])

	before := queuedWebhookRows(t, h)
	res = h.doAsAdmin(t, http.MethodPost, path+"/"+id.String()+"/redeliver", nil)
	res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode, "\"*\" does not cover a follow event")
	require.Equal(t, before, queuedWebhookRows(t, h))

	hook.Events = []string{"ticket.followed"}
	require.NoError(t, h.authStore.UpdateWebhook(context.Background(), hook))
	res = h.doAsAdmin(t, http.MethodPost, path+"/"+id.String()+"/redeliver", nil)
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, before+1, queuedWebhookRows(t, h))
	var stored string
	require.NoError(t, h.tx.QueryRow(
		`SELECT event::text FROM notification_outbox WHERE channel = 'webhook' ORDER BY created_at DESC LIMIT 1`).Scan(&stored))
	require.Contains(t, stored, h.adminID.String(), "the person's id survives into the replay")
}

// "All events" plus a follow event by name is a subscription the API accepts and
// stores as given: the hook gets everything, and the follow events too.
func TestWebhookCreate_AcceptsTheNewNamesAlongsideAllEvents(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"url": "https://example.com/hook", "events": []string{"*", "ticket.followed", "ticket.unfollowed"}})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created struct {
		Events []string `json:"events"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, []string{"*", "ticket.followed", "ticket.unfollowed"}, created.Events)
}
