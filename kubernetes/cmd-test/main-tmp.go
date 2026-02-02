package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"

	repomanager "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository"
	githubprovider "github.com/AlwaysSayNo/quorum-based-manifests-governance/kubernetes/internal/repository/github"
)

const (
	RepoURL  = "git@github.com:AlwaysSayNo/quorum-based-manifests-governance-test.git"
	BasePath = "/tmp/git"
)

var (
	PGPPrivateKey = `-----BEGIN PGP PRIVATE KEY BLOCK-----

lQIGBGk8QpABBADnkX2Cb4AkBhM3iL2rWNi5bxcMF1EUNJIDK6voruVQkuwgRhJg
1YuE/f1deC9evRIGw0zY9f7Lpv6gkX3rkUpgA2z5VWcF+NPZY58nTMLsgIk6KDIz
BYBa8Nxj/GONC7Zpr9V+pyKzRAO+GiFlW8YytbW2Bo/FX/A1Hs7kWqz5GwARAQAB
/gcDAhEcPNQv3NGn/7etkgEsLO6Kr9zCwWUbwZriWkJOUe2zEnWgirGHUfpLIzC6
oWhULyVlyPu8cxx/9q+DgpGa0g2JZEeOjhsm0WcNo9ITMDDrGnQftppagNG6pNTI
yoB+vpWYRADOKPlz8+Z28ueS/o/MccG4XKieRZs7ttVljWdGuuUpTAnFps/dW/E6
ADl9ftZEVQxM29voRfoVMMavqDBvccGXXDCz55a+I8rwHmAGejS4DoQBEqPG5uiq
rQbGJ4IHdRjcRUKdclbVVhmgyCyJHsytaTOS/3gXf8lNPtUpfejEGPEvX7NXt5kB
uLVq1x1ECmoSVwDo9gyp1+j8rM5JvhVr9CRGYBfG1Gi5nLAWcYsyyat2nrBk+Mg5
b3aVPLhrdeKmlpXyYA0qtvckX1dfE/aMQ3klx80Jz4GQYFVLVkHqwcsj9x6d9qDV
hIT1h3ey1LxRobUTaoO+DFCjA2nmGTEBGDcnnDiv0YTbrHrvmgZRzHa0I1Rlc3Qg
VGVzdCA8dGVzdC5wcm90ZWN0ZWRAdGVzdC5jb20+iM4EEwEKADgWIQSyrYy43tPA
BRmg6FZBWa/Rb9utogUCaTxCkAIbAwULCQgHAgYVCgkICwIEFgIDAQIeAQIXgAAK
CRBBWa/Rb9utomLSBACMgBLT0mp4QBw/p/GpjEirq03zmqZuWMaz+MLJ6vw5eTUU
sSP/RkKt6bOlrJOJN/on2kFou7iLiTAuBWkDgoW8vzIV3dkAnEfgolKCi6oeBSKm
FSXbinGAvi88VQQibevjvWdmDFJ6UPXD7xGE4vxOzNV2oxMhWGfJmtbqaoCxW50C
BgRpPEKQAQQAyTyCSyoE14vMz7s9e0+cChTMuSrJsYMZrRi1LvjbVR4quGwFIyT7
PqRZXt8BgmQ4HxgFXkhKDXBzO3oy0+zjPDa3ky8KsklQez0NBwu2OefXIZCzmD2J
Bndok7AFhlyuctAHWu8bFmefX9gojjNB13m4NOpS/2sTSTUXTsJqEJsAEQEAAf4H
AwKQkaWqAzXbPv94Om/IAXVWKtIsW6ILpGw7EdZPvB8c3XNjUwEM6iOLvHwowv47
QGi++l9Wx2JkGE6ZsdCyFmIFJHgRfaFr80SqkhKWRyejZ/vUNbSrawjzaindj0mG
1gmCrKX8+UyJNSbCy16NE5f10heqpoxn2jHxefiXGAN5rVljG/P6ptd6PUSQ2kjN
YN0GwLqx3vgfWOzhrEa2uG1UhjRcj47siIRdwI1zk8o7RZd4BQL76NXIqYe5QtN+
5WQlEf10NEUHU+MNBz01C594feRhR3SnXlgLaMFK4biuYt8VzRMEGDNoBqRswEMy
F4V/GgNHS9TdRSdenadZRg/k0sOpdr4TFr1Y7dc8R+2HEW46PU9l2BWcUJs7o59W
aiUDVg5e3GukU6KeWGJiDZc8NgyEOKFfXiEs38nsZDewy5hpHBLzrLd2B/pBWqJt
CbV8eLUFTmpZPWF7hYIGQ5iksnwvwakPRSF4ZxH/6IivtI9EpsUhiLYEGAEKACAW
IQSyrYy43tPABRmg6FZBWa/Rb9utogUCaTxCkAIbDAAKCRBBWa/Rb9utokGlBACv
EMqmg6DrxNPfmV9PcnkptH9e145NAIEW6iV4JaD2tEYelWdTti/+fjlVKuYRDwt1
238OrM+X+51NiryUWvbo/yX0AaSe3WrLrKl8tYEAuxOx6L1Xfm6vBsLZ3jx+UPP5
BgefsmG//mihRnTv9bzSQLpXitA6VU2LzkfVTH+Yxg==
=60zY
-----END PGP PRIVATE KEY BLOCK-----`
	PGPPassphrase = "password"

	SSHPrivateKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABD1Y7FqfS
sRTe9VhGDkxf5GAAAAGAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAIKnAN8hG5AQyUIhp
3hLPVxYBM89hhObI8/+NG8UVbqAjAAAAoMLeujFmvvPIVTY5SOpCSBbG9bWbvZMRrp6wOA
AqjXTl6xhMYUUyB8LPyo9h5ITziNHA+ZHgx97DD3RYaDoek49ikFYK/2uCjKXwk/gSgBhd
0pMjULI8y0CUmDy2bcHsVvIEu1SnbKGdbj6Vs3ju1faxU0glhuyPa2QQPFy279lUE4k3j3
l4lAZk8RG+szStDZs8V3zAx3YUZD0Vn/zbReI=
-----END OPENSSH PRIVATE KEY-----`
	SSHPassphrase = "Account 123"
)

var (
	repoProvider        repomanager.GitRepository
	repoProviderFactory githubprovider.GitHubProviderFactory
)

func main() {
	ctx := context.TODO()
	gitRepo, err := GetProviderForMRT(ctx)
	if err != nil {
		return
	}
	fmt.Print(gitRepo.GetLatestRevision(ctx))
}

func GetProviderForMRT(ctx context.Context) (repomanager.GitRepository, error) {
	repoURL := RepoURL
	if repoURL == "" {
		return nil, fmt.Errorf("repository URL is not defined in MRT spec")
	}

	// Generate unique local path for the clone from the repo URL
	repoHash := fmt.Sprintf("%x", sha1.Sum([]byte(repoURL)))
	localPath := filepath.Join(BasePath, repoHash)

	// Sync pgp and ssh secrets
	pgpSecrets, err := syncPGPSecrets(ctx)
	if err != nil {
		return nil, err
	}
	sshSecrets, err := syncSSHSecrets(ctx)
	if err != nil {
		return nil, err
	}

	provider, err := findProvider(ctx, repoURL, localPath, sshSecrets, pgpSecrets)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

func findProvider(ctx context.Context, repoURL, localPath string, auth transport.AuthMethod, pgpSecrets repomanager.PgpSecrets) (repomanager.GitRepository, error) {
	return repoProviderFactory.New(ctx, repoURL, localPath, auth, pgpSecrets)
}

func syncPGPSecrets(ctx context.Context) (repomanager.PgpSecrets, error) {
	privateKeyBytes := []byte(PGPPrivateKey)
	passphraseBytes := []byte(PGPPassphrase)

	return repomanager.PgpSecrets{
		PrivateKey: string(privateKeyBytes),
		Passphrase: string(passphraseBytes),
	}, nil
}

func syncSSHSecrets(ctx context.Context) (*ssh.PublicKeys, error) {
	privateKeyBytes := []byte(SSHPrivateKey)
	passphraseBytes := []byte(SSHPassphrase)

	publicKeys, err := ssh.NewPublicKeys("git", privateKeyBytes, string(passphraseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create public keys from secret: %w", err)
	}

	return publicKeys, nil
}
