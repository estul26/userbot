package userbot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"
)

func TestEncryptedFileSessionStorageRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.enc")
	storage, err := NewEncryptedFileSessionStorage(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	want := []byte("raw gotd session bytes")
	if err := storage.StoreSession(context.Background(), want); err != nil {
		t.Fatalf("store session: %v", err)
	}

	got, err := storage.LoadSession(context.Background())
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("expected %q, got %q", want, got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	if info.Mode().Perm() != sessionFileMode {
		t.Fatalf("expected file mode %o, got %o", sessionFileMode, info.Mode().Perm())
	}
}

func TestEncryptedFileSessionStorageMissingFile(t *testing.T) {
	storage, err := NewEncryptedFileSessionStorage(filepath.Join(t.TempDir(), "missing.enc"), "passphrase")
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	_, err = storage.LoadSession(context.Background())
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("expected session.ErrNotFound, got %v", err)
	}
}

func TestEncryptedFileSessionStorageRejectsWrongPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.enc")
	storage, err := NewEncryptedFileSessionStorage(path, "right-passphrase")
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.StoreSession(context.Background(), []byte("secret session")); err != nil {
		t.Fatalf("store session: %v", err)
	}

	wrong, err := NewEncryptedFileSessionStorage(path, "wrong-passphrase")
	if err != nil {
		t.Fatalf("new wrong storage: %v", err)
	}
	if _, err := wrong.LoadSession(context.Background()); err == nil {
		t.Fatalf("expected wrong passphrase to fail")
	}
}

func TestEncryptedFileSessionStorageValidatesInputs(t *testing.T) {
	if _, err := NewEncryptedFileSessionStorage("", "passphrase"); err == nil {
		t.Fatalf("expected empty path to fail")
	}
	if _, err := NewEncryptedFileSessionStorage("session.enc", ""); err == nil {
		t.Fatalf("expected empty passphrase to fail")
	}
}
