package routers

import (
	"net/http"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

// ConfigApplier returns the applier of the settings of the web manager: when
// one changes, it builds the handler again as Init does and hands it to set.
// The handler is built once the new settings are current, from the same
// arguments as Init.
func ConfigApplier(db *file.DbUtils, identity *crypt.Identity, runtime webservice.Runtime, set func(http.Handler)) servercfg.Applier {
	return servercfg.Applier{
		Name:     "web manager",
		Settings: webSettings,
		Apply: func(_, _ *servercfg.Snapshot) error {
			handler, err := Init(db, identity, runtime)
			if err != nil {
				return err
			}
			set(handler)
			return nil
		},
	}
}

// webSettings are the settings the web manager reads, at Init or for each
// request; its listener is not among them.
var webSettings = []string{
	"App.Name", "App.IPRegisterMaxHours",
	"Web.InstanceKeyFile", "Web.BaseURL", "Web.HeadCustomCode", "Web.CloseOnNotFound",
	"Web.Username", "Web.Password", "Web.TOTPSecret",
	"Auth.Key", "Auth.CryptKey", "Auth.AllowXRealIP", "Auth.TrustedProxyIPs", "Auth.TrustedProxies",
	"Feature.AllowUserLogin", "Feature.AllowUserRegister", "Feature.AllowUserVkeyLogin",
	"Feature.AllowUserChangeUsername", "Feature.OpenCaptcha", "Feature.AllowFlowLimit",
	"Feature.AllowRateLimit", "Feature.AllowTimeLimit", "Feature.AllowConnectionNumLimit",
	"Feature.AllowMultiIP", "Feature.AllowTunnelNumLimit", "Feature.AllowUserLocal",
	"Security.ForcePoW", "Security.PoWBits", "Security.LoginBanTime", "Security.LoginIPBanTime",
	"Security.LoginUserBanTime", "Security.LoginMaxFailTimes", "Security.LoginMaxBody",
	"Security.LoginMaxSkew", "Security.LoginACLMode", "Security.LoginACLRules",
	"Network.HTTPProxyAccessPort", "Network.HTTPSProxyAccessPort",
	"Bridge.Addr", "Bridge.Display",
}
