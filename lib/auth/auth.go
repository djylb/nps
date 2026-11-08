// Package auth checks Basic credentials against single and multiple
// accounts, validates proof-of-work answers and matches trusted proxies.
package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/netip"
	"strings"

	"github.com/djylb/geomatch"
	"github.com/djylb/netx/forwarded"
	"github.com/djylb/nps/lib/textutil"
)

func ValidatePoW(bits int, parts ...string) bool {
	if bits < 1 || bits > 256 {
		return false
	}
	data := strings.Join(parts, "")
	sum := sha256.Sum256([]byte(data))
	fullBytes := bits / 8
	for i := range fullBytes {
		if sum[i] != 0 {
			return false
		}
	}
	remBits := bits % 8
	if remBits > 0 {
		mask := byte(0xFF << (8 - remBits))
		if (sum[fullBytes] & mask) != 0 {
			return false
		}
	}
	return true
}

// ParseTrustedProxies parses a comma-separated list of trusted proxies: IP
// addresses, CIDR prefixes, IPv4 patterns whose trailing octets are "*"
// (such as "192.168.*.*"), or "*" for every address. Entries that are none
// of these are skipped. Parse the list once, where the config is read.
func ParseTrustedProxies(list string) *forwarded.PrefixSet {
	var prefixes []netip.Prefix
	for raw := range strings.SplitSeq(list, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "*" {
			return AnyAddress
		}
		r, err := geomatch.ParseRule(entry)
		if err != nil || r.Kind != geomatch.RuleIP || r.Last.IsValid() || len(r.Ports) > 0 {
			continue
		}
		prefixes = append(prefixes, r.Prefix)
	}
	return forwarded.NewPrefixSet(prefixes...)
}

// AnyAddress is the set of every address.
var AnyAddress = forwarded.NewPrefixSet(netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))

// CheckAuthWithAccountMap
// u current login user
// p current login passwd
// user global user
// passwd global passwd
// accountMap enable multi user auth
func CheckAuthWithAccountMap(u, p, user, passwd string, accountMap, authMap map[string]string) bool {
	// Single account check
	noAccountMap := len(accountMap) == 0
	noAuthMap := len(authMap) == 0
	if noAccountMap && noAuthMap {
		return u == user && p == passwd
	}
	// Multi-account authentication check
	if len(u) == 0 {
		return false
	}
	if u == user && p == passwd {
		return true
	}
	if !noAccountMap {
		if P, ok := accountMap[u]; ok && p == P {
			return true
		}
	}
	if !noAuthMap {
		if P, ok := authMap[u]; ok && p == P {
			return true
		}
	}
	return false
}

// NeedsAuth reports whether any credential is configured. A username or a
// password on its own is enough to require authentication.
func NeedsAuth(user, passwd string, accountMap, authMap map[string]string) bool {
	return user != "" || passwd != "" || len(accountMap) > 0 || len(authMap) > 0
}

// CheckAuth validates the Basic credentials of a request to a reverse-proxied
// host. It reads Authorization and falls back to Proxy-Authorization only when
// the request carries no Authorization header at all.
func CheckAuth(r *http.Request, user, passwd string, accountMap, authMap map[string]string) bool {
	return checkBasicAuthHeader(credentialHeader(r, "Authorization", "Proxy-Authorization"), user, passwd, accountMap, authMap)
}

// credentialHeader returns the primary header if present, otherwise the fallback.
func credentialHeader(r *http.Request, primary, fallback string) string {
	if v := r.Header.Values(primary); len(v) > 0 {
		return v[0]
	}
	return r.Header.Get(fallback)
}

func checkBasicAuthHeader(header, user, passwd string, accountMap, authMap map[string]string) bool {
	if !NeedsAuth(user, passwd, accountMap, authMap) {
		return true
	}
	u, p, ok := ParseBasicAuth(header)
	if !ok {
		return false
	}
	return CheckAuthWithAccountMap(u, p, user, passwd, accountMap, authMap)
}

// ParseBasicAuth parses a "Basic <base64(user:pass)>" credential. The scheme is
// matched case-insensitively and unpadded base64 is accepted.
func ParseBasicAuth(header string) (user, passwd string, ok bool) {
	scheme, cred, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Basic") {
		return "", "", false
	}
	cred = strings.TrimSpace(cred)
	b, err := base64.StdEncoding.DecodeString(cred)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(cred); err != nil {
			return "", "", false
		}
	}
	return strings.Cut(string(b), ":")
}

func DealMultiUser(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	multiUserMap := make(map[string]string)
	for _, v := range textutil.SplitLines(s) {
		if strings.TrimSpace(v) == "" {
			continue
		}
		item := strings.SplitN(v, "=", 2)
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
		}
		multiUserMap[strings.TrimSpace(item[0])] = strings.TrimSpace(item[1])
	}
	return multiUserMap
}
