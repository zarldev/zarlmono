# AGENTS.md — `zkit/vault`

Owns passphrase-derived key material and authenticated encryption primitives. It does not choose application settings, credential policy, environment fallbacks, or database locations.

## Security contracts

- `Open` uses only the explicitly supplied directory and passphrase callback. Never add an ambient environment fallback.
- `master.kdf` contains KDF parameters, salt, and verifier; it is not a substitute for protecting backups and historical plaintext elsewhere.
- Persisted key versions are compatibility identifiers. Unsupported versions fail closed and remain untouched for explicit replacement or migration by the owning service.
- Validate nonce length and persisted envelope shape before invoking cryptographic primitives that can panic or allocate without bound.
- New KDF material is installed atomically with private permissions and concurrent initializers cannot overwrite one another.
- Never log passphrases, derived keys, plaintext, ciphertext payloads, nonces paired with ciphertext, or verifier material.
- This package returns cryptographic/storage errors; application-level credential locking and migration semantics belong to `zkit/prefs` or its consumer.

## Verification

Use recognizable secret canaries and assert they do not appear in errors. Cover wrong passphrases, corruption, unsupported versions, concurrent initialization, and file permissions.

```bash
go test -C zkit -count=1 ./vault
go test -C zkit -race ./vault
```
