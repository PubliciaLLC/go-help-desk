package main

import (
	"context"
	"io"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// resolveJWTSecret picks the key OAuth access tokens are signed with.
//
// An environment value that is one of the examples this project publishes is
// discarded and treated as unset: anyone can read the example, so a token
// signed with it proves nothing. discarded tells the caller to say so in the
// log. This is the one place the domain's ResolveJWTSecret is wrapped, because
// "which strings are published examples" is a config concern the domain must
// not import.
func resolveJWTSecret(ctx context.Context, env string, store auth.SecretStore, rnd io.Reader) (secret, source string, discarded bool, err error) {
	if config.IsExampleSecret(env) {
		env, discarded = "", true
	}
	secret, source, err = auth.ResolveJWTSecret(ctx, env, store, rnd)
	return secret, source, discarded, err
}
