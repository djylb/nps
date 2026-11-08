package api

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/netx/forwarded"
	"github.com/djylb/nps/lib/auth"
	"github.com/djylb/nps/lib/clock"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/slogx"
)

func (a *App) LoginVerify(c Context) {
	cfg := a.currentConfig()
	loginPolicy := a.loginPolicy()
	settings := loginPolicy.Settings()
	if requestContentLength(c) > settings.MaxLoginBody {
		c.RespondString(413, "Payload too large")
		return
	}

	nonce := crypt.GetRandomString(16)
	stored := c.SessionValue("login_nonce")
	setSession(c, "login_nonce", nonce)

	username := requestString(c, "username")
	ip := VisitorIP(c, cfg)

	isIPBan := loginPolicy.IsIPBanned(ip)
	isUserBan := loginPolicy.IsUserBanned(username)

	totpCode := ""
	captchaVerified := true
	if cfg.Feature.OpenCaptcha {
		captchaID := requestString(c, framework.CaptchaIDField)
		captchaCode := requestString(c, framework.CaptchaValueField)
		codeLen := len(captchaCode)
		if codeLen >= crypt.TotpLen {
			totpCode = captchaCode[codeLen-crypt.TotpLen:]
			captchaCode = captchaCode[:codeLen-crypt.TotpLen]
		}
		captchaVerified = framework.VerifyCaptcha(captchaID, captchaCode)
		if isIPBan || (!captchaVerified && totpCode == "") || (!captchaVerified && isUserBan) {
			reason := "bad_captcha"
			if isIPBan {
				reason = "banned"
			}
			loginFailed(ip, username, reason)
			loginPolicy.RecordIPFailure(ip)
			setSession(c, "login_nonce", nonce)
			ajaxWithNonce(c, "the verification code is wrong, please get it again and try again", 0, nonce)
			return
		}
	}

	rawPassword := requestString(c, "password")
	if ((isUserBan && cfg.Security.SecureMode) || cfg.Security.ForcePoW || !captchaVerified || isIPBan) && cfg.Security.PoWBits > 0 {
		powX := requestString(c, "powx")
		bits := requestIntValue(c, "bits")
		if bits != cfg.Security.PoWBits || !auth.ValidatePoW(cfg.Security.PoWBits, rawPassword, powX) {
			loginFailed(ip, username, "bad_pow")
			loginPolicy.RecordIPFailure(ip)
			if !captchaVerified {
				loginPolicy.RecordUserFailure(username, ip)
			}
			setSession(c, "login_nonce", nonce)
			ajaxWithNonceBits(c, "pow verification failed", 0, nonce, cfg.Security.PoWBits)
			return
		}
	}

	payload, err := a.Identity.ParseLoginPayload(rawPassword)
	if err != nil {
		loginFailed(ip, username, "malformed", "err", err)
		loginPolicy.RecordIPFailure(ip)
		if !captchaVerified {
			loginPolicy.RecordUserFailure(username, ip)
		}
		cert, _ := a.Identity.RSAPublicKeyPEM()
		ajaxWithNonceCert(c, "decrypt error", 0, nonce, cert)
		return
	}

	storedNonce, _ := stored.(string)
	if storedNonce == "" || storedNonce != payload.Nonce {
		loginFailed(ip, username, "bad_nonce")
		loginPolicy.RecordIPFailure(ip)
		if !captchaVerified {
			loginPolicy.RecordUserFailure(username, ip)
		}
		ajaxWithNonce(c, "invalid nonce", 0, nonce)
		return
	}

	if cfg.Security.SecureMode {
		nowMillis := clock.Now().UnixMilli()
		if payload.Timestamp < nowMillis-settings.MaxSkew || payload.Timestamp > nowMillis+settings.MaxSkew {
			loginFailed(ip, username, "expired")
			loginPolicy.RecordIPFailure(ip)
			if !captchaVerified {
				loginPolicy.RecordUserFailure(username, ip)
			}
			ajaxWithNonceTimestamp(c, "timestamp expired", 0, nonce, nowMillis)
			return
		}
	}

	time.Sleep(time.Millisecond * time.Duration(rand.Intn(20)))
	reason := a.doLogin(c, username, payload.Password, totpCode)
	if reason == "" {
		slogx.Emit(context.Background(), log, &logevent.LoginOK, "src_ip", ip, "user", username)
		deleteSession(c, "login_nonce")
		ajax(c, "login success", 1)
		return
	}

	loginFailed(ip, username, reason)
	loginPolicy.RecordUserFailure(username, ip)
	ajaxWithNonce(c, "username or password incorrect", 0, nonce)
}

