package remote

import (
	"errors"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestConstructorErrorsCarryField(t *testing.T) {
	cases := []struct {
		name  string
		build func() error
		field string
		code  string
	}{
		{"WebDAV 公网 http", func() error {
			_, err := NewWebDAV(WebDAVConfig{URL: "http://example.com/dav", Username: "u", Password: "p"})
			return err
		}, "webdav_url", model.FieldInvalid},
		{"WebDAV 目录点号段", func() error {
			_, err := NewWebDAV(WebDAVConfig{URL: "https://nas.lan/dav", Dir: "a/../b", Username: "u", Password: "p"})
			return err
		}, "webdav_dir", model.FieldInvalid},
		{"WebDAV 地址点号段", func() error {
			_, err := NewWebDAV(WebDAVConfig{URL: "https://nas.lan/a/../dav", Username: "u", Password: "p"})
			return err
		}, "webdav_url", model.FieldInvalid},
		{"SMB 主机", func() error { _, err := NewSMB(SMBConfig{Host: "", Share: "s", Username: "u"}); return err }, "smb_host", model.FieldInvalid},
		{"SMB 端口", func() error {
			_, err := NewSMB(SMBConfig{Host: "h", Port: 70000, Share: "s", Username: "u"})
			return err
		}, "smb_port", model.FieldOutOfRange},
		{"SMB 共享名", func() error { _, err := NewSMB(SMBConfig{Host: "h", Share: "a/b", Username: "u"}); return err }, "smb_share", model.FieldInvalid},
		{"SMB 用户名", func() error { _, err := NewSMB(SMBConfig{Host: "h", Share: "s"}); return err }, "smb_username", model.FieldRequired},
		{"SMB 目录", func() error {
			_, err := NewSMB(SMBConfig{Host: "h", Share: "s", Username: "u", Dir: "../x"})
			return err
		}, "smb_dir", model.FieldInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.build()
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("期望 *ConfigError，得到 %T %v", err, err)
			}
			if ce.Field != c.field || ce.Code != c.code {
				t.Fatalf("Field/Code = %s/%s，期望 %s/%s", ce.Field, ce.Code, c.field, c.code)
			}
			fe := ce.FieldErrors()
			if fe[c.field] != c.code {
				t.Fatalf("FieldErrors = %v", fe)
			}
		})
	}
}
