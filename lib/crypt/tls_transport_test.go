package crypt

import "testing"

func TestPeerTransportDataPin(t *testing.T) {
	certDER := []byte("test-cert-der")
	pin := EncodePeerTransportData(certDER)
	if !VerifyPeerTransportData(pin, certDER) {
		t.Fatal("pin of the certificate does not verify")
	}
	for _, bad := range []string{"", "not-hex", pin[:len(pin)-2], EncodePeerTransportData([]byte("other"))} {
		if VerifyPeerTransportData(bad, certDER) {
			t.Fatalf("pin %q verified", bad)
		}
	}
	if VerifyPeerTransportData(pin, nil) {
		t.Fatal("empty certificate verified")
	}
}
