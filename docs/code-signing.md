# Code signing

Authenticode signing for the Windows release binaries. Currently a no-op
scaffold: `.github/workflows/release.yml` has a `sign` job that is skipped
until you configure the Azure Key Vault secrets. This doc is for the
maintainer who flips the switch.

## Why

Modern Windows treats unsigned executables as suspect. Microsoft Defender
Exploit Guard ASR (Attack Surface Reduction) rules — common on corporate
machines — block unsigned binaries from running outright. The user sees
"Access is denied" or a silent failure with nothing useful in the event
log. We hit this on Deloitte-managed laptops with the v0.0.2 binary.

Signing the exe with an Authenticode certificate plus an RFC 3161
timestamp gets us into the trusted bucket. SmartScreen reputation still
takes a few hundred installs to warm up, but the hard ASR block goes
away immediately.

## Two paths

### 1. SignPath OSS sponsorship (recommended, free)

[SignPath](https://about.signpath.io/oss) sponsors open-source projects
with a managed Authenticode certificate backed by an Azure Key Vault. No
hardware token, no per-year renewal, no $300+ cost.

1. Apply at <https://about.signpath.io/oss>. Approval is usually a few days.
2. SignPath provisions a project and gives you the five values below.
3. Add them to the repo as GitHub Actions secrets
   (Settings → Secrets and variables → Actions → New repository secret):

   | Secret name             | What it is                                       |
   | ----------------------- | ------------------------------------------------ |
   | `AZURE_KEY_VAULT_URL`   | e.g. `https://signpath-localmind.vault.azure.net` |
   | `AZURE_TENANT_ID`       | Azure AD tenant GUID                             |
   | `AZURE_CLIENT_ID`       | Service principal app GUID                       |
   | `AZURE_CLIENT_SECRET`   | Service principal secret                         |
   | `AZURE_CERT_NAME`       | Cert name inside the vault, e.g. `localmind`     |

4. Cut the next release as normal (`git tag vX.Y.Z && git push --tags`).
   The `sign` job now matches its `if:` gate and runs after `release`.

### 2. Self-funded Authenticode cert

Buy a standard (not EV) code-signing cert from Sectigo, DigiCert, or
similar. ~$300–$600/year. EV certs warm SmartScreen faster but need a
hardware token, which doesn't fit a CI workflow without extra plumbing.

Once issued, import the cert into a fresh Azure Key Vault and create a
service principal with `get`/`sign` permissions on the certificate. Set
the same five secrets above. The workflow doesn't care which path you
took.

## What the workflow does

The `sign` job in `.github/workflows/release.yml`:

1. Downloads the artifacts the `release` job uploaded.
2. Installs [AzureSignTool](https://github.com/vcsjones/AzureSignTool)
   via `dotnet tool install --global AzureSignTool`.
3. Unpacks `localmind-windows-amd64.zip` and `localmind-windows-arm64.zip`,
   signs each `localmind.exe`, repacks.
4. Refreshes `checksums.txt` (the signed exe has a different hash).
5. Re-uploads the three changed files to the GitHub Release with
   `gh release upload --clobber`.

Release URLs and `install.ps1` don't change. Existing users with the
unsigned v0.0.x binaries are unaffected; the next tag they pull will be
signed.

## Verification

After a signed release is up, on a Windows host:

```powershell
# Download and verify
iwr https://github.com/bsaisuryacharan/localmind/releases/download/vX.Y.Z/localmind-windows-amd64.zip -OutFile lm.zip
Expand-Archive lm.zip -DestinationPath lm
Get-AuthenticodeSignature lm\localmind.exe
```

Expected output: `Status: Valid`, `SignerCertificate` showing SignPath /
DigiCert / your CA. If `Status` is `NotSigned`, the gate didn't fire —
check that all five secrets are set and the job ran in the workflow log.

## macOS notarization

Not in scope for v0.0.x. macOS Gatekeeper currently warns on the
unsigned binary; users override with `xattr -d com.apple.quarantine`.
A future change will add a `notarize` job using `notarytool`, gated on
`APPLE_DEVELOPER_ID_CERT` the same way. Requires an Apple Developer
Program membership (~$99/year).
