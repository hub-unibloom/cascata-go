package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// getMasterKey derives a 32-byte key from INTERNAL_CTRL_SECRET
func getMasterKey() []byte {
	secret := os.Getenv("INTERNAL_CTRL_SECRET")
	if secret == "" {
		secret = "cascata_default_fallback_insecure_key_change_me"
	}
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

// Encrypt encrypts a string using AES-256-GCM
// Returns "iv:authTag:encryptedData" in base64
func Encrypt(text string) (string, error) {
	key := getMasterKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	// GCM Standard: Seal appends authTag to the ciphertext
	// Node equivalent: authTag = cipher.getAuthTag() + ciphertext
	// In Go, Seal(dst, nonce, plaintext, additionalData) appends tag to dst
	sealed := gcm.Seal(nil, iv, []byte(text), nil)

	// Node format is iv:authTag:encryptedData
	// Go Seal format is encryptedData + authTag (16 bytes)
	tagSize := gcm.Overhead()
	encryptedData := sealed[:len(sealed)-tagSize]
	authTag := sealed[len(sealed)-tagSize:]

	return fmt.Sprintf("%s:%s:%s",
		base64.StdEncoding.EncodeToString(iv),
		base64.StdEncoding.EncodeToString(authTag),
		base64.StdEncoding.EncodeToString(encryptedData),
	), nil
}

// Decrypt decrypts a boxed string in "iv:authTag:encryptedData" format
func Decrypt(boxed string) (string, error) {
	parts := strings.Split(boxed, ":")
	if len(parts) != 3 {
		return "", errors.New("invalid boxed format")
	}

	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}

	authTag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	encryptedData, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}

	key := getMasterKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	if len(iv) != gcm.NonceSize() {
		return "", errors.New("invalid nonce size")
	}

	// Reconstruct the Go-standard ciphertext: encryptedData + authTag
	ciphertext := append(encryptedData, authTag...)

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", errors.New("decryption failed: integrity check or key mismatch")
	}

	return string(plaintext), nil
}
