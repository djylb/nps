package framework

import "github.com/gin-gonic/gin"

const (
	sessionContextKey = "nps.session"
	requestDataKey    = "nps.request_data"
	requestParamsKey  = "nps.request_params"
	requestInputKey   = "nps.request_input"
)

func SetRequestData(g *gin.Context, key string, value interface{}) {
	data := requestDataFromContext(g)
	if data == nil {
		data = make(map[string]interface{})
		g.Set(requestDataKey, data)
	}
	data[key] = value
	g.Set(key, value)
}

func SetRequestParam(g *gin.Context, key, value string) {
	params := requestParamsFromContext(g)
	if params == nil {
		params = make(map[string]string)
		g.Set(requestParamsKey, params)
	}
	params[key] = value
}

func RequestParam(g *gin.Context, key string) (string, bool) {
	params := requestParamsFromContext(g)
	if params == nil {
		return "", false
	}
	value, ok := params[key]
	return value, ok
}

// SetRequestInput records a value submitted in the request body. Unlike
// request params it never takes precedence over server-normalized values.
func SetRequestInput(g *gin.Context, key, value string) {
	input := stringMapFromContext(g, requestInputKey)
	if input == nil {
		input = make(map[string]string)
		g.Set(requestInputKey, input)
	}
	input[key] = value
}

func RequestInput(g *gin.Context, key string) (string, bool) {
	input := stringMapFromContext(g, requestInputKey)
	if input == nil {
		return "", false
	}
	value, ok := input[key]
	return value, ok
}

func requestDataFromContext(g *gin.Context) map[string]interface{} {
	if v, ok := g.Get(requestDataKey); ok {
		if data, ok := v.(map[string]interface{}); ok {
			return data
		}
	}
	return nil
}

func requestParamsFromContext(g *gin.Context) map[string]string {
	return stringMapFromContext(g, requestParamsKey)
}

func stringMapFromContext(g *gin.Context, key string) map[string]string {
	if v, ok := g.Get(key); ok {
		if params, ok := v.(map[string]string); ok {
			return params
		}
	}
	return nil
}
