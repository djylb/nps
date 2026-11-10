package service

import "errors"

var (
	ErrClientQRCodeTextRequired  = errors.New("missing qr code text")
	ErrClientQRCodeSecretInvalid = errors.New("invalid totp secret")
	ErrClientModifyFailed        = errors.New("client modify failed")
	ErrInvalidScheduler          = errors.New("invalid scheduler")
	ErrClientVKeyDuplicate       = errors.New("client verify key duplicate")
	ErrWebUsernameDuplicate      = errors.New("client web username duplicate")
	ErrInvalidRegistration       = errors.New("invalid registration")
	ErrReservedUsername          = errors.New("reserved username")
	ErrClientNotFound            = errors.New("client not found")
	ErrVirtualClientOperation    = errors.New("operation is not supported for virtual clients")
	ErrClientOffline             = errors.New("client is offline")
	ErrServerNotRunning          = errors.New("the server is not running")
	ErrClientUpdateUnsupported   = errors.New("client version does not support remote update")
	ErrServerUpdateUnavailable   = errors.New("server update is unavailable")
	ErrConfigReloadUnavailable   = errors.New("no server runs to reload the config")
	ErrServerUpdateRunning       = errors.New("server update is already running")
	ErrGeoDataUpdateRunning      = errors.New("geodata update is already running")
	ErrRegisterKeyInvalid        = errors.New("invalid registration key")
	ErrRegisterKeyDuplicate      = errors.New("registration key duplicate")
	ErrTunnelNotFound            = errors.New("tunnel not found")
	ErrLaunchFormTooLarge        = errors.New("start command form is too large")
	ErrHostNotFound              = errors.New("host not found")
	ErrHostExists                = errors.New("host already exists")
	ErrModeRequired              = errors.New("mode is required")
	ErrTunnelModeInvalid         = errors.New("unknown tunnel mode")
	ErrPortUnavailable           = errors.New("port unavailable")
	ErrTunnelLimitExceeded       = errors.New("tunnel limit exceeded")
	ErrAdminSetupRequired        = errors.New("admin login is not set up: set web_username and web_password in nps.conf")
	ErrInvalidCredentials        = errors.New("invalid credentials")
	ErrUnauthenticated           = errors.New("unauthenticated")
	ErrForbidden                 = errors.New("forbidden")
	ErrPageNotFound              = errors.New("page not found")
)

// MaxLaunchForm is the longest start command form a client or tunnel keeps.
const MaxLaunchForm = 64 << 10
