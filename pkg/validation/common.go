package validation

import (
	"bytes"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

func VerifySignature(
	publicKey string,
	content []byte,
	signatureData []byte,
) (string, error) {
	msg := "CRITICAL WARNING: The file itself is invalid or has been tampered with. Do not proceed"
	if len(signatureData) == 0 {
		return msg, fmt.Errorf("operator signature is missing")
	}

	// The file must have associated the public key of the operator that signed it.
	if publicKey == "" {
		return msg, fmt.Errorf("operator public key is empty")
	}

	keyRing, err := openpgp.ReadArmoredKeyRing(bytes.NewReader([]byte(publicKey)))
	if err != nil {
		return msg, fmt.Errorf("failed to parse operator public key: %w", err)
	}

	// De-armor the signature before verification
	armorBlock, err := armor.Decode(bytes.NewReader(signatureData))
	if err != nil {
		return msg, fmt.Errorf("failed to decode armored signature: %w", err)
	}

	_, err = openpgp.CheckDetachedSignature(keyRing, bytes.NewReader(content), armorBlock.Body, nil)
	if err != nil {
		return msg, fmt.Errorf("signature verification failed: %w", err)
	}

	return "", nil
}
