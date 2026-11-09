package launch

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/config"
)

// Account is one proxy account of a mixed, socks5 or http listener.
type Account struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// Access is one local access, however it was written: a -L URL or an entry
// of a configuration file. It compiles to a config.LocalServer, so the
// access has one path from here on.
type Access struct {
	Access     string    `json:"access"`
	Target     string    `json:"target"`
	LocalType  string    `json:"local_type"`
	LocalPort  int       `json:"local_port"`
	LocalIP    string    `json:"local_ip"`
	ConnMode   string    `json:"conn_mode"`
	LocalProxy bool      `json:"local_proxy"`
	Auth       []Account `json:"auth"`
}

// compile checks a and makes its local server.
func (a Access) compile() (*config.LocalServer, error) {
	typ := strings.ToLower(strings.TrimSpace(a.LocalType))
	if typ == "" {
		typ = config.LocalTCPUDP
	}
	if !slices.Contains(config.LocalTypes, typ) {
		return nil, fmt.Errorf("local type %q: want one of %s", a.LocalType, strings.Join(config.LocalTypes, ", "))
	}
	mode := strings.ToLower(strings.TrimSpace(a.ConnMode))
	if mode == "" {
		mode = config.ConnAuto
	}
	if !slices.Contains(config.ConnModes, mode) {
		return nil, fmt.Errorf("conn_mode %q: want one of %s", a.ConnMode, strings.Join(config.ConnModes, ", "))
	}
	switch {
	case a.Access == "":
		return nil, errors.New("access (the key of the tunnel) is required")
	case a.LocalPort < 0 || a.LocalPort > 65535:
		return nil, fmt.Errorf("port %d is out of range", a.LocalPort)
	case a.Target != "" && config.IsProxyType(typ):
		return nil, fmt.Errorf("%s takes the destination from each connection, it has no target", typ)
	case len(a.Auth) > 0 && !config.IsAccountType(typ):
		return nil, errors.New("accounts are only used by mixed, socks5 and http")
	}
	l := &config.LocalServer{Type: typ, ConnMode: mode, Port: a.LocalPort, Ip: a.LocalIP,
		Password: a.Access, Target: a.Target, LocalProxy: a.LocalProxy}
	for _, acc := range a.Auth {
		switch {
		case acc.User == "":
			return nil, errors.New("an account needs a user name")
		case l.Auth == nil:
			l.Auth = make(map[string]string, len(a.Auth))
		}
		if p, dup := l.Auth[acc.User]; dup && p != acc.Pass {
			return nil, fmt.Errorf("account %q is given twice with different passwords", acc.User)
		}
		l.Auth[acc.User] = acc.Pass
	}
	return l, nil
}

// Local is one parsed -L: the access and the connection it goes over.
type Local struct {
	Access Access
	Via    string // the name of a connection; empty when there is only one
	Source string // where it was written, for messages
}

// localSchemes are the local types a -L is written with.
var localSchemes = []string{config.LocalTCP, config.LocalUDP, config.LocalTCPUDP, config.LocalMixed, config.LocalSOCKS5, config.LocalHTTP, config.LocalTransparent}

