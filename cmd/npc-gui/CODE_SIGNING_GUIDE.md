# Code Signing Guide for Windows Deployment

## Why Code Signing is Critical

Code signing is **essential** for Windows deployment to:
1. Prevent Windows SmartScreen warnings
2. Establish publisher identity and trust
3. Reduce false positives from antivirus software
4. Enable Microsoft Store distribution (if desired)
5. Provide tamper detection

## Obtaining a Code Signing Certificate

### Option 1: Standard Code Signing Certificate (~$100-300/year)
**Recommended Providers:**
- [DigiCert](https://www.digicert.com/signing/code-signing-certificates)
- [Sectigo (Comodo)](https://sectigo.com/ssl-certificates-tls/code-signing)
- [GlobalSign](https://www.globalsign.com/en/code-signing-certificate)
- [Entrust](https://www.entrust.com/digital-security/certificate-solutions/products/digital-certificates/code-signing-certificates)

**What You Need:**
- Business verification (company registration documents)
- Identity verification (government ID)
- Business email address
- Phone verification

**Timeline:**
- Application: 1-2 hours
- Verification: 1-5 business days
- Certificate issuance: Same day after verification

### Option 2: EV Code Signing Certificate (~$300-500/year)
**Advantages:**
- **Immediate SmartScreen trust** (no reputation building needed)
- Higher level of validation
- USB token-based (more secure)

**Disadvantages:**
- More expensive
- Requires USB token (cannot be copied)
- Stricter verification requirements

**Recommended for:**
- Professional/commercial distribution
- High-volume downloads
- Enterprise deployments

## Signing Process

### Step 1: Install Certificate
If you have a `.pfx` file:
```powershell
# Import to certificate store (optional)
Import-PfxCertificate -FilePath "certificate.pfx" -CertStoreLocation Cert:\CurrentUser\My
```

### Step 2: Sign the Executable
```powershell
# Using signtool (part of Windows SDK)
# Download from: https://developer.microsoft.com/en-us/windows/downloads/windows-sdk/

# Basic signing
signtool sign /f "certificate.pfx" /p "password" /tr http://timestamp.digicert.com /td sha256 /fd sha256 npc-gui.exe

# With description and URL
signtool sign /f "certificate.pfx" /p "password" `
    /tr http://timestamp.digicert.com /td sha256 /fd sha256 `
    /d "NPC GUI - NPS Client Manager" `
    /du "https://github.com/Yourdaylight/jqhl" `
    npc-gui.exe

# Using certificate from store
signtool sign /n "Your Company Name" `
    /tr http://timestamp.digicert.com /td sha256 /fd sha256 `
    /d "NPC GUI - NPS Client Manager" `
    /du "https://github.com/Yourdaylight/jqhl" `
    npc-gui.exe
```

### Step 3: Verify Signature
```powershell
# Verify the signature
signtool verify /pa /v npc-gui.exe

# Expected output:
# Signature Index: 0 (Primary Signature)
# Hash of file (sha256): ...
# Signing Certificate Chain:
#   ...
# Successfully verified: npc-gui.exe
```

## Timestamp Servers

Always use a timestamp server. This ensures the signature remains valid even after the certificate expires.

**Recommended Timestamp Servers:**
```
http://timestamp.digicert.com
http://timestamp.sectigo.com
http://timestamp.globalsign.com
http://timestamp.comodoca.com
```

## Automated Signing in CI/CD

### GitHub Actions Example

```yaml
name: Build and Sign Windows Release

on:
  push:
    tags:
      - 'v*'

jobs:
  build-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.25'
      
      - name: Setup Node
        uses: actions/setup-node@v3
        with:
          node-version: '18'
      
      - name: Install Wails
        run: go install github.com/wailsapp/wails/v2/cmd/wails@latest
      
      - name: Build Application
        run: |
          cd cmd/npc-gui
          wails build -platform windows/amd64
      
      - name: Decode Certificate
        run: |
          echo "${{ secrets.WINDOWS_CERTIFICATE_BASE64 }}" | base64 --decode > certificate.pfx
      
      - name: Sign Executable
        run: |
          & "C:\Program Files (x86)\Windows Kits\10\bin\10.0.22621.0\x64\signtool.exe" sign `
            /f certificate.pfx `
            /p "${{ secrets.WINDOWS_CERTIFICATE_PASSWORD }}" `
            /tr http://timestamp.digicert.com `
            /td sha256 `
            /fd sha256 `
            /d "NPC GUI - NPS Client Manager" `
            /du "https://github.com/Yourdaylight/jqhl" `
            cmd/npc-gui/build/bin/npc-gui.exe
      
      - name: Remove Certificate
        if: always()
        run: Remove-Item certificate.pfx -ErrorAction SilentlyContinue
      
      - name: Create Release
        uses: softprops/action-gh-release@v1
        with:
          files: cmd/npc-gui/build/bin/npc-gui.exe
```

### Storing Certificate in GitHub Secrets

1. Convert certificate to base64:
```powershell
[Convert]::ToBase64String([IO.File]::ReadAllBytes("certificate.pfx")) | Out-File certificate.txt
```

2. Add to GitHub Secrets:
   - Go to repository Settings → Secrets and variables → Actions
   - Add `WINDOWS_CERTIFICATE_BASE64` with the base64 content
   - Add `WINDOWS_CERTIFICATE_PASSWORD` with the certificate password

## Without Code Signing Certificate

If you cannot obtain a certificate immediately:

### Short-term Mitigations:
1. **Documentation**: Include clear README explaining the software purpose
2. **Source Code**: Link to GitHub repository in all documentation
3. **Checksums**: Provide SHA256 hashes for verification
4. **VirusTotal**: Upload to VirusTotal and share results
5. **User Instructions**: Document how to add Windows Defender exclusions

### User Instructions Template:
```markdown
## Windows SmartScreen Warning

If you see a "Windows protected your PC" message:

1. Click "More info"
2. Click "Run anyway"

This warning appears because the application is not yet code-signed. 
We are working on obtaining a code signing certificate.

### Adding Windows Defender Exclusion

If Windows Defender flags the application:

1. Open Windows Security (search in Start menu)
2. Go to "Virus & threat protection"
3. Click "Manage settings" under "Virus & threat protection settings"
4. Scroll to "Exclusions" and click "Add or remove exclusions"
5. Click "Add an exclusion" → "File"
6. Select npc-gui.exe

**Why is this safe?**
- Source code is available: https://github.com/Yourdaylight/jqhl
- No malicious code (review the source)
- False positive due to lack of code signature
```

## Best Practices

1. **Always Timestamp**: Use timestamp servers for long-term validity
2. **SHA256**: Use SHA256 algorithm (not SHA1)
3. **Dual Sign**: Consider dual signing (SHA1 + SHA256) for Windows 7 compatibility
4. **Test**: Verify signature after signing
5. **Secure Storage**: Store certificates securely, never commit to repository
6. **Rotate**: Renew certificates before expiration
7. **Document**: Keep records of signing certificates used

## Certificate Management

### Security:
- Store `.pfx` files encrypted
- Use strong passwords
- Never share private keys
- Use hardware tokens for EV certificates
- Rotate passwords regularly

### Backup:
- Keep backup of certificate and password
- Store in encrypted, secure location
- Have backup person with access (for teams)

### Renewal:
- Set reminders 60 days before expiration
- Test new certificate before old expires
- Update automation with new certificate

## Verification Commands

```powershell
# Check signature
signtool verify /pa /v npc-gui.exe

# View certificate details
Get-AuthenticodeSignature npc-gui.exe | Format-List *

# Check timestamp
signtool verify /pa /tw npc-gui.exe
```

## Troubleshooting

### "SignTool Error: No certificates were found"
- Ensure certificate is imported to correct store
- Use `/f` parameter to specify file directly

### "SignTool Error: The specified timestamp server failed"
- Try different timestamp server
- Check internet connection
- Timestamp server may be temporarily down

### Signature appears invalid
- Ensure certificate is valid (not expired)
- Check timestamp
- Verify certificate chain is complete

## Cost Considerations

**Annual Costs:**
- Standard Certificate: $100-300/year
- EV Certificate: $300-500/year
- Certificate renewal: Same as initial cost

**One-time Costs:**
- USB token (EV certs): Usually included
- Windows SDK: Free

**ROI:**
- Prevents user friction
- Reduces support burden
- Enables wider distribution
- Professional appearance

## Recommended: Start with Standard Certificate

For initial release, a standard code signing certificate is sufficient. Consider upgrading to EV if:
- High download volume (>1000/month)
- Enterprise customers
- Budget allows
- Immediate trust needed

## Resources

- [Microsoft: Code Signing](https://docs.microsoft.com/en-us/windows/win32/seccrypto/cryptography-tools)
- [SignTool Documentation](https://docs.microsoft.com/en-us/windows/win32/seccrypto/signtool)
- [Windows SDK Download](https://developer.microsoft.com/en-us/windows/downloads/windows-sdk/)
- [VirusTotal](https://www.virustotal.com/)

## Next Steps

1. ✅ Review this guide
2. ⬜ Choose certificate provider
3. ⬜ Gather required documents
4. ⬜ Apply for certificate
5. ⬜ Install certificate
6. ⬜ Sign test build
7. ⬜ Verify signature
8. ⬜ Set up CI/CD signing
9. ⬜ Sign release builds
10. ⬜ Test on clean Windows installation
