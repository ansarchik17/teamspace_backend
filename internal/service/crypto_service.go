package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

type CryptoService struct {
	key []byte
}

func NewCryptoService(rawKey string) *CryptoService {
	sum := sha256.Sum256([]byte(rawKey))
	return &CryptoService{key: sum[:]}
}

const (
	gcmIVLen = 12
)

func (c *CryptoService) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	iv := make([]byte, gcmIVLen)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	ct := gcm.Seal(nil, iv, []byte(plain), nil)

	out := make([]byte, 0, len(iv)+len(ct))
	out = append(out, iv...)
	out = append(out, ct...)

	return base64.StdEncoding.EncodeToString(out), nil
}

func (c *CryptoService) Decrypt(cipherText string) (string, error) {
	if cipherText == "" {
		return "", nil
	}

	all, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return "", err
	}
	if len(all) < gcmIVLen+1 {
		return "", errors.New("cipherText too short")
	}

	iv := all[:gcmIVLen]
	ct := all[gcmIVLen:]

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	pt, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