// ParseLocal reads one -L value, scheme://[user:pass@]host:port?access=..&target=..
// Errors never contain the value: it holds the access key.
func ParseLocal(raw string) (Local, error) {
	var out Local
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	scheme = strings.ToLower(scheme)
	if !ok || !slices.Contains(localSchemes, scheme) {
		return out, fmt.Errorf("-L starts with one of %s://", strings.Join(localSchemes, "://, "))
	}
	authority, query, _ := strings.Cut(rest, "?")
	if user, host, has := cutLast(authority, "@"); has {
		acc, err := parseAccount(user)
		if err != nil {
			return out, err
		}
		out.Access.Auth = append(out.Access.Auth, acc)
		authority = host
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return out, errors.New("-L needs a listen address host:port, such as :2000 or [::1]:2000")
	}
	if out.Access.LocalPort, err = strconv.Atoi(port); err != nil {
		return out, fmt.Errorf("-L: port %q is not a number", port)
	}
	out.Access.LocalType, out.Access.LocalIP = scheme, host
	seen := make(map[string]bool)
	for pair := range strings.SplitSeq(query, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if v, err = url.QueryUnescape(v); err != nil {
			return out, fmt.Errorf("-L: parameter %q is not percent-encoded correctly", k)
		}
		if seen[k] && k != "auth" {
			return out, fmt.Errorf("-L: parameter %q given twice", k)
		}
		seen[k] = true
		switch k {
		case "access":
			out.Access.Access = v
		case "target":
			out.Access.Target = v
		case "conn_mode":
			out.Access.ConnMode = v
		case "via":
			out.Via = v
		case "local_proxy":
			if out.Access.LocalProxy, err = strconv.ParseBool(v); err != nil {
				return out, fmt.Errorf("-L: local_proxy %q is not a boolean", v)
			}
		case "auth":
			u, p, has := strings.Cut(v, ":")
			if !has {
				return out, errors.New("-L: auth is user:pass")
			}
			out.Access.Auth = append(out.Access.Auth, Account{User: u, Pass: p})
		default:
			return out, fmt.Errorf("-L: unknown parameter %q (want access, target, conn_mode, local_proxy, via, auth)", k)
		}
	}
	if _, err := out.Access.compile(); err != nil {
		return out, fmt.Errorf("-L: %w", err)
	}
	return out, nil
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return "", s, false
}

// parseAccount reads the percent-encoded user:pass before "@".
func parseAccount(s string) (Account, error) {
	u, p, _ := strings.Cut(s, ":")
	user, err := url.PathUnescape(u)
	if err != nil {
		return Account{}, errors.New("-L: the user name is not percent-encoded correctly")
	}
	pass, err := url.PathUnescape(p)
	if err != nil {
		return Account{}, errors.New("-L: the password is not percent-encoded correctly")
	}
	return Account{User: user, Pass: pass}, nil
}

// ParseLocals reads the -L values in order; a value @path is a file with one
// value per line (blank lines and lines starting with # are skipped), which
// is how an installed service keeps them off its command line.
func ParseLocals(values []string) ([]Local, error) {
	var out []Local
	add := func(raw, source string) error {
		l, err := ParseLocal(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		l.Source = source
		out = append(out, l)
		return nil
	}
	for i, v := range values {
		v = strings.TrimSpace(v)
		source := "-L #" + strconv.Itoa(i+1)
		if !strings.HasPrefix(v, "@") {
			if err := add(v, source); err != nil {
				return nil, err
			}
			continue
		}
		path := strings.TrimSpace(v[1:])
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line[0] == '#' {
				continue
			}
			if err := add(line, fmt.Sprintf("-L @%s line %d", path, n+1)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// formatLocal writes l as a -L value; reveal false masks the secrets.
func formatLocal(l *config.LocalServer, via string, reveal bool) string {
	var b strings.Builder
	b.WriteString(l.Type + "://" + net.JoinHostPort(l.Ip, strconv.Itoa(l.Port)) + "?access=")
	if reveal {
		b.WriteString(url.QueryEscape(l.Password))
	} else {
		b.WriteString("***")
	}
	if l.ConnMode != "" && l.ConnMode != config.ConnAuto {
		b.WriteString("&conn_mode=" + l.ConnMode)
	}
	if l.Target != "" && !config.IsProxyType(l.Type) {
		b.WriteString("&target=" + url.QueryEscape(l.Target))
	}
	if l.LocalProxy {
		b.WriteString("&local_proxy=true")
	}
	for _, user := range slices.Sorted(maps.Keys(l.Auth)) {
		pass := "***"
		if reveal {
			pass = url.QueryEscape(l.Auth[user])
		}
		b.WriteString("&auth=" + url.QueryEscape(user) + ":" + pass)
	}
	if via != "" {
		b.WriteString("&via=" + url.QueryEscape(via))
	}
	return b.String()
}
