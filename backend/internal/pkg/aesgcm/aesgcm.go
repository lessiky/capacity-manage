package aesgcm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// New 创建 AES-256-GCM 加解密器（key 为 32 字节 hex 字符串，64 位）
func New(keyHex string) (*Cipher, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid cred key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("cred key must be 32 bytes (64 hex chars)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

type Cipher struct{ gcm cipher.AEAD }

// Encrypt 明文 → "enc.v1." + base64(nonce||ciphertext)
func (c *Cipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return "enc.v1." + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 的输出；无 enc.v1. 前缀视为未加密直接返回（兼容旧数据迁移）
func (c *Cipher) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if len(enc) < 7 || enc[:7] != "enc.v1." {
		return enc, nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc[7:])
	if err != nil {
		return "", fmt.Errorf("bad cred encoding: %w", err)
	}
	nonce, ct := raw[:c.gcm.NonceSize()], raw[c.gcm.NonceSize():]
	plain, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("cred decrypt failed (key rotated?): %w", err)
	}
	return string(plain), nil
}