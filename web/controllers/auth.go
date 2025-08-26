package controllers

import (
	"encoding/hex"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
)

type AuthController struct {
	beego.Controller
}

func (s *AuthController) GetAuthKey() {
	m := make(map[string]interface{})
	defer func() {
		s.Data["json"] = m
		s.ServeJSON()
	}()
	if cryptKey := beego.AppConfig.String("auth_crypt_key"); len(cryptKey) != 16 {
		m["status"] = 0
		return
	} else {
		b, err := crypt.AesEncrypt([]byte(beego.AppConfig.String("auth_key")), []byte(cryptKey))
		if err != nil {
			m["status"] = 0
			return
		}
		m["status"] = 1
		m["crypt_auth_key"] = hex.EncodeToString(b)
		m["crypt_type"] = "aes cbc"
		return
	}
}

func (s *AuthController) GetTime() {
	m := make(map[string]interface{})
	m["time"] = common.TimeNow().Unix()
	s.Data["json"] = m
	s.ServeJSON()
}

func (s *AuthController) GetCert() {
	m := make(map[string]interface{})
	var err error
	m["cert"], err = crypt.GetRSAPublicKeyPEM()
	if err != nil {
		m["status"] = 0
	} else {
		m["status"] = 1
	}
	s.Data["json"] = m
	s.ServeJSON()
}
