package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// staleOffFields reads the two #459 fields off the security-warnings response.
func staleOffFields(t *testing.T, h *harness) (explicit, stale bool) {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var body struct {
		AttachmentScanning struct {
			PolicyExplicit bool `json:"policy_explicit"`
			StaleOff       bool `json:"stale_off"`
		} `json:"attachment_scanning"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.AttachmentScanning.PolicyExplicit, body.AttachmentScanning.StaleOff
}

// #459: scanning that is off because nobody ever chose it must be called out,
// while a deliberate "Do not scan" must not be. Every combination of the four
// inputs is listed so the rule cannot drift to a looser one unnoticed.
func TestSecurityWarnings_StaleOff(t *testing.T) {
	const addr = "tcp://127.0.0.1:1"
	tests := []struct {
		name         string
		policy       string // "" = never saved
		attachment   bool
		envAddr      string
		settingAddr  string
		wantExplicit bool
		wantStaleOff bool
	}{
		{"nothing set, no attachments", "", false, "", "", false, false},
		{"nothing set, attachment exists", "", true, "", "", false, true},
		{"explicit off, attachment exists", "off", true, "", "", true, false},
		{"explicit off, no attachments", "off", false, "", "", true, false},
		{"explicit required, attachment exists", "required", true, "", "", true, false},
		{"explicit permissive, attachment exists", "permissive", true, "", "", true, false},
		{"address in env, attachment exists", "", true, addr, "", false, false},
		{"address in setting, attachment exists", "", true, "", addr, false, false},
		{"explicit off with env address, attachment exists", "off", true, addr, "", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarnessWith(t, 0, tc.envAddr)
			defer cleanup()
			ctx := context.Background()

			if tc.policy != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, tc.policy))
			}
			if tc.settingAddr != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanAddress, tc.settingAddr))
			}
			if tc.attachment {
				tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
					Subject: "Attachment", Description: "has one", CategoryID: h.catID,
					ReporterUserID: &h.staffID,
				})
				require.NoError(t, err)
				require.NoError(t, h.ticketStore.CreateAttachment(ctx, ticket.Attachment{
					ID: uuid.New(), TicketID: tk.ID, Filename: "old.pdf",
					MimeType: "application/pdf", SizeBytes: 10, StoragePath: "/dev/null",
				}))
			}

			explicit, stale := staleOffFields(t, h)
			require.Equal(t, tc.wantExplicit, explicit, "policy_explicit")
			require.Equal(t, tc.wantStaleOff, stale, "stale_off")
		})
	}
}

// A stored value that is not a valid policy is not a choice anybody made.
func TestSecurityWarnings_InvalidStoredPolicyIsNotExplicit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyAttachmentScanPolicy, []byte(`"bogus"`)))

	explicit, _ := staleOffFields(t, h)
	require.False(t, explicit)
}
