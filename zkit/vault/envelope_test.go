package vault_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vault"
)

func TestWrappedDEKLifecycle(t *testing.T) {
	password := []byte("correct horse battery staple")
	header, wrapped, session, err := vault.Initialize(password)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer session.Close()

	aad := []byte("wallet:wallet-1|schema:1|network:devnet")
	plaintext := []byte("private-key-material")
	envelope, err := session.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	unlocked, err := vault.Unlock(password, header, wrapped)
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	defer unlocked.Close()
	got, err := unlocked.Open(envelope, aad)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("open = %q, want %q", got, plaintext)
	}
}

func TestWrappedDEKRejectsWrongPasswordAndTampering(t *testing.T) {
	header, wrapped, session, err := vault.Initialize([]byte("correct"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if _, err := vault.Unlock([]byte("wrong"), header, wrapped); !errors.Is(err, vault.ErrAuthentication) {
		t.Fatalf("wrong password error = %v, want ErrAuthentication", err)
	}

	aad := []byte("wallet:a|schema:1|network:mainnet")
	envelope, err := session.Seal([]byte("secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*vault.Envelope){
		"nonce":      func(value *vault.Envelope) { value.Nonce[0] ^= 1 },
		"ciphertext": func(value *vault.Envelope) { value.Ciphertext[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			altered := envelope
			altered.Nonce = bytes.Clone(envelope.Nonce)
			altered.Ciphertext = bytes.Clone(envelope.Ciphertext)
			mutate(&altered)
			if _, err := session.Open(altered, aad); !errors.Is(err, vault.ErrAuthentication) {
				t.Fatalf("open error = %v, want ErrAuthentication", err)
			}
		})
	}
}

func TestWrappedDEKBindsAssociatedData(t *testing.T) {
	_, _, session, err := vault.Initialize([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	envelope, err := session.Seal([]byte("secret"), []byte("wallet:a|schema:1|network:devnet"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Open(envelope, []byte("wallet:b|schema:1|network:devnet")); !errors.Is(err, vault.ErrAuthentication) {
		t.Fatalf("cross-record open error = %v, want ErrAuthentication", err)
	}
}

func TestWrappedDEKRewrap(t *testing.T) {
	oldPassword := []byte("old password")
	header, wrapped, session, err := vault.Initialize(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("wallet:a|schema:1|network:testnet")
	envelope, err := session.Seal([]byte("secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Close()

	newHeader, newWrapped, err := vault.Rewrap(oldPassword, []byte("new password"), header, wrapped)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if bytes.Equal(header.Salt, newHeader.Salt) || bytes.Equal(wrapped.Ciphertext, newWrapped.Ciphertext) {
		t.Fatal("rewrap did not replace public wrapping material")
	}
	if _, err := vault.Unlock(oldPassword, newHeader, newWrapped); !errors.Is(err, vault.ErrAuthentication) {
		t.Fatalf("old password error = %v, want ErrAuthentication", err)
	}
	rotated, err := vault.Unlock([]byte("new password"), newHeader, newWrapped)
	if err != nil {
		t.Fatalf("unlock rotated vault: %v", err)
	}
	defer rotated.Close()
	got, err := rotated.Open(envelope, aad)
	if err != nil || string(got) != "secret" {
		t.Fatalf("open after rewrap = %q, %v", got, err)
	}
}

func TestWrappedDEKCloseFailsClosed(t *testing.T) {
	_, _, session, err := vault.Initialize([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := session.Seal([]byte("secret"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Seal(nil, nil); !errors.Is(err, vault.ErrClosed) {
		t.Fatalf("seal after close = %v, want ErrClosed", err)
	}
	if _, err := session.Open(envelope, nil); !errors.Is(err, vault.ErrClosed) {
		t.Fatalf("open after close = %v, want ErrClosed", err)
	}
}

func TestWrappedDEKPersistenceEncoding(t *testing.T) {
	header, wrapped, session, err := vault.Initialize([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	envelope, err := session.Seal([]byte("secret"), []byte("record"))
	if err != nil {
		t.Fatal(err)
	}

	headerJSON, err := vault.EncodeHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	decodedHeader, err := vault.DecodeHeader(headerJSON)
	if err != nil {
		t.Fatal(err)
	}
	wrappedJSON, err := vault.EncodeWrappedDEK(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	decodedWrapped, err := vault.DecodeWrappedDEK(wrappedJSON)
	if err != nil {
		t.Fatal(err)
	}
	envelopeJSON, err := vault.EncodeEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decodedEnvelope, err := vault.DecodeEnvelope(envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	unlocked, err := vault.Unlock([]byte("password"), decodedHeader, decodedWrapped)
	if err != nil {
		t.Fatal(err)
	}
	defer unlocked.Close()
	if got, err := unlocked.Open(decodedEnvelope, []byte("record")); err != nil || string(got) != "secret" {
		t.Fatalf("persistence round trip = %q, %v", got, err)
	}

	var value map[string]any
	if err := json.Unmarshal(headerJSON, &value); err != nil {
		t.Fatal(err)
	}
	value["unknown"] = true
	unknown, _ := json.Marshal(value)
	if _, err := vault.DecodeHeader(unknown); !errors.Is(err, vault.ErrInvalidHeader) {
		t.Fatalf("unknown header field error = %v", err)
	}
}

func TestWrappedDEKRejectsVersionsAndMalformedLengths(t *testing.T) {
	header, wrapped, session, err := vault.Initialize([]byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	header.Version++
	if _, err := vault.Unlock([]byte("password"), header, wrapped); !errors.Is(err, vault.ErrInvalidHeader) {
		t.Fatalf("header version error = %v", err)
	}
	wrapped.Nonce = wrapped.Nonce[:len(wrapped.Nonce)-1]
	if err := vault.ValidateWrappedDEK(wrapped); !errors.Is(err, vault.ErrInvalidWrappedDEK) {
		t.Fatalf("wrapped nonce error = %v", err)
	}
	for _, envelope := range []vault.Envelope{
		{Version: 99, Nonce: make([]byte, 12), Ciphertext: make([]byte, 16)},
		{Version: vault.CurrentEnvelopeVersion, Nonce: make([]byte, 11), Ciphertext: make([]byte, 16)},
		{Version: vault.CurrentEnvelopeVersion, Nonce: make([]byte, 12), Ciphertext: make([]byte, 15)},
	} {
		if _, err := session.Open(envelope, nil); !errors.Is(err, vault.ErrInvalidEnvelope) {
			t.Errorf("malformed envelope error = %v", err)
		}
	}
}
