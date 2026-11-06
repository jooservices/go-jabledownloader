package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// releasePublicKeyB64 is the public half of the release signing key.
// The private half is the RELEASE_SIGNING_KEY secret and never ships in a release.
const releasePublicKeyB64 = "QMh8wblWtCl9gclyGY1tkvYGRx2eSsySuNLUj4f7QIE="

// releasePublicKey verifies checksums.txt. Tests replace it with a throwaway key.
var releasePublicKey = ed25519.PublicKey(mustPublicKey(releasePublicKeyB64))

// embeddedPublicKey is the key shipped in the binary. Tests restore it when
// they check that some other key cannot sign a release.
var embeddedPublicKey = append(ed25519.PublicKey(nil), releasePublicKey...)

func mustPublicKey(encoded string) []byte {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("release public key is invalid")
	}
	return key
}

// SignChecksums signs the exact checksums.txt bytes with an ed25519 private key.
// privateKey is a 32-byte seed or a 64-byte private key.
func SignChecksums(privateKey, message []byte) ([]byte, error) {
	var private ed25519.PrivateKey
	switch len(privateKey) {
	case ed25519.SeedSize:
		private = ed25519.NewKeyFromSeed(privateKey)
	case ed25519.PrivateKeySize:
		private = ed25519.PrivateKey(privateKey)
	default:
		return nil, fmt.Errorf("signing key must be %d or %d bytes", ed25519.SeedSize, ed25519.PrivateKeySize)
	}
	return ed25519.Sign(private, message), nil
}

func verifySignature(message, signature []byte) error {
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(releasePublicKey, message, signature) {
		return fmt.Errorf("checksums signature is invalid")
	}
	return nil
}

func decodeSignature(raw []byte) ([]byte, error) {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("checksums signature is invalid")
	}
	return signature, nil
}
