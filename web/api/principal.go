package api

import webservice "github.com/djylb/nps/web/service"

func PrincipalFromActor(actor *Actor) webservice.Principal {
	if actor == nil {
		return webservice.Principal{}
	}
	principal := webservice.Principal{
		Authenticated: actor.Kind != "" && actor.Kind != "anonymous",
		Kind:          actor.Kind,
		SubjectID:     actor.SubjectID,
		Username:      actor.Username,
		IsAdmin:       actor.IsAdmin,
		ClientIDs:     append([]int(nil), actor.ClientIDs...),
		Roles:         append([]string(nil), actor.Roles...),
		Permissions:   append([]string(nil), actor.Permissions...),
	}
	if len(actor.Attributes) > 0 {
		principal.Attributes = make(map[string]string, len(actor.Attributes))
		for key, value := range actor.Attributes {
			principal.Attributes[key] = value
		}
	}
	return principal
}
