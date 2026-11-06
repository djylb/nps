package framework

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/dchest/captcha"
	"github.com/gin-gonic/gin"
)

const (
	CaptchaIDField    = "captcha_id"
	CaptchaValueField = "captcha"
)

func NewCaptchaHTML(baseURL string) template.HTML {
	id := captcha.NewLen(4)
	imageURL := CaptchaImageURL(baseURL, id)
	newURL := joinBase(baseURL, "/captcha/new")
	snippet := fmt.Sprintf(
		`<input type="hidden" name="%s" value="%s"><img class="captcha-img" src="%s" alt="captcha" onclick="return window.npsRefreshCaptcha(this);"><script>window.npsRefreshCaptcha=window.npsRefreshCaptcha||function(img){var hidden=img.previousElementSibling;return fetch(%q,{cache:'no-store'}).then(function(r){return r.json();}).then(function(res){if(hidden){hidden.value=res.id;}img.src=res.url+'?_='+Date.now();return false;}).catch(function(){img.src=img.src.split('?')[0]+'?_='+Date.now();return false;});};</script>`,
		CaptchaIDField,
		id,
		imageURL,
		newURL,
	)
	return template.HTML(snippet)
}

func VerifyCaptcha(id, answer string) bool {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(answer) == "" {
		return false
	}
	return captcha.VerifyString(id, strings.TrimSpace(answer))
}

func CaptchaNewHandler(baseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := captcha.NewLen(4)
		c.JSON(200, gin.H{
			"id":  id,
			"url": CaptchaImageURL(baseURL, id),
		})
	}
}

func CaptchaImageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSuffix(c.Param("id"), ".png")
		if id == "" {
			c.Status(404)
			return
		}
		c.Header("Content-Type", "image/png")
		if err := captcha.WriteImage(c.Writer, id, 100, 50); err != nil {
			c.Status(500)
		}
	}
}

func CaptchaImageURL(baseURL, id string) string {
	return joinBase(baseURL, "/captcha/"+id+".png")
}

func joinBase(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return suffix
	}
	return base + suffix
}
