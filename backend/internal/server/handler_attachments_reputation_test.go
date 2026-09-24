package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Every attachment with a hash carries a VirusTotal link, whatever the
// per-provider toggles say.
//
// A link is not a lookup. A lookup is this server sending a customer's file
// hash to a third party — the operator's decision, and what the toggles
// govern. A link sends nothing from here: it is an anchor the analyst clicks
// in their own browser, exactly as if they had copied the hash off the page,
// which they can do anyway.
//
// Built by the server rather than the browser because a frontend that
// assembled the URL itself would carry the format in TypeScript, a copy that
// drifts from the Go one the moment either changes.
func TestAttachments_CarryAHashLinkWhateverIsEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Reputation", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	res := uploadNamed(t, h, tk.ID.String(), "report.pdf", pdf)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	const want = "https://www.virustotal.com/gui/file/"

	cases := []struct {
		name    string
		enabled []string
	}{
		{name: "nothing enabled, which is every fresh instance"},
		{name: "VirusTotal enabled", enabled: []string{"virustotal"}},
		{
			// The case that decided this. An operator who switched VirusTotal
			// off did so to stop their SERVER sending customers' hashes
			// there; it is not a rule about where their staff may read.
			name:    "VirusTotal deliberately off, CIRCL on",
			enabled: []string{"circl"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			on := map[string]bool{}
			for _, p := range tc.enabled {
				on[p] = true
			}
			for _, p := range admin.ReputationProviders() {
				enabledKey, _, ok := admin.ReputationSettingKeys(p)
				require.True(t, ok)
				raw := []byte("false")
				if on[p] {
					raw = []byte("true")
				}
				require.NoError(t, h.adminSvc.SetRaw(ctx, enabledKey, raw))
			}

			got := attachmentOverHTTP(t, h, tk.ID.String(), "report.pdf")
			require.NotNil(t, got.ReputationURL,
				"the hash link needs no key, no toggle and no server call")
			require.Equal(t, want+*got.SHA256, *got.ReputationURL)
		})
	}
}

// An attachment nobody inspected has no hash, so there is nothing to look up
// and no link to give. nil rather than a URL ending in an empty hash, which
// would 404 for whoever clicked it.
func TestAttachments_WithoutAHashCarryNoLookupLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "No hash", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Written straight to the store, which is how every row that predates the
	// detection columns looks.
	require.NoError(t, h.ticketStore.CreateAttachment(ctx, ticket.Attachment{
		ID: uuid.New(), TicketID: tk.ID, Filename: "old.pdf",
		MimeType: "application/pdf", SizeBytes: 10, StoragePath: "/dev/null",
	}))

	got := attachmentOverHTTP(t, h, tk.ID.String(), "old.pdf")
	require.Nil(t, got.SHA256)
	require.Nil(t, got.ReputationURL)
}

// attachmentOverHTTP reads an attachment back through the API rather than the
// store.
//
// The distinction is the point of these tests. ReputationURL is not a column;
// it is filled in at the HTTP boundary from a setting, so a helper that reads
// the domain object sees nil however correct the handler is. The first version
// of this test did exactly that and failed against working code.
func attachmentOverHTTP(t *testing.T, h *harness, ticketID, filename string) ticket.Attachment {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var list []ticket.Attachment
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	for _, a := range list {
		if a.Filename == filename {
			return a
		}
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Filename)
	}
	t.Fatalf("no attachment named %q over HTTP; got %v", filename, names)
	return ticket.Attachment{}
}
