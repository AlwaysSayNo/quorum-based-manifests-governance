#!/usr/bin/env bash

ssh_repo_url=$1
repo_name=$2

if [ -z "$ssh_repo_url" ]; then
    echo "Usage: $0 <git-repo-ssh-url>"
    exit 1
elif [ -z "$repo_name" ]; then
    echo "Usage: $0 <git-repo-ssh-url> <git-repo-name>"
    exit 1
fi

cd ./repos/$repo_name

if [[ -n $(git status --porcelain) ]]; then
    echo "Error: local repository has uncommitted changes. Please commit or stash them first."
    exit 1
fi

# Pull changes to avoid conflicts
git pull origin main

# Make a dummy change to create MRT
echo "apiVersion: governance.nazar.grynko.com/v1alpha1
kind: ManifestRequestTemplate

metadata:
  name: mrt-my
  namespace: test-ns

spec:
  version: 1
  
  gitRepository:
    sshUrl: #{REPO_URL}#

  pgp:
    publicKey: |
      -----BEGIN PGP PUBLIC KEY BLOCK-----

      mI0EaTxCkAEEAOeRfYJvgCQGEzeIvatY2LlvFwwXURQ0kgMrq+iu5VCS7CBGEmDV
      i4T9/V14L169EgbDTNj1/sum/qCRfeuRSmADbPlVZwX409ljnydMwuyAiTooMjMF
      gFrw3GP8Y40Ltmmv1X6nIrNEA74aIWVbxjK1tbYGj8Vf8DUezuRarPkbABEBAAG0
      I1Rlc3QgVGVzdCA8dGVzdC5wcm90ZWN0ZWRAdGVzdC5jb20+iM4EEwEKADgWIQSy
      rYy43tPABRmg6FZBWa/Rb9utogUCaTxCkAIbAwULCQgHAgYVCgkICwIEFgIDAQIe
      AQIXgAAKCRBBWa/Rb9utomLSBACMgBLT0mp4QBw/p/GpjEirq03zmqZuWMaz+MLJ
      6vw5eTUUsSP/RkKt6bOlrJOJN/on2kFou7iLiTAuBWkDgoW8vzIV3dkAnEfgolKC
      i6oeBSKmFSXbinGAvi88VQQibevjvWdmDFJ6UPXD7xGE4vxOzNV2oxMhWGfJmtbq
      aoCxW7iNBGk8QpABBADJPIJLKgTXi8zPuz17T5wKFMy5KsmxgxmtGLUu+NtVHiq4
      bAUjJPs+pFle3wGCZDgfGAVeSEoNcHM7ejLT7OM8NreTLwqySVB7PQ0HC7Y559ch
      kLOYPYkGd2iTsAWGXK5y0Ada7xsWZ59f2CiOM0HXebg06lL/axNJNRdOwmoQmwAR
      AQABiLYEGAEKACAWIQSyrYy43tPABRmg6FZBWa/Rb9utogUCaTxCkAIbDAAKCRBB
      Wa/Rb9utokGlBACvEMqmg6DrxNPfmV9PcnkptH9e145NAIEW6iV4JaD2tEYelWdT
      ti/+fjlVKuYRDwt1238OrM+X+51NiryUWvbo/yX0AaSe3WrLrKl8tYEAuxOx6L1X
      fm6vBsLZ3jx+UPP5BgefsmG//mihRnTv9bzSQLpXitA6VU2LzkfVTH+Yxg==
      =7D/e
      -----END PGP PUBLIC KEY BLOCK-----
    secretsRef:
      name: pgp-secret-my
      namespace: test-ns

  ssh:
    secretsRef:
      name: ssh-secret-my
      namespace: test-ns

  argoCDApplication:
    name: application-my
    namespace: argocd

  location:
    folder: manifest-signing-requests

  msr:
    name: msr-my
    namespace: test-ns
  
  mca:
    name: mca-my
    namespace: test-ns

  governors:
    notificationChannels:
      - slack:
          channelID: C87654321
    members:
      - alias: owner 
        publicKey: |
          -----
      - alias: voter1
        publicKey: |
          -----
      - alias: voter2
        publicKey: |
          -----

  require:
    all: true
    signer: owner
    require:
      - atLeast: 2
        require:
          - signer: voter1
          - signer: voter2" > app-manifests/mrt.yaml

sed -i'' -e "s|#{REPO_URL}#|$ssh_repo_url|g" app-manifests/mrt.yaml

git add .
git commit -m "Create dummy MRT"
git push origin main
echo "Pushed new change to GitHub. Argo CD will now sync."