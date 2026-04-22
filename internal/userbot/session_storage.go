package userbot

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/session"
	"golang.org/x/crypto/scrypt"
)

const (
	sessionFileVersion = 1
	sessionSaltSize    = 16
	sessionKeySize     = 32
	sessionScryptN     = 32768
	sessionScryptR     = 8
	sessionScryptP     = 1
	sessionFileMode    = 0o600
	sessionDirMode     = 0o700
)

type encryptedSessionFile struct {
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Salt    []byte `json:"salt"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"`
}

// EncryptedFileSessionStorage stores gotd session bytes in an encrypted local
// file. The passphrase is never persisted.
type EncryptedFileSessionStorage struct {
	path       string
	passphrase string
}

func NewEncryptedFileSessionStorage(path, passphrase string) (*EncryptedFileSessionStorage, error) {
	path = strings.TrimSpace(path)
	passphrase = strings.TrimSpace(passphrase)
	if path == "" {
		return nil, errors.New("session path is required")
	}
	if passphrase == "" {
		return nil, errors.New("session passphrase is required")
	}

	return &EncryptedFileSessionStorage{
		path:       path,
		passphrase: passphrase,
	}, nil
}

func (s *EncryptedFileSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, session.ErrNotFound
		}
		return nil, fmt.Errorf("read encrypted session: %w", err)
	}

	var payload encryptedSessionFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode encrypted session: %w", err)
	}
	if payload.Version != sessionFileVersion {
		return nil, fmt.Errorf("unsupported encrypted session version %d", payload.Version)
	}
	if payload.KDF != "scrypt" {
		return nil, fmt.Errorf("unsupported encrypted session kdf %q", payload.KDF)
	}

	key, err := deriveSessionKey(s.passphrase, payload.Salt)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create session cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create session gcm: %w", err)
	}

	plain, err := gcm.Open(nil, payload.Nonce, payload.Data, nil)
	if err != nil {
		return nil, errors.New("decrypt encrypted session: passphrase is invalid or file is corrupted")
	}

	return plain, nil
}

func (s *EncryptedFileSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}

	salt, err := randomBytes(sessionSaltSize)
	if err != nil {
		return err
	}
	key, err := deriveSessionKey(s.passphrase, salt)
	if err != nil {
		return err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create session cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create session gcm: %w", err)
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return err
	}

	payload := encryptedSessionFile{
		Version: sessionFileVersion,
		KDF:     "scrypt",
		Salt:    salt,
		Nonce:   nonce,
		Data:    gcm.Seal(nil, nonce, data, nil),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode encrypted session: %w", err)
	}

	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, sessionDirMode); err != nil {
			return fmt.Errorf("create session directory: %w", err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create session temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write encrypted session: %w", err)
	}
	if err := tmp.Chmod(sessionFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod encrypted session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close encrypted session: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace encrypted session: %w", err)
	}

	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}

func randomBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	return buf, nil
}

func deriveSessionKey(passphrase string, salt []byte) ([]byte, error) {
	if len(salt) != sessionSaltSize {
		return nil, errors.New("invalid encrypted session salt")
	}

	key, err := scrypt.Key([]byte(passphrase), salt, sessionScryptN, sessionScryptR, sessionScryptP, sessionKeySize)
	if err != nil {
		return nil, fmt.Errorf("derive session key: %w", err)
	}
	return key, nil
}
