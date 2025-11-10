# Windows Deployment Checklist

## Pre-Release Checklist

### ✅ Code Quality & Security

- [x] Code review completed
- [x] No hardcoded credentials
- [x] No obfuscated code
- [x] Proper error handling
- [x] Clean shutdown procedures
- [x] No suspicious API calls
- [x] No registry modifications (except optional service install)
- [x] No system file modifications
- [x] User-initiated actions only
- [x] Transparent operations

### ✅ Application Metadata

- [x] Application manifest with trustInfo
- [x] Version information resource (info.json)
- [x] Professional icon (multiple resolutions)
- [x] Clear file description
- [x] Copyright information
- [x] Product name and version

### ⬜ Code Signing (Critical)

- [ ] Obtain code signing certificate
  - [ ] Choose provider (DigiCert, Sectigo, GlobalSign, Entrust)
  - [ ] Submit application with required documents
  - [ ] Complete identity verification
  - [ ] Receive certificate
- [ ] Install certificate securely
- [ ] Sign executable with timestamp
- [ ] Verify signature
- [ ] Test on clean Windows installation

### ✅ Documentation

- [x] README with clear purpose
- [x] Build instructions
- [x] Usage guide
- [x] Quick start guide
- [x] Development guide
- [x] Windows security review
- [x] Code signing guide
- [ ] Privacy policy
- [ ] Terms of use
- [ ] FAQ about Windows Defender

### ⬜ Testing

- [ ] Test on Windows 10
- [ ] Test on Windows 11
- [ ] Test with Windows Defender active
- [ ] Test installation process
- [ ] Test uninstallation
- [ ] Test update process (if applicable)
- [ ] Test all connection types (TCP, TLS, KCP, QUIC, WS, WSS)
- [ ] Test configuration save/load
- [ ] Test log viewing
- [ ] Test start/stop client

### ⬜ Distribution

- [ ] Create checksums (SHA256)
- [ ] Upload to VirusTotal
- [ ] Address any false positives
- [ ] Submit to major AV vendors if needed
- [ ] Create GitHub release
- [ ] Include signature verification instructions
- [ ] Provide download links
- [ ] Update documentation with release notes

### ⬜ Support

- [ ] Set up issue tracking
- [ ] Create support documentation
- [ ] Document common issues
- [ ] Prepare Windows Defender exclusion guide
- [ ] Create FAQ for antivirus warnings

## Release Process

### 1. Build

```bash
cd cmd/npc-gui
wails build -platform windows/amd64
```

### 2. Sign

```powershell
signtool sign /f certificate.pfx /p password `
    /tr http://timestamp.digicert.com /td sha256 /fd sha256 `
    /d "NPC GUI - NPS Client Manager" `
    /du "https://github.com/Yourdaylight/jqhl" `
    build/bin/npc-gui.exe
```

### 3. Verify

```powershell
signtool verify /pa /v build/bin/npc-gui.exe
```

### 4. Create Checksum

```powershell
certutil -hashfile build/bin/npc-gui.exe SHA256 > npc-gui.exe.sha256
```

### 5. Test

- [ ] Run on clean Windows VM
- [ ] Verify signature shows correctly
- [ ] Test all features
- [ ] Check for SmartScreen warnings
- [ ] Scan with Windows Defender

### 6. Upload to VirusTotal

```
https://www.virustotal.com/gui/home/upload
```

### 7. Create Release

- [ ] Create Git tag
- [ ] Generate release notes
- [ ] Upload signed binary
- [ ] Upload checksum file
- [ ] Include VirusTotal link
- [ ] Update documentation

### 8. Announce

- [ ] Update README
- [ ] Post in discussions
- [ ] Notify users
- [ ] Update website (if applicable)

## Post-Release Monitoring

### Week 1
- [ ] Monitor for false positive reports
- [ ] Check download statistics
- [ ] Monitor issue tracker
- [ ] Respond to user feedback
- [ ] Address any AV flagging

### Week 2-4
- [ ] Review usage patterns
- [ ] Collect feedback
- [ ] Plan updates
- [ ] Monitor security reports

### Ongoing
- [ ] Keep dependencies updated
- [ ] Monitor for vulnerabilities
- [ ] Plan feature updates
- [ ] Maintain code signing certificate
- [ ] Renew certificate before expiration

## Antivirus Submission Contacts

If flagged as malicious, submit to:

### Microsoft Defender
https://www.microsoft.com/en-us/wdsi/filesubmission

### Symantec
https://submit.symantec.com/

### McAfee
https://www.mcafee.com/enterprise/en-us/threat-center/submit-sample.html

### Kaspersky
https://www.kaspersky.com/about/contact

### AVG/Avast
https://www.avg.com/en-us/false-positive-file-form

### Bitdefender
https://www.bitdefender.com/consumer/support/answer/29358/

### ESET
https://support.eset.com/en/kb142-submit-a-false-positive-file-or-website

### Trend Micro
https://www.trendmicro.com/en_us/about/legal/detection-reevaluation.html

## Support Documentation

### For Users Getting Warnings

Create file `WINDOWS_WARNINGS.md`:

```markdown
# Windows Security Warnings

