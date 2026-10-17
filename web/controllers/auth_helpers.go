package controllers

import (
	webservice "github.com/djylb/nps/web/service"
)

func IsValidAuthKey(configKey, md5Key string, timestamp int, nowUnix int64) bool {
	return webservice.ValidAuthKey(configKey, md5Key, timestamp, nowUnix)
}