func (a *App) RegisterUser(c Context) {
	cfg := a.currentConfig()
	if !cfg.Feature.AllowUserRegister {
		ajax(c, "register is not allow", 0)
		return
	}

	nonce := crypt.GetRandomString(16)
	stored := c.SessionValue("login_nonce")
	setSession(c, "login_nonce", nonce)

	username := requestString(c, "username")
	password := requestString(c, "password")
	if username == "" || password == "" || username == cfg.Web.Username {
		ajaxWithNonce(c, "please check your input", 0, nonce)
		return
	}

	if cfg.Feature.OpenCaptcha && !framework.VerifyCaptcha(requestString(c, framework.CaptchaIDField), requestString(c, framework.CaptchaValueField)) {
		setSession(c, "login_nonce", nonce)
		ajaxWithNonce(c, "the verification code is wrong, please get it again and try again", 0, nonce)
		return
	}

	payload, err := a.Identity.ParseLoginPayload(password)
	if err != nil {
		cert, _ := a.Identity.RSAPublicKeyPEM()
		ajaxWithNonceCert(c, "decrypt error", 0, nonce, cert)
		return
	}

	storedNonce, _ := stored.(string)
	if storedNonce == "" || storedNonce != payload.Nonce {
		ajaxWithNonce(c, "invalid nonce", 0, nonce)
		return
	}

	if cfg.Security.SecureMode {
		nowMillis := clock.Now().UnixMilli()
		maxSkew := a.loginPolicy().Settings().MaxSkew
		if payload.Timestamp < nowMillis-maxSkew || payload.Timestamp > nowMillis+maxSkew {
			ajaxWithNonceTimestamp(c, "timestamp expired", 0, nonce, nowMillis)
			return
		}
	}

	result, err := a.Services.Auth.RegisterUser(webservice.RegisterUserInput{
		Username: username,
		Password: payload.Password,
	})
	if err != nil {
		msg := err.Error()
		if errors.Is(err, webservice.ErrInvalidRegistration) || errors.Is(err, webservice.ErrReservedUsername) {
			msg = "please check your input"
		}
		ajaxWithNonce(c, msg, 0, nonce)
		return
	}

	deleteSession(c, "login_nonce")
	clientID := 0
	if len(result.ClientIDs) > 0 {
		clientID = result.ClientIDs[0]
	}
	log.Info("user registered", "src_ip", VisitorIP(c, cfg), "user", result.Username, "client", clientID)
	ajax(c, "register success", 1)
}

func (a *App) LogoutRedirect(c Context) {
	logLogout(c, a.currentConfig())
	_ = clearSessionIdentity(c)
	deleteSession(c, "login_nonce")
	c.Redirect(302, joinBase(a.currentConfig().Web.BaseURL, "/login/index"))
}

// loginFailed writes the login_failed event of a login from ip as user.
func loginFailed(ip, user, reason string, args ...any) {
	slogx.Emit(context.Background(), log, &logevent.LoginFailed, append([]any{"src_ip", ip, "user", user, "reason", reason}, args...)...)
}

// logLogout notes that the user of c logged out.
func logLogout(c Context, cfg *servercfg.Snapshot) {
	if user := actorName(c); user != "" {
		log.Info("logout", "src_ip", VisitorIP(c, cfg), "user", user)
	}
}

// doLogin logs the user in and returns "", or returns why the login failed.
func (a *App) doLogin(c Context, username, password, totp string) string {
	cfg := a.currentConfig()
	loginPolicy := a.loginPolicy()
	ip := VisitorIP(c, cfg)

	if loginPolicy.IsIPBanned(ip) {
		return "banned"
	}

	identity, err := a.Services.Auth.Authenticate(webservice.AuthenticateInput{
		Username: username,
		Password: password,
		TOTP:     totp,
	})
	if err != nil {
		loginPolicy.RecordIPFailure(ip)
		if !errors.Is(err, webservice.ErrInvalidCredentials) {
			log.Warn("authentication error", "src_ip", ip, "user", username, "err", err)
			return "error"
		}
		return "bad_password"
	}

	if err := applySessionIdentity(c, identity); err != nil {
		log.Error("login session not saved", "src_ip", ip, "user", username, "err", err)
		return "session_failed"
	}
	a.system().RegisterManagementAccess(ip, identity.ClientIDs, identity.IsAdmin)

	loginPolicy.ClearUserFailures(username)
	return ""
}

// VisitorIP returns the address of the visitor of a web request: the peer,
// or the X-Real-IP header a trusted proxy sets, or that a request carrying
// the HTTP-only pass sets. A header value that is not an IP address, with or
// without a port, is ignored. It is the one resolver of the visitor address
// for the web interface; gin's own ClientIP is not used.
func VisitorIP(c Context, cfg *servercfg.Snapshot) string {
	peer, err := netip.ParseAddrPort(c.RemoteAddr())
	if err != nil {
		return requestRemoteIP(c)
	}
	var trusted *forwarded.PrefixSet
	switch httpOnlyPass := cfg.Auth.HTTPOnlyPass; {
	case httpOnlyPass != "" && c.RequestHeader("X-NPS-Http-Only") == httpOnlyPass:
		trusted = auth.AnyAddress
	case cfg.Auth.AllowXRealIP:
		trusted = cfg.Auth.TrustedProxies
	}
	if trusted == nil {
		return peer.Addr().Unmap().String()
	}
	r := forwarded.Resolver{Trusted: trusted, Header: forwarded.HeaderXRealIP}
	var header map[string][]string
	if v := c.RequestHeader("X-Real-IP"); v != "" {
		header = map[string][]string{"X-Real-Ip": {v}}
	}
	return r.Resolve(peer, header).Addr.Addr().String()
}

func requestRemoteIP(c Context) string {
	host, _, err := net.SplitHostPort(c.RemoteAddr())
	if err != nil {
		return c.RemoteAddr()
	}
	return host
}

func requestContentLength(c Context) int64 {
	value := strings.TrimSpace(c.RequestHeader("Content-Length"))
	if value == "" {
		return 0
	}
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return size
}
