package api

import (
	"errors"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
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
	ip := requestVisitorIP(c, cfg)

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
			logs.Warn("Captcha failed for user %s from %s", username, ip)
			loginPolicy.RecordFailure(ip, true)
			setSession(c, "login_nonce", nonce)
			ajaxWithNonce(c, "the verification code is wrong, please get it again and try again", 0, nonce)
			return
		}
	}

	rawPassword := requestString(c, "password")
	if ((isUserBan && cfg.Security.SecureMode) || cfg.Security.ForcePoW || !captchaVerified || isIPBan) && cfg.Security.PoWBits > 0 {
		powX := requestString(c, "powx")
		bits := requestIntValue(c, "bits")
		if bits != cfg.Security.PoWBits || !common.ValidatePoW(cfg.Security.PoWBits, rawPassword, powX) {
			logs.Warn("PoW failed for user %s from %s", username, ip)
			loginPolicy.RecordFailure(ip, true)
			if !captchaVerified {
				loginPolicy.RecordFailure(username, true)
			}
			setSession(c, "login_nonce", nonce)
			ajaxWithNonceBits(c, "pow verification failed", 0, nonce, cfg.Security.PoWBits)
			return
		}
	}

	payload, err := crypt.ParseLoginPayload(rawPassword)
	if err != nil {
		logs.Warn("Decrypt error for user %s from %s: %v", username, ip, err)
		loginPolicy.RecordFailure(ip, true)
		if !captchaVerified {
			loginPolicy.RecordFailure(username, true)
		}
		cert, _ := crypt.GetRSAPublicKeyPEM()
		ajaxWithNonceCert(c, "decrypt error", 0, nonce, cert)
		return
	}

	storedNonce, _ := stored.(string)
	if storedNonce == "" || storedNonce != payload.Nonce {
		logs.Warn("Invalid nonce for user %s from %s", username, ip)
		loginPolicy.RecordFailure(ip, true)
		if !captchaVerified {
			loginPolicy.RecordFailure(username, true)
		}
		ajaxWithNonce(c, "invalid nonce", 0, nonce)
		return
	}

	if cfg.Security.SecureMode {
		nowMillis := common.TimeNow().UnixMilli()
		if payload.Timestamp < nowMillis-settings.MaxSkew || payload.Timestamp > nowMillis+settings.MaxSkew {
			logs.Warn("Timestamp expired for user %s from %s", username, ip)
			loginPolicy.RecordFailure(ip, true)
			if !captchaVerified {
				loginPolicy.RecordFailure(username, true)
			}
			ajaxWithNonceTimestamp(c, "timestamp expired", 0, nonce, nowMillis)
			return
		}
	}

	time.Sleep(time.Millisecond * time.Duration(rand.Intn(20)))
	if a.doLogin(c, username, payload.Password, totpCode, true) {
		logs.Info("Login success for user %s from %s", username, ip)
		deleteSession(c, "login_nonce")
		a.Emit(c, Event{
			Name:     "session.login",
			Resource: "session",
			Action:   "login",
			Fields:   map[string]interface{}{"username": username},
		})
		ajax(c, "login success", 1)
		return
	}

	logs.Warn("Login failed for user %s from %s", username, ip)
	loginPolicy.RecordFailure(username, true)
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

	payload, err := crypt.ParseLoginPayload(password)
	if err != nil {
		cert, _ := crypt.GetRSAPublicKeyPEM()
		ajaxWithNonceCert(c, "decrypt error", 0, nonce, cert)
		return
	}

	storedNonce, _ := stored.(string)
	if storedNonce == "" || storedNonce != payload.Nonce {
		ajaxWithNonce(c, "invalid nonce", 0, nonce)
		return
	}

	if cfg.Security.SecureMode {
		nowMillis := common.TimeNow().UnixMilli()
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
	a.Emit(c, Event{
		Name:     "client.registered",
		Resource: "client",
		Action:   "register",
		Fields:   map[string]interface{}{"client_id": clientID, "username": result.Username, "subject_id": result.SubjectID},
	})
	ajax(c, "register success", 1)
}

func (a *App) LogoutRedirect(c Context) {
	a.Emit(c, Event{
		Name:     "session.logout",
		Resource: "session",
		Action:   "logout",
	})
	clearSessionIdentity(c)
	deleteSession(c, "login_nonce")
	c.Redirect(302, joinBase(a.currentConfig().Web.BaseURL, "/login/index"))
}

func (a *App) doLogin(c Context, username, password, totp string, explicit bool) bool {
	cfg := a.currentConfig()
	loginPolicy := a.loginPolicy()
	loginPolicy.Clean(false)

	ip := requestVisitorIP(c, cfg)

	if explicit && loginPolicy.IsIPBanned(ip) {
		return false
	}

	identity, err := a.Services.Auth.Authenticate(webservice.AuthenticateInput{
		Username: username,
		Password: password,
		TOTP:     totp,
	})
	if err != nil {
		if !errors.Is(err, webservice.ErrInvalidCredentials) {
			logs.Warn("Authentication error for user %s from %s: %v", username, ip, err)
		}
		loginPolicy.RecordFailure(ip, explicit)
		return false
	}

	applySessionIdentity(c, identity)
	a.system().RegisterManagementAccess(c.ClientIP(), identity.ClientIDs, identity.IsAdmin)

	loginPolicy.RemoveBan(ip)
	return true
}

// requestVisitorIP returns the address of the visitor: the peer, or the
// X-Real-IP header a trusted proxy sets.
func requestVisitorIP(c Context, cfg *servercfg.Snapshot) string {
	ip := requestRemoteIP(c)
	httpOnlyPass := cfg.Auth.HTTPOnlyPass
	if (cfg.Auth.AllowXRealIP && common.IsTrustedProxy(cfg.Auth.TrustedProxyIPs, ip)) ||
		(httpOnlyPass != "" && c.RequestHeader("X-NPS-Http-Only") == httpOnlyPass) {
		if realIP := c.RequestHeader("X-Real-IP"); realIP != "" {
			ip = realIP
		}
	}
	return ip
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
