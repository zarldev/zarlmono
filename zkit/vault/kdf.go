package vault

import (
	"errors"
	"fmt"
)

// ErrInvalidKDF means persisted legacy master.kdf data is malformed or unsafe.
var ErrInvalidKDF = errors.New("vault: invalid KDF metadata")

func validateKDF(f kdfFile) error {
	if f.Time < minKDFTime || f.Time > maxKDFTime ||
		f.Memory < minKDFMemory || f.Memory > maxKDFMemory ||
		f.Threads < minKDFThreads || f.Threads > maxKDFThreads {
		return fmt.Errorf("%w: Argon2id parameters outside supported limits", ErrInvalidKDF)
	}
	if f.KeyLength != masterKeySize || len(f.Salt) != masterKeySize {
		return fmt.Errorf("%w: invalid key or salt length", ErrInvalidKDF)
	}
	if len(f.VNonce) != gcmNonceSize || len(f.Verifier) != len(verifierPlaintext)+gcmTagSize {
		return fmt.Errorf("%w: invalid verifier or nonce length", ErrInvalidKDF)
	}
	return nil
}
