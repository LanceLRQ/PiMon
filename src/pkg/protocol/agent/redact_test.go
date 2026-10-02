package agent

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact_格式化与日志不泄露密钥(t *testing.T) {
	inst := AssignedInstance{
		ID: "i1", PluginID: "p", Config: map[string]any{"host": "x"},
		Secrets: map[string]string{"token": "S3CRET-VALUE"}, ProxyURL: "http://user:PROXYPW@127.0.0.1:8080",
	}
	cases := map[string]any{
		"Auth":       &Auth{Credential: "CRED-SECRET", Role: RoleAgent},
		"Auth值":      Auth{Credential: "CRED-SECRET", Role: RoleAgent},
		"Pair":       &Pair{Code: "PAIR-SECRET", Role: RoleAgent, Name: "n"},
		"PairOK":     &PairOK{Credential: "CRED-SECRET", DeviceID: "d"},
		"Instance":   inst,
		"Instance指针": &inst,
		"Assign":     &Assign{Version: 1, Instances: []AssignedInstance{inst}},
	}
	secrets := []string{"CRED-SECRET", "PAIR-SECRET", "S3CRET-VALUE", "PROXYPW"}
	for name, v := range cases {
		outs := []string{fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), fmt.Sprintf("%s", v)}
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "v", v)
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", "v", v)
		outs = append(outs, buf.String())
		for _, o := range outs {
			for _, s := range secrets {
				if strings.Contains(o, s) {
					t.Errorf("%s 输出泄露 %q: %s", name, s, o)
				}
			}
		}
	}
}

func TestRedact_保留非敏感信息(t *testing.T) {
	s := fmt.Sprintf("%v", AssignedInstance{ID: "i1", PluginID: "plug", ProxyURL: "http://user:pw@127.0.0.1:8080", Secrets: map[string]string{"token": "x"}})
	for _, w := range []string{"i1", "plug", "user", "127.0.0.1:8080", "token", "***"} {
		if !strings.Contains(s, w) {
			t.Errorf("输出缺 %q: %s", w, s)
		}
	}
}

func TestRedact_不影响JSON编码(t *testing.T) {
	data, err := Encode(&Auth{Credential: "CRED", Role: RoleHub})
	if err != nil || !strings.Contains(string(data), `"credential":"CRED"`) {
		t.Fatalf("编码应保留原文: %s %v", data, err)
	}
}
