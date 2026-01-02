package crypto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type Secrets struct {
	PrivateKey string
	Passphrase string
}

func GetPGPSecrets(
	path, passphrase string,
) (*Secrets, error) {
	privateKey, err := readPrivateKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pgp privateKey: %w", err)
	}

	return &Secrets{
		PrivateKey: privateKey,
		Passphrase: passphrase,
	}, nil
}

func GetSSHSecrets(
	path, passphrase string,
) (*Secrets, error) {
	privateKey, err := readPrivateKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ssh privateKey: %w", err)
	}

	return &Secrets{
		PrivateKey: privateKey,
		Passphrase: passphrase,
	}, nil
}

func SyncSSHSecrets(
	ctx context.Context,
	secrets *Secrets,
) (*ssh.PublicKeys, error) {
	if secrets == nil {
		return nil, fmt.Errorf("ssh information is nil")
	}

	privateKeyBytes := []byte(secrets.PrivateKey)

	// Decrypt the private key
	publicKeys, err := ssh.NewPublicKeys("git", privateKeyBytes, secrets.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to create public keys from secret: %w", err)
	}

	return publicKeys, nil
}

func readPrivateKeyFile(
	path string,
) (string, error) {
	if path == "" {
		return "", fmt.Errorf("secret path is not provided")
	}

	// Read PGP file with privateKey in it
	file, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}

	privateKey := string(file)
	if privateKey == "" {
		return "", fmt.Errorf("privateKey is empty")
	}

	return privateKey, nil
}

func readFile(
	path string,
) ([]byte, error) {
	p := filepath.Join(filepath.Clean(path))
	if _, err := os.Stat(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Unknown error
		return nil, fmt.Errorf("fetch file from location %s: %w", path, err)
	}

	// Get file and extract data from it
	file, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", p, err)
	}

	return file, nil
}

func GetPGPEntity(
	ctx context.Context,
	secrets *Secrets,
) (*openpgp.Entity, error) {
	if secrets == nil {
		return nil, fmt.Errorf("secret object is nil")
	} else if secrets.PrivateKey == "" {
		return nil, fmt.Errorf("PGP private key is not configured")
	}

	entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(secrets.PrivateKey))
	if err != nil {
		return nil, err
	}
	entity := entityList[0]

	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		// If a passphrase is required but not provided, fail.
		if secrets.Passphrase == "" {
			return nil, fmt.Errorf("PGP private key is encrypted, but no passphrase was provided")
		}

		passphrase := []byte(secrets.Passphrase)

		// Attempt to decrypt the private key.
		err := entity.PrivateKey.Decrypt(passphrase)
		if err != nil || entity.PrivateKey.Encrypted {
			return nil, fmt.Errorf("failed to decrypt PGP private key: %w", err)
		}
	}

	return entity, nil
}

func CreateDetachedSignature(
	ctx context.Context,
	fileContent []byte,
	secrets *Secrets,
) ([]byte, error) {
	signKey, err := GetPGPEntity(ctx, secrets)
	if err != nil {
		return nil, fmt.Errorf("get pgp entity: %w", err)
	}

	return CreateDetachedSignatureByEntity(fileContent, signKey)
}

// createDetachedSignature takes the raw bytes of a file and a GPG entity,
// and returns the raw bytes of an armored, detached signature.
func CreateDetachedSignatureByEntity(
	fileContent []byte,
	signKey *openpgp.Entity,
) ([]byte, error) {
	// Create a buffer to hold the armored signature
	sigBuf := new(bytes.Buffer)
	armorWriter, err := armor.Encode(sigBuf, openpgp.SignatureType, nil)
	if err != nil {
		return nil, err
	}

	// Generate the detached signature from the file content
	err = openpgp.DetachSign(armorWriter, signKey, bytes.NewReader(fileContent), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create detached signature: %w", err)
	}
	armorWriter.Close()

	return sigBuf.Bytes(), nil
}
