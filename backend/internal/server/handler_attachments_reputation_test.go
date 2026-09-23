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

// The link that lets a person look an attachment's hash up is built by the
// server, not the browser.
//
// Two reasons, and both are the reason this is tested rather than assumed. The
// provider is a session-gated admin setting, so staff cannot read it — a
// frontend that needed to know it would need a way to be told, and that is a
// setting leak waiting to happen. And a frontend that assembled the URL itself
// would carry each provider's format in TypeScript, a copy that drifts from
// the Go one the moment either changes.
func TestAttachments_CarryALookupLinkBuiltFromTheConfiguredProvider(t *testing.T) {
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

	_ = attachmentOverHTTP(t, h, tk.ID.String(), "report.pdf")

	cases := []struct {
		name     string
		setting  string
		wantHost string
	}{
		{
			name:     "the shipped default",
			setting:  "",
			wantHost: "https://www.virustotal.com/gui/file/",
		},
		{
			name:     "an operator who chose the other one",
			setting:  "metadefender",
			wantHost: "https://metadefender.com/results/hash/",
		},
		{
			// A typo must land somewhere predictable. The shipped default is
			// the safe landing: no link at all would look like the feature is
			// broken rather than like the setting is.
			name:     "a value nobody recognises",
			setting:  "notaprovider",
			wantHost: "https://www.virustotal.com/gui/file/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setting == "" {
				require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentReputationProvider, []byte(`null`)))
			} else {
				require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentReputationProvider,
					[]byte(`"`+tc.setting+`"`)))
			}

			got := attachmentOverHTTP(t, h, tk.ID.String(), "report.pdf")
			require.NotNil(t, got.ReputationURL,
				"an attachment with a hash always has somewhere to look it up; the link needs no API key")
			require.Equal(t, tc.wantHost+*got.SHA256, *got.ReputationURL)
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
