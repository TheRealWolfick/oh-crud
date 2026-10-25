package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// This function is used to encrypt data. It must be used on every key or otherwise sensitive
// information saved in the database[
func Encrypt(master_key []byte, plain_text []byte, auth_key []byte) ([]byte, error) {
	// Create a new cipher based on the master key
	block, err := aes.NewCipher(master_key)
	if err != nil { return nil, err }

	// Create a new gcm cipher based on the cipher block Galois Counter Mode
	gcm, err := cipher.NewGCM(block)
	if err != nil { return nil, err }

	// Create the nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil { return nil, err }

	return gcm.Seal(nonce, nonce, plain_text, auth_key), nil
}


// This function is used to decrypt data. It must be used on all public ad private keys being read from the database.
func Decrypt(master_key []byte, encrypted []byte, auth_key []byte) ([]byte, error) {
	// Create a new block with the master key
	block, err := aes.NewCipher(master_key)
	if err != nil { return nil, err }

	// Create a new gcm cipher for decripting
	gcm, err := cipher.NewGCM(block)
	if err != nil { return nil, err }

	// Extract the nonce
	nonce_size := gcm.NonceSize()
	if len(encrypted) < nonce_size { return nil, errors.New("ciphertext too small") }

	// Decript and return
	return gcm.Open(nil, encrypted[:nonce_size], encrypted[nonce_size:], auth_key)
}


// Encode a function into a sha256 hex string
func Sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
