// Package claim carries the author's sealed identity claim: an encrypted
// statement of the idea and its author, embedded in every binary. The
// ciphertext is public — anyone can read these bytes, and that is fine.
// Only the holder of the 32-byte sealing key can open the blob, which is
// the whole point: retrieving the claim IS the proof of authorship,
// because the only way in is the key the author alone keeps. (Hiding
// would be security by obscurity; sealing is a lock with one key.)
package claim

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
)

// claim.bin is the sealed blob (written by claim/seal.go); without this
// directive the binary ships an empty seal and no key can ever open it.
//
//go:embed claim.bin
var sealed []byte

const (
	nonceSize = 12
	keySize   = 32
)

var errBadKeySize = errors.New("claim: key must be 32 bytes")

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, errBadKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Seal(key, plaintext []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

func Open(key, blob []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < nonceSize {
		return nil, errors.New("claim: sealed blob is truncated")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("claim: this key does not open the seal: %w", err)
	}
	return plaintext, nil
}

func Claim(key []byte) (string, error) {
	plaintext, err := Open(key, sealed)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
