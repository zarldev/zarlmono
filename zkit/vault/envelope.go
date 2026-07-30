package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	// CurrentEnvelopeVersion is the persistence format understood by the wrapped-DEK API.
	CurrentEnvelopeVersion uint16 = 1

	wrappedKeySize     = 32
	wrappedSaltSize    = 32
	gcmNonceSize       = 12
	gcmTagSize         = 16
	maxPassphraseSize  = 1 << 20
	maxAssociatedData  = 64 << 10
	maxEnvelopePayload = 16 << 20
	maxEncodedHeader   = 64 << 10
)

const (
	minKDFTime    uint32 = 1
	maxKDFTime    uint32 = 10
	minKDFMemory  uint32 = 8 * 1024
	maxKDFMemory  uint32 = 256 * 1024
	minKDFThreads uint8  = 1
	maxKDFThreads uint8  = 16
)

var (
	// ErrInvalidHeader means persisted vault header data is malformed or unsupported.
	ErrInvalidHeader = errors.New("vault: invalid header")
	// ErrInvalidWrappedDEK means persisted wrapped key data is malformed or unsupported.
	ErrInvalidWrappedDEK = errors.New("vault: invalid wrapped DEK")
	// ErrInvalidEnvelope means persisted encrypted record data is malformed or unsupported.
	ErrInvalidEnvelope = errors.New("vault: invalid envelope")
	// ErrAuthentication means a password, associated-data value, or encrypted value did not authenticate.
	ErrAuthentication = errors.New("vault: authentication failed")
	// ErrClosed means an operation was attempted after its session was closed.
	ErrClosed = errors.New("vault: session closed")
)

// KDFParameters are the bounded Argon2id costs persisted in a Header.
type KDFParameters struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// Header is public persistence metadata used to derive a key-encryption key.
// It contains no password-derived key material.
type Header struct {
	Version uint16        `json:"version"`
	KDF     KDFParameters `json:"kdf"`
	Salt    []byte        `json:"salt"`
}

// WrappedDEK is a data-encryption key authenticated and encrypted by a password-derived key.
type WrappedDEK struct {
	Version    uint16 `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Envelope is a versioned encrypted record. Callers must persist the associated data
// inputs used with Seal separately as stable record identity.
type Envelope struct {
	Version    uint16 `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Session owns an unlocked data-encryption key. Close it as soon as vault work is complete.
type Session struct {
	mu     sync.RWMutex
	dek    []byte
	closed bool
}

// Initialize creates a fresh vault and returns all public persistence data plus an
// unlocked session. The caller owns password and should wipe it after this call.
func Initialize(password []byte) (Header, WrappedDEK, *Session, error) {
	if err := validatePassword(password); err != nil {
		return Header{}, WrappedDEK{}, nil, err
	}
	header := Header{
		Version: CurrentEnvelopeVersion,
		KDF: KDFParameters{
			Time:      argonTime,
			MemoryKiB: argonMemory,
			Threads:   argonThreads,
		},
		Salt: make([]byte, wrappedSaltSize),
	}
	if _, err := rand.Read(header.Salt); err != nil {
		return Header{}, WrappedDEK{}, nil, fmt.Errorf("vault salt: %w", err)
	}
	dek := make([]byte, wrappedKeySize)
	if _, err := rand.Read(dek); err != nil {
		wipe(dek)
		return Header{}, WrappedDEK{}, nil, fmt.Errorf("vault DEK: %w", err)
	}
	wrapped, err := wrapDEK(password, header, dek)
	if err != nil {
		wipe(dek)
		return Header{}, WrappedDEK{}, nil, err
	}
	return cloneHeader(header), wrapped, &Session{dek: dek}, nil
}

// Unlock derives a key-encryption key, authenticates and unwraps the persisted DEK,
// and returns a new backend-only session. Wrong passwords and tampering fail alike.
func Unlock(password []byte, header Header, wrapped WrappedDEK) (*Session, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := ValidateHeader(header); err != nil {
		return nil, err
	}
	if err := ValidateWrappedDEK(wrapped); err != nil {
		return nil, err
	}
	kek := deriveKEK(password, header)
	defer wipe(kek)
	aead, err := aeadFromKey(kek)
	if err != nil {
		return nil, err
	}
	dek, err := aead.Open(nil, wrapped.Nonce, wrapped.Ciphertext, wrappedAAD(header))
	if err != nil || len(dek) != wrappedKeySize {
		wipe(dek)
		return nil, ErrAuthentication
	}
	return &Session{dek: dek}, nil
}

// Rewrap rotates a vault password without decrypting or re-encrypting record envelopes.
func Rewrap(oldPassword, newPassword []byte, header Header, wrapped WrappedDEK) (Header, WrappedDEK, error) {
	if err := validatePassword(newPassword); err != nil {
		return Header{}, WrappedDEK{}, err
	}
	session, err := Unlock(oldPassword, header, wrapped)
	if err != nil {
		return Header{}, WrappedDEK{}, err
	}
	defer session.Close()

	newHeader := Header{Version: CurrentEnvelopeVersion, KDF: header.KDF, Salt: make([]byte, wrappedSaltSize)}
	if _, err := rand.Read(newHeader.Salt); err != nil {
		return Header{}, WrappedDEK{}, fmt.Errorf("vault salt: %w", err)
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	newWrapped, err := wrapDEK(newPassword, newHeader, session.dek)
	if err != nil {
		return Header{}, WrappedDEK{}, err
	}
	return cloneHeader(newHeader), newWrapped, nil
}

// Seal encrypts private record bytes and authenticates explicit, caller-defined record identity.
func (s *Session) Seal(plaintext, associatedData []byte) (Envelope, error) {
	if len(plaintext) > maxEnvelopePayload {
		return Envelope{}, fmt.Errorf("%w: plaintext exceeds %d bytes", ErrInvalidEnvelope, maxEnvelopePayload)
	}
	if err := validateAAD(associatedData); err != nil {
		return Envelope{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Envelope{}, ErrClosed
	}
	aead, err := aeadFromKey(s.dek)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("vault envelope nonce: %w", err)
	}
	return Envelope{
		Version:    CurrentEnvelopeVersion,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, payloadAAD(associatedData)),
	}, nil
}

// Open authenticates and decrypts an envelope using the same record identity passed to Seal.
func (s *Session) Open(envelope Envelope, associatedData []byte) ([]byte, error) {
	if err := ValidateEnvelope(envelope); err != nil {
		return nil, err
	}
	if err := validateAAD(associatedData); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	aead, err := aeadFromKey(s.dek)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, payloadAAD(associatedData))
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

// Close wipes session-owned DEK bytes and permanently closes the session. It is idempotent.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	wipe(s.dek)
	s.dek = nil
	return nil
}

