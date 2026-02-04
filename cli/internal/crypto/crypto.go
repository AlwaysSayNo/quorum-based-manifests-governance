package crypto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
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

	if encrypted, _ := IsSSHKeyEncrypted(secrets.PrivateKey); encrypted && secrets.Passphrase == "" {
		return nil, fmt.Errorf("SSH private key is encrypted, but no passphrase was provided")
	}

	// Decrypt private key
	return DecryptSSHKey(secrets)
}

func IsSSHKeyEncrypted(privateKeyStr string) (bool, error) {
	privateKeyBytes := []byte(privateKeyStr)

	_, err := cryptossh.ParsePrivateKey(privateKeyBytes)

	// The key is valid and not encrypted.
	if err == nil {
		return false, nil
	}

	// The key is encrypted if PassphraseMissingError.
	var passphraseMissingErr *cryptossh.PassphraseMissingError
	if errors.As(err, &passphraseMissingErr) {
		return true, nil
	}

	//The key is invalid.
	return false, err
}

func DecryptSSHKey(
	secrets *Secrets,
) (*ssh.PublicKeys, error) {
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

func CheckSSHKeyNeedsPassphrase(path string) (bool, error) {
	privateKey, err := readPrivateKeyFile(path)
	if err != nil {
		return false, err
	}

	encrypted, err := IsSSHKeyEncrypted(privateKey)
	if err != nil {
		return false, err
	}

	return encrypted, nil
}

func CheckPGPKeyNeedsPassphrase(path string) (bool, error) {
	privateKey, err := readPrivateKeyFile(path)
	if err != nil {
		return false, err
	}

	entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(privateKey))
	if err != nil {
		return false, err
	}

	if len(entityList) == 0 {
		return false, fmt.Errorf("no PGP entity found in key file")
	}

	entity := entityList[0]
	return IsPGPKeyEncrypted(entity), nil
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

	// If a passphrase is required but not provided, fail.
	if IsPGPKeyEncrypted(entity) && secrets.Passphrase == "" {
		return nil, fmt.Errorf("PGP private key is encrypted, but no passphrase was provided")
	}

	// Decrypt private key
	if err := DecryptPGPKey(entity, secrets.Passphrase); err != nil {
		return nil, fmt.Errorf("decrypt PGP private key: %w", err)
	}

	return entity, nil
}

func IsPGPKeyEncrypted(
	entity *openpgp.Entity,
) bool {
	// Check if private key or any subkey is encrypted
	needsPass := (entity.PrivateKey != nil && entity.PrivateKey.Encrypted)
	if !needsPass {
		for _, sk := range entity.Subkeys {
			if sk.PrivateKey != nil && sk.PrivateKey.Encrypted {
				needsPass = true
				break
			}
		}
	}

	return needsPass
}

func DecryptPGPKey(
	entity *openpgp.Entity,
	passphraseStr string,
) error {
	passphrase := []byte(passphraseStr)

	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		// Attempt to decrypt the private key.
		err := entity.PrivateKey.Decrypt(passphrase)
		if err != nil || entity.PrivateKey.Encrypted {
			return fmt.Errorf("failed to decrypt PGP private key: %w", err)
		}
	}

	for i := range entity.Subkeys {
		if entity.Subkeys[i].PrivateKey != nil && entity.Subkeys[i].PrivateKey.Encrypted {
			if err := entity.Subkeys[i].PrivateKey.Decrypt(passphrase); err != nil {
				return fmt.Errorf("failed to decrypt subkey: %w", err)
			}
		}
	}

	return nil
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
		armorWriter.Close()
		return nil, fmt.Errorf("failed to create detached signature: %w", err)
	}

	if err := armorWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize signature: %w", err)
	}

	return sigBuf.Bytes(), nil
}

func ConvertPublicKeyToHash(
	pgpEntity *openpgp.Entity,
) (string, error) {
	// Encode the public key to raw bytes
	pubKeyBuf := new(bytes.Buffer)
	err := pgpEntity.PrimaryKey.Serialize(pubKeyBuf)
	if err != nil {
		return "", fmt.Errorf("failed to serialize public key: %w", err)
	}

	// Hash the public key bytes
	hasher := sha256.New()
	hasher.Write(pubKeyBuf.Bytes())

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
