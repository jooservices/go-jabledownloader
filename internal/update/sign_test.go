package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestReleaseKeyRejectsForeignSignature(t *testing.T) {
	saved := append(ed25519.PublicKey(nil), releasePublicKey...)
	releasePublicKey = embeddedPublicKey
	t.Cleanup(func() { releasePublicKey = saved })
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("checksums")
	signature, err := SignChecksums(private, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(message, signature); err == nil {
		t.Fatal("embedded release key accepted a signature from another key")
	}
	decoded, err := decodeSignature([]byte("not-a-signature"))
	if err == nil || decoded != nil {
		t.Fatalf("decoded = %v err = %v", decoded, err)
	}
}