// ValidateHeader rejects unsupported versions and unsafe KDF inputs before Argon2id runs.
func ValidateHeader(header Header) error {
	if header.Version != CurrentEnvelopeVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidHeader, header.Version)
	}
	if len(header.Salt) != wrappedSaltSize {
		return fmt.Errorf("%w: salt is %d bytes", ErrInvalidHeader, len(header.Salt))
	}
	if header.KDF.Time < minKDFTime || header.KDF.Time > maxKDFTime ||
		header.KDF.MemoryKiB < minKDFMemory || header.KDF.MemoryKiB > maxKDFMemory ||
		header.KDF.Threads < minKDFThreads || header.KDF.Threads > maxKDFThreads {
		return fmt.Errorf("%w: Argon2id parameters outside supported limits", ErrInvalidHeader)
	}
	return nil
}

// ValidateWrappedDEK checks persisted wrapped key lengths before calling AES-GCM.
func ValidateWrappedDEK(wrapped WrappedDEK) error {
	if wrapped.Version != CurrentEnvelopeVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidWrappedDEK, wrapped.Version)
	}
	if len(wrapped.Nonce) != gcmNonceSize || len(wrapped.Ciphertext) != wrappedKeySize+gcmTagSize {
		return fmt.Errorf("%w: invalid ciphertext or nonce length", ErrInvalidWrappedDEK)
	}
	return nil
}

// ValidateEnvelope checks persistence fields and bounds before calling AES-GCM.
func ValidateEnvelope(envelope Envelope) error {
	if envelope.Version != CurrentEnvelopeVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidEnvelope, envelope.Version)
	}
	if len(envelope.Nonce) != gcmNonceSize || len(envelope.Ciphertext) < gcmTagSize || len(envelope.Ciphertext) > maxEnvelopePayload+gcmTagSize {
		return fmt.Errorf("%w: invalid ciphertext or nonce length", ErrInvalidEnvelope)
	}
	return nil
}

// EncodeHeader returns the stable JSON persistence encoding for a validated header.
func EncodeHeader(header Header) ([]byte, error) {
	if err := ValidateHeader(header); err != nil {
		return nil, err
	}
	return json.Marshal(header)
}

// DecodeHeader strictly decodes and validates persisted header data.
func DecodeHeader(data []byte) (Header, error) {
	var header Header
	if err := strictDecode(data, maxEncodedHeader, &header); err != nil {
		return Header{}, errors.Join(ErrInvalidHeader, err)
	}
	if err := ValidateHeader(header); err != nil {
		return Header{}, err
	}
	return cloneHeader(header), nil
}

