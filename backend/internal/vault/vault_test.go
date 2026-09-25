package vault

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	box, err := seal(key, []byte("whsec_123"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(box, []byte("whsec_123")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := open(key, box)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "whsec_123" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenRejectsTamperingAndWrongKey(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	box, _ := seal(key, []byte("secret"))

	tampered := append([]byte(nil), box...)
	tampered[len(tampered)-1] ^= 1
	if _, err := open(key, tampered); err == nil {
		t.Fatal("expected tamper detection")
	}

	other := make([]byte, 32)
	rand.Read(other)
	if _, err := open(other, box); err == nil {
		t.Fatal("expected wrong-key failure")
	}
}

func TestLocalWrapperRequires32Bytes(t *testing.T) {
	if _, err := NewLocalWrapper("k", []byte("short")); err == nil {
		t.Fatal("expected error")
	}
}