## SmartScreen Warning

### What is SmartScreen?
Windows SmartScreen is a security feature that warns about unrecognized applications.

### Why am I seeing this warning?
The warning appears because:
1. The application is new and building reputation
2. [If unsigned] The application is not code-signed

### Is it safe?
Yes! The application is safe because:
- Source code is open and available
- No malicious code (you can review it)
- Built with reputable framework (Wails)
- Transparent purpose (NPC client manager)

### How to run the application?
1. Click "More info"
2. Click "Run anyway"

## Windows Defender Warning

### Adding Exclusion
1. Open Windows Security
2. Go to "Virus & threat protection"
3. Click "Manage settings"
4. Scroll to "Exclusions"
5. Click "Add or remove exclusions"
6. Add npc-gui.exe

### Why the false positive?
- Network functionality (connects to NPS servers)
- [If unsigned] Lack of code signature
- New application without established reputation

## Verification

### Check File Hash
```powershell
certutil -hashfile npc-gui.exe SHA256
```

Compare with the hash provided in the release notes.

### Check Source Code
Visit: https://github.com/Yourdaylight/jqhl/tree/main/cmd/npc-gui

### VirusTotal Report
[Include link to VirusTotal scan in release notes]

## Still Concerned?

1. Review the source code
2. Build from source yourself
3. Contact us with questions
4. Check our security review: WINDOWS_SECURITY_REVIEW.md
```

## Critical Action Items

### Immediate (Before First Release)
1. ⚠️ **Obtain code signing certificate**
2. ⚠️ **Test on Windows 10 and 11**
3. ⚠️ **Create checksums**
4. ⚠️ **Upload to VirusTotal**

### Important (Within First Week)
1. Monitor for issues
2. Address false positives
3. Update documentation based on feedback
4. Set up automated builds with signing

### Recommended (Within First Month)
1. Build reputation (more downloads)
2. Consider EV certificate if needed
3. Create installer (optional)
4. Set up auto-update (optional)

## Success Metrics

- [ ] No Windows Defender false positives
- [ ] No SmartScreen warnings (with signed binary)
- [ ] Positive user feedback
- [ ] Clean VirusTotal scans
- [ ] No AV vendor flagging
- [ ] Smooth installation experience
- [ ] No critical bugs reported

## Notes

- **Code signing is the most important step**
- Test thoroughly before public release
- Respond quickly to false positive reports
- Keep documentation up to date
- Maintain good security practices
- Build trust through transparency

## Resources

- Windows Security Review: `WINDOWS_SECURITY_REVIEW.md`
- Code Signing Guide: `CODE_SIGNING_GUIDE.md`
- Build Instructions: `README.md`
- Development Guide: `DEVELOPMENT.md`

---

**Last Updated**: 2025-01-10
**Status**: Pre-release checklist
**Next Action**: Obtain code signing certificate
