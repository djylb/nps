package controllers

import webservice "github.com/djylb/nps/web/service"

func IfLoginFail(key string, explicit bool) {
	webservice.SharedLoginPolicy().RecordFailure(key, explicit)
}

func RemoveAllLoginBan() {
	webservice.SharedLoginPolicy().RemoveAllBans()
}
