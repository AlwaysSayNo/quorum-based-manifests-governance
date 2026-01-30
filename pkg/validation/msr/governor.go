package validation

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"

	common "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/validation"
)

// GetVerifiedSigners maps governors to signatures and returns warnings, if any noticed.
func GetVerifiedSigners(
	msr *dto.ManifestSigningRequestManifestObject,
	governorSignatures []dto.SignatureData,
	msrBytes []byte,
) (map[string]common.SignatureStatus, map[string]string) {
	// Build a map<alias, pubKey> of governor public keys for lookup.
	governorKeys := make(map[string]string)
	for _, gov := range msr.Spec.Governors.Members {
		governorKeys[gov.Alias] = gov.PublicKey
	}

	// Build a map<alias, status> of governors signatures.
	verifiedSigners := make(map[string]common.SignatureStatus)
	for _, gov := range msr.Spec.Governors.Members {
		verifiedSigners[gov.Alias] = common.Pending
	}

	signerWarnings := checkPublicKeysCorrectness(msr.Spec.Governors.Members, verifiedSigners)
	for _, sigData := range governorSignatures {
		// For each signature, try to verify it against every known governor's public key.
		for alias, pubKeyStr := range governorKeys {
			// Skip already processed keys.
			if verifiedSigners[alias] != common.Pending {
				continue
			}

			keyRing, err := openpgp.ReadArmoredKeyRing(bytes.NewReader([]byte(pubKeyStr)))
			if err != nil {
				// This governor has a malformed public key in the MSR Spec.
				verifiedSigners[alias] = common.MalformedPublicKey
				signerWarnings[alias] = err.Error()
				continue
			}

			// De-armor the signature before verification
			armorBlock, err := armor.Decode(bytes.NewReader(sigData))
			if err != nil {
				// Signature might be malformed, try next signer
				continue
			}

			_, err = openpgp.CheckDetachedSignature(keyRing, bytes.NewReader(msrBytes), armorBlock.Body, nil)
			if err == nil {
				verifiedSigners[alias] = common.Signed
				// Move to the next signature
				break
			}
		}
	}

	return verifiedSigners, signerWarnings
}

func checkPublicKeysCorrectness(
	members []dto.Governor,
	signers map[string]common.SignatureStatus,
) map[string]string {
	// Any warnings found.
	signerWarnings := make(map[string]string)
	for _, gov := range members {
		entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(gov.PublicKey))
		if err != nil {
			signerWarnings[gov.Alias] = fmt.Errorf("failed to parse armored key ring: %w", err).Error()
			continue
		}
		if len(entityList) < 1 {
			signerWarnings[gov.Alias] = fmt.Errorf("no OpenPGP entities found in the provided key string").Error()
			continue
		}

		entity := entityList[0]
		if entity.PrimaryKey == nil {
			signerWarnings[gov.Alias] = fmt.Errorf("PGP entity does not contain a primary key").Error()
			continue
		}
	}

	// Update signers statuses
	for alias := range signerWarnings {
		signers[alias] = common.MalformedPublicKey
	}

	return signerWarnings
}
