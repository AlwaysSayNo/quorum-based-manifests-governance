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

  locations:
    governancePath: manifest-signing-requests

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
          -----BEGIN PGP PUBLIC KEY BLOCK-----
          Version: Keybase OpenPGP v1.0.0
          Comment: https://keybase.io/crypto

          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
          csK4fdfSR9+e
          =2Og1
          -----END PGP PUBLIC KEY BLOCK-----
      - alias: voter1
        publicKey: |
          -----BEGIN PGP PUBLIC KEY BLOCK-----
          Version: Keybase OpenPGP v1.0.0
          Comment: https://keybase.io/crypto

          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
          tZZrsaV/6CsqSsY=
          =a/zo
          -----END PGP PUBLIC KEY BLOCK-----
      - alias: voter2
        publicKey: |
          -----BEGIN PGP PUBLIC KEY BLOCK-----

          mQENBGlWd9EBCADQWJ8nDEf9YGJEj0Maj4rBxd0xvAZ4OleWae+MBqTPVqKhF60/
          ze3dPKadE+4UehuAZC00NX+sfwG7Xk4Nf26MZ4+JDIHVwSQcAtOobTe4zrSIJHiG
          wiiUMrgCds94+MV/Lx3tWnCq8AkFgaDwYMyzuhg5UcDU5D7cn1eCcDZFbABBUdL6
          GgbsAmmGCPPdy3vtKF2Z8RHt5OzOXHAj6fe7zO1u/RLCWt0rVCImMgVtaiSd5ECL
          1OvoeGRJch7Gi9abU/FND13BuBHrN0iQVbF9MGl4hGl+r7frL7lZUMaDPSw5Nzpc
          NtJplq+4YFY8QkMYUbKDQYxSUgwC0OI+Pdo9ABEBAAG0G3ZvdGVyMiA8dm90ZXIy
          QGV4YW1wbGUuY29tPokBTgQTAQoAOBYhBBVItCGKPBXcj9RofC71d6/VTwkZBQJp
          VnfRAhsDBQsJCAcCBhUKCQgLAgQWAgMBAh4BAheAAAoJEC71d6/VTwkZahcH/inf
          2TGMZnvBBlthXCmFGpqQ1FY0MSGLmi8WIMup/C8B1DU9YdkQ5Avmal/9RmVzuEXE
          7q6wBp9m0l6P/MO3WGm9CjtrOlTkcroMhGA8aM4kIPBdJMy76PeiF3tOnlHuIBoe
          3V+k0Q8MIfpSxNm70ifZvvnYSBEcsinTojSQ96nFrl6+wwLPSf2kRtDPLbnb7oSe
          XEVkJsydgWWPMXClnFpBL0/NXucecXFUfnQCG5vczwi8/zgZR7Rpchp/RP1yninr
          xUPqLoan5dg1eAJlhIap/WTrIE9SOlfxMyu+UF52zWyNrZOopbaW1YvP9A9hC1QX
          KE/w16tKuLd3hPB75be5AQ0EaVZ30QEIANcL/t0qCEFp+e264+aL/ciDkVcIzbqB
          pmgoVNgWnMG7VW8LzKFfEiMCWmpc7AlT5tF0BzNgXC0n7de8Bgx4ZBvJi0Abd4ny
          D8qFEvokdB9KyAJfcHjmvbW73HgKiVyCluewc/LvB/DXMt5JlcszHiysOg+PbuDy
          QJvoQTZw0TseGJlVE9hELzxQ/8Hy8lK1W1T19hmJ6fRqOAz+mwT9L4eRNu4uk4PD
          Q558lV9jDHhZu8rec761ZAxGmVzAf5gx0RroDegNbz3elrmH3LLSnyH1M1ZVupzE
          /obQXS1ratzg2lhgBOV46WN3MrePXUIU8+Pauxab+8kEecz66eba0EEAEQEAAYkB
          NgQYAQoAIBYhBBVItCGKPBXcj9RofC71d6/VTwkZBQJpVnfRAhsMAAoJEC71d6/V
          TwkZ2l8H/2TGnORTYEKpJjgF4d9kJ5mXFW36JO/72alQsYRp2WawaU9b6uhgiXTB
          jWGHEwTEty73l4kQCdKUbT3ghBxKSda2ZcigTKffs66AP163abYuvQU+Pba1VtGW
          qNL3e4sjYod1PHWC80Maxe0tRbY8w4HvHZwNRzqU3KVXXuMTxpZ04wLQf3/sD928
          MF4stL1vuhl3ZO0FGNrv88vS+O5bwEZ7Y5lIYgTlo8bhA+d2n/9ZAknzQIyfL861
          wq1/aCJtNe3eqAqio2BZvp3fy92RWbgm4WR2PBKwDa14hjpJb4m+zfXbCSFvcmiM
          qZFJ8yB6vi9xpphaaefJBkTbd/Sw+Hs=
          =bfc6
          -----END PGP PUBLIC KEY BLOCK-----
      - alias: voter3
        publicKey: |
          -----

  require:
    all: true
    require:
      - signer: owner
      - atLeast: 1
        require:
          - signer: voter1
          - signer: voter2
          - signer: voter3" > app-manifests/mrt.yaml

sed -i'' -e "s|#{REPO_URL}#|$ssh_repo_url|g" app-manifests/mrt.yaml

git add .
git commit -m "Create dummy MRT"
git push origin main
echo "Pushed new change to GitHub. Argo CD will now sync."