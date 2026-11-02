package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/djylb/netx/forwarded"
)

func RandomBytes(maxLen int) ([]byte, error) {
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(maxLen+1)))
	if err != nil {
		return nil, err
	}
	n := int(nBig.Int64())
	buf := make([]byte, n)
	if _, err = rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func ValidatePoW(bits int, parts ...string) bool {
	if bits < 1 || bits > 256 {
		return false
	}
	data := strings.Join(parts, "")
	sum := sha256.Sum256([]byte(data))
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
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

// TrustedProxies parses a comma-separated list of trusted proxies: IP
// addresses, CIDR prefixes, IPv4 patterns whose trailing octets are "*"
// (such as "192.168.*.*"), or "*" for every address. Entries that are none
// of these are skipped.
func TrustedProxies(list string) *forwarded.PrefixSet {
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(list, ",") {
		entry := strings.TrimSpace(raw)
		switch {
		case entry == "":
		case entry == "*":
			prefixes = append(prefixes, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))
		case strings.Contains(entry, "/"):
			if p, err := netip.ParsePrefix(entry); err == nil {
				prefixes = append(prefixes, p)
			}
		case strings.Contains(entry, "*"):
			if p, ok := wildcardPrefix(entry); ok {
				prefixes = append(prefixes, p)
			}
		default:
			if a, err := netip.ParseAddr(entry); err == nil {
				a = a.WithZone("")
				prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	return forwarded.NewPrefixSet(prefixes...)
}

// wildcardPrefix returns the prefix of an IPv4 pattern such as "10.0.*.*".
func wildcardPrefix(entry string) (netip.Prefix, bool) {
	segs := strings.Split(entry, ".")
	if len(segs) != 4 {
		return netip.Prefix{}, false
	}
	var b [4]byte
	bits := 0
	for i, seg := range segs {
		if seg == "*" {
			continue
		}
		if bits != i*8 {
			return netip.Prefix{}, false // a fixed octet after a wildcard
		}
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 || n > 255 {
			return netip.Prefix{}, false
		}
		b[i] = byte(n)
		bits += 8
	}
	return netip.PrefixFrom(netip.AddrFrom4(b), bits), true
}

// IsTrustedProxy reports whether addr, an IP address with or without a
// port, is in the trusted proxy list as TrustedProxies parses it.
func IsTrustedProxy(list, addr string) bool {
	if list == "" {
		return false
	}
	ap, ok := forwarded.ParseNode(strings.TrimSpace(addr))
	return ok && TrustedProxies(list).Contains(ap.Addr())
}

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

// CheckProxyAuth validates the Basic credentials of a forward proxy request. It
// reads Proxy-Authorization and falls back to Authorization only when the
// request carries no Proxy-Authorization header at all, so a client's own
// Authorization for the origin server never overrides its proxy credentials.
func CheckProxyAuth(r *http.Request, user, passwd string, accountMap, authMap map[string]string) bool {
	return checkBasicAuthHeader(credentialHeader(r, "Proxy-Authorization", "Authorization"), user, passwd, accountMap, authMap)
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
	for _, v := range SplitLines(s) {
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
