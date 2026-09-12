package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// ErrNonceMismatch reports an ID token whose nonce claim does not match the one
// issued with the authorization request that began this login.
var ErrNonceMismatch = errors.New("oidc: id token nonce mismatch")

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
		return nil, fmt.Errorf(
			"OIDC discovery for issuer %q: %w",
			cfg.IssuerURL,
			err,
		)
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

// AuthRequest is the result of starting a login: the URL to send the visitor
// to, plus the two per-login secrets the callback needs to finish it.
//
// Nonce and Verifier must be stored against the visitor's session and are
// useless — in fact dangerous — if shared between logins. They exist as one
// struct so a caller cannot generate the URL and forget to keep them.
type AuthRequest struct {
	URL string

	// Nonce is echoed by the IdP in the ID token, binding that token to this
	// authorization request.
	Nonce string

	// Verifier is the PKCE code_verifier. Its S256 challenge goes to the IdP
	// now; the verifier itself is presented at token exchange, proving the
	// exchange comes from whoever started the login.
	Verifier string
}

// AuthorizationURL creates the redirect URL to the OIDC provider, along with
// the nonce and PKCE verifier that must survive until the callback.
//
// access_type=offline is deliberately not requested: it asks the IdP for a
// refresh token, and this application neither stores nor uses one. Asking for
// a standing grant we throw away is gratuitous.
func (p *OIDCProvider) AuthorizationURL(state string) AuthRequest {

	// uuid.New draws from crypto/rand; this matches how state is generated.
	nonce := uuid.New().String()

	// PKCE generation is left to oauth2 rather than hand-rolled.
	verifier := oauth2.GenerateVerifier()

	return AuthRequest{
		URL: p.Config.AuthCodeURL(
			state,
			oidc.Nonce(nonce),
			oauth2.S256ChallengeOption(verifier),
		),
		Nonce:    nonce,
		Verifier: verifier,
	}
}

// Exchange exchanges the authorization code returned by the provider, proving
// via the PKCE verifier that this exchange belongs to the login that started it.
func (p *OIDCProvider) Exchange(
	ctx context.Context,
	code string,
	verifier string,
) (*oauth2.Token, error) {

	return p.Config.Exchange(
		ctx,
		code,
		oauth2.VerifierOption(verifier),
	)
}

// VerifyIDToken validates the ID token signature and claims, then checks that
// its nonce matches the one issued for this login.
//
// The nonce check is not optional and is why the caller must pass one: a token
// minted for a different session, or replayed from an earlier one, satisfies
// every signature and audience check just as well as a fresh one.
func (p *OIDCProvider) VerifyIDToken(
	ctx context.Context,
	rawIDToken string,
	nonce string,
) (*oidc.IDToken, error) {

	idToken, err := p.Verifier.Verify(
		ctx,
		rawIDToken,
	)

	if err != nil {
		return nil, err
	}

	// An empty expected nonce would otherwise match a token carrying no nonce
	// at all, quietly reinstating the defect this check exists to close.
	if nonce == "" || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return nil, ErrNonceMismatch
	}

	return idToken, nil
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
