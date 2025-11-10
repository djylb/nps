# Windows Deployment Security Review

## Overview
This document reviews the NPC GUI client application for Windows deployment compliance, ensuring it won't be flagged as malicious software by Windows Defender or other antivirus solutions.

## Code Review Summary

### ✅ Security Best Practices Implemented

#### 1. **Legitimate Application Structure**
- **Framework**: Uses Wails v2 (legitimate Go + Web framework)
- **Purpose**: Clear and legitimate - GUI for NPC client configuration
- **No Obfuscation**: Code is open-source and transparent
- **No Suspicious Behavior**: No keylogging, screen capturing, or unauthorized data collection

#### 2. **File Operations**
- **Config Storage**: `~/.npc-gui/configs.json` (user home directory)
- **Permissions**: Standard read/write permissions (0755, 0644)
- **No System Modifications**: Doesn't modify system files or registry unnecessarily
- **No Admin Required**: Runs with user-level permissions

#### 3. **Network Operations**
- **Purpose**: Legitimate - connects to NPS server for tunneling
- **User Control**: All connections are user-initiated
- **Transparent**: Server addresses configured by user
- **No C&C**: No command-and-control behavior

#### 4. **Process Behavior**
- **Single Process**: Main GUI application
- **Child Processes**: Only NPC client connections (user-initiated)
- **No Injection**: Doesn't inject code into other processes
- **Clean Shutdown**: Proper cleanup on exit

#### 5. **Data Handling**
- **User Data**: Only stores user-provided configuration
- **No Exfiltration**: Doesn't send data to unauthorized destinations
- **Local Storage**: Configuration stored locally
- **No Encryption of System Files**: Only encrypts network traffic (TLS)

### 📋 Recommendations for Windows Deployment

#### 1. **Code Signing (Critical)**
```powershell
# Obtain a code signing certificate from a trusted CA
# Sign the executable after building
signtool sign /f certificate.pfx /p password /tr http://timestamp.digicert.com /td sha256 /fd sha256 npc-gui.exe
```

**Why Important:**
- Prevents SmartScreen warnings
- Establishes publisher identity
- Required for Microsoft Store distribution
- Reduces false positives from antivirus software

**Recommended Certificate Providers:**
- DigiCert
- Sectigo (formerly Comodo)
- GlobalSign
- Entrust

#### 2. **Application Manifest**
Current manifest is good, but consider adding:
```xml
<!-- Add trustInfo section -->
<trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
        <requestedPrivileges>
            <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
        </requestedPrivileges>
    </security>
</trustInfo>
```

#### 3. **Version Information**
Add comprehensive version info resource:
```go
// In build configuration
var (
    CompanyName      = "Your Organization"
    FileDescription  = "NPC GUI - NPS Client Manager"
    FileVersion      = "1.0.0.0"
    InternalName     = "npc-gui"
    LegalCopyright   = "Copyright (C) 2025"
    OriginalFilename = "npc-gui.exe"
    ProductName      = "NPC GUI"
    ProductVersion   = "1.0.0"
)
```

#### 4. **Windows Defender Exclusions**
Document for users:
```markdown
If Windows Defender flags the application, add an exclusion:
1. Open Windows Security
2. Go to "Virus & threat protection"
3. Under "Virus & threat protection settings", click "Manage settings"
4. Scroll to "Exclusions" and click "Add or remove exclusions"
5. Add the npc-gui.exe file or installation folder
```

#### 5. **VirusTotal Submission**
- Submit signed binaries to VirusTotal before release
- Address any false positives with AV vendors
- Include VirusTotal link in release notes

#### 6. **Build Process Transparency**
- Use GitHub Actions for reproducible builds
- Publish build logs
- Include checksums (SHA256) for downloads
- Provide source code link in application

### 🔒 Security Checklist

- [x] No hardcoded credentials
- [x] No obfuscated code
- [x] No suspicious API calls (CreateRemoteThread, VirtualAllocEx, etc.)
- [x] No registry modifications (except optional installation)
- [x] No system file modifications
- [x] No privilege escalation attempts
- [x] Clear and documented purpose
- [x] User-initiated actions only
- [x] Proper error handling
- [x] Clean shutdown procedures

### 🚫 Behaviors to Avoid (Already Avoided)

- ❌ No persistence mechanisms (startup registry/folder) without user consent
- ❌ No unsigned drivers
- ❌ No kernel-mode components
- ❌ No process injection
- ❌ No anti-debugging techniques
- ❌ No packed/encrypted executables (beyond normal compression)
- ❌ No rootkit-like behavior

### 📝 Additional Recommendations

#### 1. **Application Icon**
- Current: `appicon.png` and `icon.ico` exist ✅
- Ensure high-quality, professional icon
- Multiple resolutions (16x16, 32x32, 48x48, 256x256)

