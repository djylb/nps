package httpproxy

import (
	"context"
	"strings"
	"time"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/obszap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var acmeLog = obs.Module("acme")

// acmeZap is the zap logger of certmagic and acmez: their warnings and
// errors are plain lines of module acme, the rest debug.
func acmeZap() *zap.Logger { return obszap.New(acmeLog, zapcore.WarnLevel) }

// certEvent maps certmagic's events to cert_* events; it never stops an
// operation.
func certEvent(_ context.Context, name string, data map[string]any) error {
	domain, _ := data["identifier"].(string)
	renewal, _ := data["renewal"].(bool)
	remaining, hasRemaining := data["remaining"].(time.Duration)
	r := 0
	if renewal {
		r = 1
	}
	switch name {
	case "cert_obtained":
		issuer, _ := data["issuer"].(string)
		obs.Emit(acmeLog, &logevent.CertIssued, "domain", domain, "issuer", issuer, "renewal", r)
	case "cert_failed":
		d := &logevent.CertFailed
		if hasRemaining && remaining < 7*24*time.Hour {
			d = &logevent.CertFailedUrgent
		}
		msg := ""
		reason := "issue_failed"
		if err, ok := data["error"].(error); ok {
			msg = err.Error()
			reason = acmeReason(msg)
			if len(msg) > 300 {
				msg = msg[:300]
			}
		}
		args := []any{"domain", domain, "renewal", r, "reason", reason}
		if hasRemaining {
			args = append(args, "remaining_s", int64(remaining.Seconds()))
		}
		obs.EmitMsg(acmeLog, d, msg, args...)
	case "cert_ocsp_revoked":
		var subjects []string
		if s, ok := data["subjects"].([]string); ok {
			subjects = s
		}
		obs.Emit(acmeLog, &logevent.CertRevoked, "domain", strings.Join(subjects, ","), "reason", "ocsp")
	case "cert_obtaining":
		acmeLog.Debug("obtaining certificate", "domain", domain, "renewal", r)
	}
	return nil
}

var acmeReasons = [][2]string{
	{"ratelimited", "rate_limited"},
	{"rate limit", "rate_limited"},
	{"dns", "dns_failed"},
	{"challenge", "challenge_failed"},
	{"unauthorized", "challenge_failed"},
	{"account", "account_failed"},
}

func acmeReason(msg string) string {
	m := strings.ToLower(msg)
	for _, p := range acmeReasons {
		if strings.Contains(m, p[0]) {
			return p[1]
		}
	}
	return "issue_failed"
}

var proxyLog = obs.Module("httpproxy")
