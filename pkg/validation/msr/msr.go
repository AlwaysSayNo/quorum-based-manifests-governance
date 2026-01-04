package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"

	dto "github.com/AlwaysSayNo/quorum-based-manifests-governance/pkg/api/dto"
)

// VerifyMSRSignature checks the detached signature of the MSR against the operator's public key.
func VerifyMSRSignature(
	msr *dto.ManifestSigningRequestManifestObject,
	msrBytes []byte,
	signatureData []byte,
) (string, error) {
	msg := "CRITICAL WARNING: The Manifest Signing Request itself is invalid or has been tampered with. Do not proceed"
	if len(signatureData) == 0 {
		return msg, fmt.Errorf("operator signature is missing")
	}

	// The MSR spec must contain the public key of the operator that signed it.
	operatorPubKeyStr := msr.Spec.PublicKey
	if operatorPubKeyStr == "" {
		return msg, fmt.Errorf("operator public key is not present in the MSR spec")
	}

	keyRing, err := openpgp.ReadArmoredKeyRing(bytes.NewReader([]byte(operatorPubKeyStr)))
	if err != nil {
		return msg, fmt.Errorf("failed to parse operator public key: %w", err)
	}

	// De-armor the signature before verification
	armorBlock, err := armor.Decode(bytes.NewReader(signatureData))
	if err != nil {
		return msg, fmt.Errorf("failed to decode armored signature: %w", err)
	}

	_, err = openpgp.CheckDetachedSignature(keyRing, bytes.NewReader(msrBytes), armorBlock.Body, nil)
	if err != nil {
		return msg, fmt.Errorf("signature verification failed: %w", err)
	}

	return "", nil
}

func VerifyChangedFiles(
	msr *dto.ManifestSigningRequestManifestObject,
	changedFiles map[string]dto.FileBytesWithStatus,
) (string, error) {
	msrChanges := msr.Spec.Changes
	var err error
	// Check length of both slices.
	if len(msrChanges) != len(changedFiles) {
		err = fmt.Errorf("different changes length: %d (MSR) != %d (Git)", len(msrChanges), len(changedFiles))
	}

	// If lengths are equal, check that they have the same paths / status.
	if err == nil {
		for _, cf := range msrChanges {
			cfGit, ok := changedFiles[cf.Path]
			if !ok || cfGit.Status != cf.Status {
				err = fmt.Errorf("Git and MSR have different changed file sets (files / statuses)")
			}
		}
	}

	// Return, if error found.
	if err != nil {
		msrFilePathsStr := formatAndSortFiles(msrChanges)
		gitFilePathsStr := formatAndSortFiles(slices.Collect(maps.Values(changedFiles)))

		return fmt.Sprintf("MSR files:\n%s\n\nGit files:\n%s", msrFilePathsStr, gitFilePathsStr),
			err
	}

	// Check, that files have equal hashes.
	var differentHashes []string
	for _, cf := range msrChanges {
		cfGit := changedFiles[cf.Path]

		// Calculate SHA256.
		hasher := sha256.New()
		hasher.Write(cfGit.Content)
		sha256Hex := hex.EncodeToString(hasher.Sum(nil))

		if sha256Hex != cf.SHA256 {
			differentHashes = append(differentHashes, fmt.Sprintf("%s: %s vs %s", cf.Path, sha256Hex, cf.SHA256))
		}
	}

	// Return error, if any file changes have different hashes.
	if len(differentHashes) != 0 {
		return fmt.Sprintf("Hashes (Git vs MSR): %s", strings.Join(differentHashes, "\n")),
			fmt.Errorf("some Git and MSR files have different SHA256")
	}

	return "", nil
}

func formatAndSortFiles[T dto.PathStatusGetter](
	files []T,
) string {
	var paths []string
	for _, f := range files {
		paths = append(paths, fmt.Sprintf("%s (%s)", f.GetPath(), f.GetStatus()))
	}

	// Sort ascending lexicographically
	slices.Sort(paths)

	return strings.Join(paths, "\n")
}
