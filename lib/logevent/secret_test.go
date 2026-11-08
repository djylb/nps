package logevent

import (
	"testing"

	"github.com/djylb/slogx"
)

func TestSecret(t *testing.T) {
	t.Cleanup(func() { SetShowSecrets(false) })
	if _, ok := Secret("k").(slogx.Redacted); !ok || SecretsShown() {
		t.Fatal("secret shown by default")
	}
	SetShowSecrets(true)
	if v, ok := Secret("k").(string); !ok || v != "k" || !SecretsShown() {
		t.Fatal("log_secrets did not show the secret")
	}
}
