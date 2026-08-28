package auth

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig defines the configuration required to connect
// to an OpenID Connect identity provider.
type OIDCConfig struct {
	Enabled bool

	IssuerURL string

	ClientID string

	ClientSecret string

	RedirectURL string
}

// OIDCProvider represents an initialized OIDC authorization provider.
type OIDCProvider struct {
	Config   *oauth2.Config
	Verifier *oidc.IDTokenVerifier
}

// OIDCClaims contains the standard identity claims returned
// in an OpenID Connect ID token.
type OIDCClaims struct {
	Subject string `json:"sub"`

	Email string `json:"email"`

	EmailVerified bool `json:"email_verified"`

	Name string `json:"name"`

	GivenName string `json:"given_name"`

	FamilyName string `json:"family_name"`

	PreferredUsername string `json:"preferred_username"`
}

// NewOIDCProvider initializes an OIDC provider using discovery.
func NewOIDCProvider(
	ctx context.Context,
	cfg OIDCConfig,
) (*OIDCProvider, error) {

	provider, err := oidc.NewProvider(
		ctx,
		cfg.IssuerURL,
	)

	if err != nil {
		return nil, err
	}

	verifier := provider.Verifier(
		&oidc.Config{
			ClientID: cfg.ClientID,
		},
	)

	oauthCfg := &oauth2.Config{
		ClientID: cfg.ClientID,

		ClientSecret: cfg.ClientSecret,

		Endpoint: provider.Endpoint(),

		RedirectURL: cfg.RedirectURL,

		Scopes: []string{
			"openid",
			"profile",
			"email",
		},
	}

	return &OIDCProvider{
		Config:   oauthCfg,
		Verifier: verifier,
	}, nil
}

// AuthorizationURL creates the redirect URL to the OIDC provider.
func (p *OIDCProvider) AuthorizationURL(state string) string {

	return p.Config.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
	)
}

// Exchange exchanges the authorization code returned by the provider.
func (p *OIDCProvider) Exchange(
	ctx context.Context,
	code string,
) (*oauth2.Token, error) {

	return p.Config.Exchange(
		ctx,
		code,
	)
}

// VerifyIDToken validates the ID token signature and claims.
func (p *OIDCProvider) VerifyIDToken(
	ctx context.Context,
	rawIDToken string,
) (*oidc.IDToken, error) {

	return p.Verifier.Verify(
		ctx,
		rawIDToken,
	)
}

// DecodeClaims extracts application identity fields.
func DecodeClaims(
	token *oidc.IDToken,
) (OIDCClaims, error) {

	var claims OIDCClaims

	err := token.Claims(&claims)

	if err != nil {
		return OIDCClaims{}, err
	}

	return claims, nil
}