#### 2. **Installer**
If creating an installer:
- Use reputable installer framework (Inno Setup, WiX, NSIS)
- Sign the installer too
- Request only necessary permissions
- Provide uninstaller
- Clear privacy policy

#### 3. **Documentation**
- Include README with clear purpose
- Privacy policy (what data is collected/stored)
- Terms of use
- Contact information
- Support channels

#### 4. **Auto-Update**
If implementing:
- Use HTTPS for updates
- Verify signatures before updating
- User notification before update
- Rollback capability

### 🔧 Implementation Steps

#### Step 1: Add Version Resource
Create `versioninfo.json`:
```json
{
    "FixedFileInfo": {
        "FileVersion": {
            "Major": 1,
            "Minor": 0,
            "Patch": 0,
            "Build": 0
        },
        "ProductVersion": {
            "Major": 1,
            "Minor": 0,
            "Patch": 0,
            "Build": 0
        },
        "FileFlagsMask": "3f",
        "FileFlags": "00",
        "FileOS": "040004",
        "FileType": "01",
        "FileSubType": "00"
    },
    "StringFileInfo": {
        "Comments": "NPS Client Configuration Manager",
        "CompanyName": "Your Organization",
        "FileDescription": "NPC GUI - NPS Client Manager",
        "FileVersion": "1.0.0.0",
        "InternalName": "npc-gui",
        "LegalCopyright": "Copyright (C) 2025",
        "LegalTrademarks": "",
        "OriginalFilename": "npc-gui.exe",
        "PrivateBuild": "",
        "ProductName": "NPC GUI",
        "ProductVersion": "1.0.0",
        "SpecialBuild": ""
    },
    "VarFileInfo": {
        "Translation": {
            "LangID": "0409",
            "CharsetID": "04b0"
        }
    },
    "IconPath": "build/appicon.png",
    "ManifestPath": "build/windows/wails.exe.manifest"
}
```

#### Step 2: Update Manifest
The current manifest is good, but ensure it includes:
- Proper assembly identity
- Common controls dependency
- DPI awareness
- Requested execution level

#### Step 3: Code Signing Process
```bash
# After building
# 1. Obtain certificate from trusted CA
# 2. Sign executable
signtool sign /f YourCertificate.pfx /p YourPassword \
    /tr http://timestamp.digicert.com /td sha256 /fd sha256 \
    /d "NPC GUI - NPS Client Manager" \
    /du "https://github.com/Yourdaylight/jqhl" \
    npc-gui.exe

# 3. Verify signature
signtool verify /pa /v npc-gui.exe
```

#### Step 4: Create Checksums
```bash
# Generate SHA256 checksums
certutil -hashfile npc-gui.exe SHA256 > npc-gui.exe.sha256
```

### 📊 Antivirus Considerations

#### Common False Positive Triggers (None Present in Current Code):
1. ✅ Network connections - Legitimate and user-controlled
2. ✅ File operations - Only in user directory
3. ✅ Process creation - Only child NPC processes
4. ✅ Registry access - Minimal (service installation only)
5. ✅ Unsigned binary - Will be resolved with code signing

#### Mitigation Strategies:
1. **Code Signing**: Most important
2. **VirusTotal Submission**: Establish reputation
3. **Vendor Whitelisting**: Submit to major AV vendors
4. **Build Transparency**: Public builds via CI/CD
5. **Source Available**: GitHub repository linked

### 🎯 Windows SmartScreen

To avoid SmartScreen warnings:
1. **Code Sign**: Essential
2. **Build Reputation**: Many downloads = better reputation
3. **EV Certificate**: Extended Validation cert bypasses SmartScreen immediately
4. **Consistent Identity**: Use same certificate for all releases

### 📞 Support for False Positives

If flagged as malicious:
1. Submit to vendor analysis:
   - Microsoft: https://www.microsoft.com/en-us/wdsi/filesubmission
   - Symantec: https://submit.symantec.com/
   - McAfee: https://www.mcafee.com/enterprise/en-us/threat-center/submit-sample.html
   - Kaspersky: https://www.kaspersky.com/about/contact

2. Provide:
   - Source code link
   - Signed binary
   - SHA256 hash
   - Clear description of purpose
   - Contact information

### ✅ Current Status

**Good:**
- Clean, legitimate code
- No suspicious behaviors
- Proper error handling
- User-initiated actions
- Transparent operations

**Needs:**
- [ ] Code signing certificate
- [ ] Version resource information
- [ ] Build automation with signing
- [ ] Checksums in releases
- [ ] User documentation about Windows Defender

### 🏁 Conclusion

The NPC GUI application follows Windows development best practices and contains no malicious code patterns. The main step to ensure it's not flagged as malicious is:

1. **Code Signing** (Critical): Obtain and use a code signing certificate
2. **Version Info**: Add comprehensive version resources
3. **Transparency**: Document purpose and provide source access
4. **Testing**: Submit to VirusTotal and major AV vendors

The application is safe and legitimate, following all Windows development guidelines.
