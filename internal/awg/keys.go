package awg

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/curve25519"
)

// Keypair is an AmneziaWG (Curve25519) key pair, base64-encoded like wg.
type Keypair struct {
	Private string
	Public  string
}

// GenerateKeypair creates a new Curve25519 keypair. AmneziaWG uses the same
// key format as WireGuard, so these are interchangeable at the crypto layer;
// only the [Interface] obfuscation params differ.
func GenerateKeypair() (Keypair, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return Keypair{}, err
	}
	// Clamp per RFC 7748.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return Keypair{}, err
	}
	return Keypair{
		Private: base64.StdEncoding.EncodeToString(priv[:]),
		Public:  base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// GeneratePresharedKey returns a base64 32-byte PSK for an extra symmetric layer.
func GeneratePresharedKey() (string, error) {
	var psk [32]byte
	if _, err := rand.Read(psk[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(psk[:]), nil
}
