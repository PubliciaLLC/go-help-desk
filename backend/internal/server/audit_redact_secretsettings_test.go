package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// audit.Redact claims to cover the class of value secretSettingKeys protects:
// a write-only secret must never render in a browser, on the settings page or
// in an audit diff. A stem list that misses one of these names (it once missed
// attachment_reputation_*_key) is a guard with a hole in exactly the place it
// exists for, so the two lists are tied together here and cannot drift.
func TestRedact_CoversEverySecretSettingKey(t *testing.T) {
	require.NotEmpty(t, secretSettingKeys)
	for k := range secretSettingKeys {
		t.Run(k, func(t *testing.T) {
			before, after := audit.Redact(map[string]any{k: "sk-live-value"}, map[string]any{k: "sk-live-other"})
			require.Equal(t, "[redacted]", before[k], "%q is write-only over the API but would render in an audit diff", k)
			require.Equal(t, "[redacted]", after[k])
		})
	}
}
