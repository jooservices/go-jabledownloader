// Command release-sign writes the ed25519 signature of checksums.txt.
// The private key is RELEASE_SIGNING_KEY, base64 of the 64-byte key.
package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/jooservices/go-jabledownloader/internal/update"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: release-sign <checksums.txt> <checksums.txt.sig>")
		os.Exit(2)
	}
	encoded := os.Getenv("RELEASE_SIGNING_KEY")
	if encoded == "" {
		fmt.Fprintln(os.Stderr, "RELEASE_SIGNING_KEY is required")
		os.Exit(1)
	}
	private, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		fmt.Fprintln(os.Stderr, "RELEASE_SIGNING_KEY is not base64")
		os.Exit(1)
	}
	message, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	signature, err := update.SignChecksums(private, message)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
