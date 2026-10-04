package agentsrv

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestValidateDialOut直连规范成空串(t *testing.T) {
	for _, in := range []string{"direct", " direct ", ""} {
		got, err := validateDialOut(model.DialOutRequest{
			Address: "203.0.113.5:31418", Fingerprint: "SHA256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Code: "abc", ProxyID: in,
		})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got.proxyID != "" {
			t.Fatalf("%q: proxyID=%q，改回直连后新设备 proxy_id 应为空", in, got.proxyID)
		}
	}
}
