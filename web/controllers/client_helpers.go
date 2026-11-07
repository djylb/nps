package controllers

import (
	"github.com/djylb/nps/lib/model"
	webservice "github.com/djylb/nps/web/service"
)

func RemoveRepeatedElement(arr []string) (newArr []string) {
	return webservice.UniqueStringsPreserveOrder(arr)
}

func clearClientStatus(client *model.Client, name string) {
	webservice.ClearClientStatus(client, name)
}
