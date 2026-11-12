package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// SoftKey is a software authenticator: just enough of a WebAuthn key to answer
// a sign-in challenge with a signature counter of the test's choosing. The
// real ceremonies belong to go-webauthn; this exists so a test can drive them
// with a counter that does not advance, which no browser will produce on
// request.
type SoftKey struct {
	CredentialID []byte
	rpID, origin string
	priv         *ecdsa.PrivateKey
}

// NewSoftKey makes a key bound to one relying party, as a real one is.
func NewSoftKey(t testing.TB, rpID, origin string) *SoftKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatalf("generating credential id: %v", err)
	}
	return &SoftKey{CredentialID: id, rpID: rpID, origin: origin, priv: priv}
}

// PublicKey is the COSE_Key a registration would have stored.
func (k *SoftKey) PublicKey(t testing.TB) []byte {
	t.Helper()
	x, y := k.priv.PublicKey.X.FillBytes(make([]byte, 32)), k.priv.PublicKey.Y.FillBytes(make([]byte, 32))
	b, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{
			KeyType:   int64(webauthncose.EllipticKey),
			Algorithm: int64(webauthncose.AlgES256),
		},
		Curve:  int64(webauthncose.P256),
		XCoord: x,
		YCoord: y,
	})
	if err != nil {
		t.Fatalf("encoding public key: %v", err)
	}
	return b
}

// Register answers a registration challenge with a "none" attestation, which is
// what a browser sends unless the site asks otherwise, and returns the request
// body a browser would post.
func (k *SoftKey) Register(t testing.TB, challenge string) []byte {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString

	clientData, err := json.Marshal(map[string]string{
		"type": "webauthn.create", "challenge": challenge, "origin": k.origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	// rpIdHash, flags (user present, attested credential data), counter, an
	// all-zero AAGUID, then the credential id and its public key.
	rpHash := sha256.Sum256([]byte(k.rpID))
	authData := append(rpHash[:], 0x41)
	authData = binary.BigEndian.AppendUint32(authData, 0)
	authData = append(authData, make([]byte, 16)...)
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(k.CredentialID)))
	authData = append(authData, k.CredentialID...)
	authData = append(authData, k.PublicKey(t)...)

	attObj, err := webauthncbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": authData,
	})
	if err != nil {
		t.Fatalf("encoding attestation: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"id": enc(k.CredentialID), "rawId": enc(k.CredentialID), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    enc(clientData),
			"attestationObject": enc(attObj),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Assert answers a sign-in challenge (the base64url string the server sent)
// and returns the request body a browser would post. userHandle is the
// account's WebAuthn user id.
func (k *SoftKey) Assert(t testing.TB, challenge string, userHandle []byte, counter uint32) []byte {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString

	clientData, err := json.Marshal(map[string]string{
		"type": "webauthn.get", "challenge": challenge, "origin": k.origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte(k.rpID))
	authData := append(rpHash[:], 0x01) // flags: user present
	authData = binary.BigEndian.AppendUint32(authData, counter)

	cdHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"id": enc(k.CredentialID), "rawId": enc(k.CredentialID), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    enc(clientData),
			"authenticatorData": enc(authData),
			"signature":         enc(sig),
			"userHandle":        enc(userHandle),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
