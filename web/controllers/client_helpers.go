package controllers

import (
	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

func RemoveRepeatedElement(arr []string) (newArr []string) {
	return webservice.UniqueStringsPreserveOrder(arr)
}

func clearClientStatus(client *file.Client, name string) {
	webservice.ClearClientStatus(client, name)
}
