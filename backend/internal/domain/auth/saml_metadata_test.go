package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// samlSPMetadataXML is a service provider's metadata: what this instance
// serves at /api/v1/auth/saml/metadata and shows on the same settings page, so
// the most likely thing to be pasted by mistake.
const samlSPMetadataXML = `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://hd/api/v1/auth/saml/metadata"><SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><AssertionConsumerService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="https://hd/acs" index="1"/></SPSSODescriptor></EntityDescriptor>`

func TestParseIdPMetadataXML(t *testing.T) {
	const entityID = "https://idp.testshib.org/idp/shibboleth"
	fixture := samlIDPMetadataXML
	body := fixture[strings.Index(fixture, "?>")+2:]

	cases := []struct {
		name         string
		input        []byte
		wantErr      error
		wantEntityID string
	}{
		{name: "TestShib IdP metadata", input: []byte(fixture), wantEntityID: entityID},
		{name: "UTF-8 byte order mark", input: append([]byte{0xEF, 0xBB, 0xBF}, fixture...), wantEntityID: entityID},
		{
			name:         "wrapped in EntitiesDescriptor",
			input:        []byte(`<EntitiesDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata">` + body + `</EntitiesDescriptor>`),
			wantEntityID: entityID,
		},
		{name: "empty", input: []byte(""), wantErr: auth.ErrIdPMetadataInvalid},
		{name: "not XML", input: []byte("hello"), wantErr: auth.ErrIdPMetadataInvalid},
		{name: "an HTML page", input: []byte("<html><body>sign in</body></html>"), wantErr: auth.ErrIdPMetadataInvalid},
		{name: "SP metadata pasted by mistake", input: []byte(samlSPMetadataXML), wantErr: auth.ErrIdPMetadataInvalid},
		{
			name:    "over the cap",
			input:   []byte(fixture + "<!--" + strings.Repeat("x", auth.MaxIdPMetadataXMLBytes) + "-->"),
			wantErr: auth.ErrIdPMetadataTooLarge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ed, err := auth.ParseIdPMetadataXML(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if ed != nil {
					t.Fatalf("got a descriptor alongside the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ed.EntityID != tc.wantEntityID {
				t.Fatalf("EntityID = %q, want %q", ed.EntityID, tc.wantEntityID)
			}
		})
	}
}

// TestParseIdPMetadataXML_CapIsExact pins the boundary: exactly the cap is
// accepted, one byte more is not.
func TestParseIdPMetadataXML_CapIsExact(t *testing.T) {
	pad := func(total int) []byte {
		head := samlIDPMetadataXML + "<!--"
		tail := "-->"
		fill := total - len(head) - len(tail)
		return []byte(head + strings.Repeat("x", fill) + tail)
	}
	if _, err := auth.ParseIdPMetadataXML(pad(auth.MaxIdPMetadataXMLBytes)); err != nil {
		t.Fatalf("exactly %d bytes: %v", auth.MaxIdPMetadataXMLBytes, err)
	}
	if _, err := auth.ParseIdPMetadataXML(pad(auth.MaxIdPMetadataXMLBytes + 1)); !errors.Is(err, auth.ErrIdPMetadataTooLarge) {
		t.Fatalf("one byte over: err = %v, want ErrIdPMetadataTooLarge", err)
	}
}

func TestParseIdPMetadataXML_FetchesNoExternalEntity(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("root:x:0:0"))
	}))
	defer srv.Close()

	doc := func(system string) []byte {
		x := strings.Replace(samlIDPMetadataXML, "<EntityDescriptor ",
			`<!DOCTYPE EntityDescriptor [<!ENTITY xxe SYSTEM "`+system+`">]><EntityDescriptor `, 1)
		x = strings.Replace(x,
			`Location="https://idp.testshib.org/idp/profile/SAML2/Redirect/SSO"`, `Location="&xxe;"`, 1)
		return []byte(x)
	}

	for _, system := range []string{srv.URL + "/x", "file:///etc/passwd"} {
		t.Run(system, func(t *testing.T) {
			_, err := auth.ParseIdPMetadataXML(doc(system))
			if !errors.Is(err, auth.ErrIdPMetadataInvalid) {
				t.Fatalf("err = %v, want ErrIdPMetadataInvalid", err)
			}
			if strings.Contains(err.Error(), "root:") {
				t.Fatalf("error leaks file content: %v", err)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the server was asked to fetch an external entity %d times", n)
	}
}