// EncodeWrappedDEK returns the stable JSON persistence encoding for a validated wrapped DEK.
func EncodeWrappedDEK(wrapped WrappedDEK) ([]byte, error) {
	if err := ValidateWrappedDEK(wrapped); err != nil {
		return nil, err
	}
	return json.Marshal(wrapped)
}

// DecodeWrappedDEK strictly decodes and validates persisted wrapped key data.
func DecodeWrappedDEK(data []byte) (WrappedDEK, error) {
	var wrapped WrappedDEK
	if err := strictDecode(data, maxEncodedHeader, &wrapped); err != nil {
		return WrappedDEK{}, errors.Join(ErrInvalidWrappedDEK, err)
	}
	if err := ValidateWrappedDEK(wrapped); err != nil {
		return WrappedDEK{}, err
	}
	return cloneWrappedDEK(wrapped), nil
}

// EncodeEnvelope returns the stable JSON persistence encoding for a validated envelope.
func EncodeEnvelope(envelope Envelope) ([]byte, error) {
	if err := ValidateEnvelope(envelope); err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

// DecodeEnvelope strictly decodes and validates persisted record data.
func DecodeEnvelope(data []byte) (Envelope, error) {
	var envelope Envelope
	if len(data) > maxEnvelopePayload*2 {
		return Envelope{}, fmt.Errorf("%w: encoded value too large", ErrInvalidEnvelope)
	}
	if err := strictDecode(data, maxEnvelopePayload*2, &envelope); err != nil {
		return Envelope{}, errors.Join(ErrInvalidEnvelope, err)
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	return cloneEnvelope(envelope), nil
}

func wrapDEK(password []byte, header Header, dek []byte) (WrappedDEK, error) {
	if err := ValidateHeader(header); err != nil {
		return WrappedDEK{}, err
	}
	kek := deriveKEK(password, header)
	defer wipe(kek)
	aead, err := aeadFromKey(kek)
	if err != nil {
		return WrappedDEK{}, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return WrappedDEK{}, fmt.Errorf("vault wrapped DEK nonce: %w", err)
	}
	return WrappedDEK{Version: CurrentEnvelopeVersion, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, dek, wrappedAAD(header))}, nil
}

func deriveKEK(password []byte, header Header) []byte {
	return argon2.IDKey(password, header.Salt, header.KDF.Time, header.KDF.MemoryKiB, header.KDF.Threads, wrappedKeySize)
}

func validatePassword(password []byte) error {
	if len(password) == 0 || len(password) > maxPassphraseSize {
		return fmt.Errorf("%w: password length outside supported limits", ErrInvalidHeader)
	}
	return nil
}

func validateAAD(aad []byte) error {
	if len(aad) > maxAssociatedData {
		return fmt.Errorf("%w: associated data exceeds %d bytes", ErrInvalidEnvelope, maxAssociatedData)
	}
	return nil
}

func wrappedAAD(header Header) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 64))
	buf.WriteString("zkit/vault/wrapped-dek")
	_ = binary.Write(buf, binary.BigEndian, header.Version)
	_ = binary.Write(buf, binary.BigEndian, header.KDF.Time)
	_ = binary.Write(buf, binary.BigEndian, header.KDF.MemoryKiB)
	buf.WriteByte(header.KDF.Threads)
	_ = binary.Write(buf, binary.BigEndian, uint32(wrappedSaltSize))
	buf.Write(header.Salt)
	return buf.Bytes()
}

func payloadAAD(aad []byte) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, len(aad)+32))
	buf.WriteString("zkit/vault/payload")
	_ = binary.Write(buf, binary.BigEndian, CurrentEnvelopeVersion)
	_ = binary.Write(buf, binary.BigEndian, uint64(len(aad)))
	buf.Write(aad)
	return buf.Bytes()
}

func strictDecode(data []byte, limit int, dst any) error {
	if len(data) == 0 || len(data) > limit {
		return errors.New("encoded value has invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing data")
		}
		return err
	}
	return nil
}

func cloneHeader(header Header) Header {
	header.Salt = bytes.Clone(header.Salt)
	return header
}

func cloneWrappedDEK(wrapped WrappedDEK) WrappedDEK {
	wrapped.Nonce = bytes.Clone(wrapped.Nonce)
	wrapped.Ciphertext = bytes.Clone(wrapped.Ciphertext)
	return wrapped
}

func cloneEnvelope(envelope Envelope) Envelope {
	envelope.Nonce = bytes.Clone(envelope.Nonce)
	envelope.Ciphertext = bytes.Clone(envelope.Ciphertext)
	return envelope
}

//go:noinline
func wipe(value []byte) {
	clear(value)
	runtime.KeepAlive(value)
}
