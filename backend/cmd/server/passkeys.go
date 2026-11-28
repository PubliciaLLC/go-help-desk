package main

import (
	"log/slog"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
)

// configurePasskeys builds the passkey service, or reports why it cannot.
//
// A BASE_URL that cannot be a WebAuthn relying-party id (an IP address, a
// single-label host, a non-ASCII host) turns passkeys off; it does not stop
// the server (#494). Many 1.2.x instances run on exactly such a URL on a LAN,
// and passkeys are an optional second factor, so refusing to boot over one
// would take the whole help desk down to protect a feature those instances
// never used. Passkeys still must not be defaulted onto some other origin:
// that would produce credentials that silently never verify.
//
// A nil service is the "off" state; the server treats it as such everywhere.
// The reason is shown to administrators, so it is returned as well as logged.
func configurePasskeys(baseURL, siteName string, log *slog.Logger) (*webauthn.Service, string) {
	svc, err := webauthn.NewService(baseURL, siteName, log)
	if err != nil {
		reason := err.Error()
		log.Warn("passkeys unavailable: BASE_URL must be a domain name (or localhost) to use passkeys",
			"base_url", baseURL, "reason", reason)
		return nil, reason
	}
	return svc, ""
}
