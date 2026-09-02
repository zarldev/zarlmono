package db

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

func transcriptChecksum(sessionID string, revision uint64, entries []TranscriptEntry) string {
	digest := sha256.New()
	writeChecksumString(digest, sessionID)
	writeChecksumUint64(digest, revision)
	for _, entry := range entries {
		writeChecksumUint64(digest, entry.Sequence)
		writeChecksumString(digest, entry.EntryID)
		writeChecksumString(digest, entry.ParentID)
		writeChecksumString(digest, entry.TurnID)
		writeChecksumString(digest, entry.Kind)
		writeChecksumUint64(digest, entry.Revision)
		writeChecksumBytes(digest, entry.PayloadJSON)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeChecksumString(digest hash.Hash, value string) { writeChecksumBytes(digest, []byte(value)) }

func writeChecksumBytes(digest hash.Hash, value []byte) {
	writeChecksumUint64(digest, uint64(len(value)))
	_, _ = digest.Write(value)
}

func writeChecksumUint64(digest hash.Hash, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = digest.Write(buffer[:])
}
