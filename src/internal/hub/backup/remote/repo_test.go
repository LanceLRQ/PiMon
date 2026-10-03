package remote

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func newRepo(t *testing.T) (*Repo, *store.DB, *clock.Fake) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return NewRepo(db, box, clk), db, clk
}

func smbInput() model.BackupRemoteInput {
	return model.BackupRemoteInput{
		Enabled: true, Type: "smb", SMBHost: "nas.lan", SMBShare: "backup", SMBDir: "pimon",
		SMBUsername: "u", SMBPassword: "smb-secret-pw", Passphrase: "correct horse battery", Keep: 30,
	}
}

func fieldErr(t *testing.T, err error) model.FieldErrors {
	t.Helper()
	var fe model.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("期望 FieldErrors，得到 %v", err)
	}
	return fe
}

func TestGetDefaults(t *testing.T) {
	r, _, _ := newRepo(t)
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Type != "smb" || got.SMBPort != 445 || got.Keep != 30 || got.UpdatedAt != nil || got.Passphrase.Set {
		t.Errorf("默认值不符: %+v", got)
	}
}

func TestPutAndSecretsNotEchoed(t *testing.T) {
	r, db, _ := newRepo(t)
	ctx := context.Background()
	got, err := r.Put(ctx, smbInput())
	if err != nil {
		t.Fatal(err)
	}
	if !got.SMBPassword.Set || !got.Passphrase.Set || got.WebDAVPassword.Set || got.SMBPort != 445 || got.UpdatedAt == nil {
		t.Errorf("响应不符: %+v", got)
	}
	var enc1, enc2 string
	if err := db.QueryRow(`SELECT smb_password_enc, passphrase_enc FROM backup_remote`).Scan(&enc1, &enc2); err != nil {
		t.Fatal(err)
	}
	if enc1 == "" || strings.Contains(enc1, "smb-secret-pw") || enc2 == "" || strings.Contains(enc2, "correct") {
		t.Error("密钥应加密入库")
	}
	cfg, err := r.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMBPassword != "smb-secret-pw" || cfg.Passphrase != "correct horse battery" {
		t.Errorf("解密配置不符: %+v", cfg)
	}
	sc := cfg.SMB()
	if sc.Host != "nas.lan" || sc.Port != 445 || sc.Share != "backup" || sc.Password != "smb-secret-pw" {
		t.Errorf("SMB 配置不符: %+v", sc)
	}
}

func TestPutBlankKeepsSecrets(t *testing.T) {
	r, _, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Put(ctx, smbInput()); err != nil {
		t.Fatal(err)
	}
	in := smbInput()
	in.SMBPassword, in.Passphrase, in.Keep = "", "", 7
	got, err := r.Put(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.PassphraseChanged || got.Keep != 7 {
		t.Errorf("留空不应算改口令: %+v", got)
	}
	cfg, _ := r.Config(ctx)
	if cfg.SMBPassword != "smb-secret-pw" || cfg.Passphrase != "correct horse battery" {
		t.Error("留空应保留原值")
	}
}

func TestPassphraseChanged(t *testing.T) {
	r, _, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Put(ctx, smbInput()); err != nil {
		t.Fatal(err)
	}
	in := smbInput()
	in.Passphrase = "correct horse battery" // 相同
	if got, _ := r.Put(ctx, in); got.PassphraseChanged {
		t.Error("相同口令不算更改")
	}
	in.Passphrase = "another long passphrase"
	got, err := r.Put(ctx, in)
	if err != nil || !got.PassphraseChanged {
		t.Errorf("新口令应标记 changed: %+v %v", got, err)
	}
}

func TestSwitchTypeKeepsOtherFields(t *testing.T) {
	r, _, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Put(ctx, smbInput()); err != nil {
		t.Fatal(err)
	}
	in := model.BackupRemoteInput{
		Enabled: true, Type: "webdav", WebDAVURL: "http://192.168.1.2:5005/dav", WebDAVDir: "b",
		WebDAVUsername: "w", WebDAVPassword: "dav-pw", Keep: 30,
		SMBHost: "nas.lan", SMBShare: "backup", SMBUsername: "u",
	}
	if _, err := r.Put(ctx, in); err != nil {
		t.Fatal(err)
	}
	cfg, _ := r.Config(ctx)
	if cfg.SMBPassword != "smb-secret-pw" || cfg.WebDAVPassword != "dav-pw" || cfg.Passphrase == "" {
		t.Errorf("两类型密码应各自保留: %+v", cfg)
	}
	wc := cfg.WebDAV()
	if wc.URL != "http://192.168.1.2:5005/dav" || wc.Dir != "b" || wc.Password != "dav-pw" {
		t.Errorf("WebDAV 配置不符: %+v", wc)
	}
	if _, err := NewWebDAV(wc); err != nil {
		t.Errorf("应能构造 WebDAV 目标: %v", err)
	}
}

func TestValidate(t *testing.T) {
	r, _, _ := newRepo(t)
	ctx := context.Background()
	cases := []struct {
		name string
		mod  func(*model.BackupRemoteInput)
		want model.FieldErrors
	}{
		{"启用缺主机", func(i *model.BackupRemoteInput) { i.SMBHost = "" }, model.FieldErrors{"smb_host": model.FieldRequired}},
		{"启用缺共享名", func(i *model.BackupRemoteInput) { i.SMBShare = "" }, model.FieldErrors{"smb_share": model.FieldRequired}},
		{"启用缺用户名", func(i *model.BackupRemoteInput) { i.SMBUsername = "" }, model.FieldErrors{"smb_username": model.FieldRequired}},
		{"启用缺密码", func(i *model.BackupRemoteInput) { i.SMBPassword = "" }, model.FieldErrors{"smb_password": model.FieldRequired}},
		{"启用缺口令", func(i *model.BackupRemoteInput) { i.Passphrase = "" }, model.FieldErrors{"passphrase": model.FieldRequired}},
		{"口令过短", func(i *model.BackupRemoteInput) { i.Passphrase = "12345678901" }, model.FieldErrors{"passphrase": model.FieldOutOfRange}},
		{"口令按字符数计", func(i *model.BackupRemoteInput) {
			i.Passphrase = "口令口令口令口令口令口令口令口令口令口令口令"
		}, nil},
		{"类型非法", func(i *model.BackupRemoteInput) { i.Type = "ftp" }, model.FieldErrors{"type": model.FieldInvalid}},
		{"保留份数 0", func(i *model.BackupRemoteInput) { i.Keep = 0 }, model.FieldErrors{"keep": model.FieldOutOfRange}},
		{"保留份数 366", func(i *model.BackupRemoteInput) { i.Keep = 366 }, model.FieldErrors{"keep": model.FieldOutOfRange}},
		{"保留份数 365", func(i *model.BackupRemoteInput) { i.Keep = 365 }, nil},
		{"端口越界", func(i *model.BackupRemoteInput) { i.SMBPort = 70000 }, model.FieldErrors{"smb_port": model.FieldOutOfRange}},
		{"共享名含斜杠", func(i *model.BackupRemoteInput) { i.SMBShare = "a/b" }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"目录含 ..", func(i *model.BackupRemoteInput) { i.SMBDir = "a/../b" }, model.FieldErrors{"smb_dir": model.FieldInvalid}},
		{"主机含空白", func(i *model.BackupRemoteInput) { i.SMBHost = "a b" }, model.FieldErrors{"smb_host": model.FieldInvalid}},
		{"主机含控制字符", func(i *model.BackupRemoteInput) { i.SMBHost = "nas\x00.lan" }, model.FieldErrors{"smb_host": model.FieldInvalid}},
		{"主机含全角空格", func(i *model.BackupRemoteInput) { i.SMBHost = "nas　lan" }, model.FieldErrors{"smb_host": model.FieldInvalid}},
		{"共享名含空白", func(i *model.BackupRemoteInput) { i.SMBShare = "back up" }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"共享名含控制字符", func(i *model.BackupRemoteInput) { i.SMBShare = "bak\x1f" }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"共享名为 ..", func(i *model.BackupRemoteInput) { i.SMBShare = ".." }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"共享名含 ..", func(i *model.BackupRemoteInput) { i.SMBShare = "a..b" }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"SMB 用户名含空白", func(i *model.BackupRemoteInput) { i.SMBUsername = "ad min" }, model.FieldErrors{"smb_username": model.FieldInvalid}},
		{"SMB 用户名首尾空白", func(i *model.BackupRemoteInput) { i.SMBUsername = " admin" }, model.FieldErrors{"smb_username": model.FieldInvalid}},
		{"SMB 用户名含换行", func(i *model.BackupRemoteInput) { i.SMBUsername = "admin\n" }, model.FieldErrors{"smb_username": model.FieldInvalid}},
		{"WebDAV 用户名含空白", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "https://nas.lan/d", "u v", "p"
		}, model.FieldErrors{"webdav_username": model.FieldInvalid}},
		{"WebDAV 用户名含控制字符", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "https://nas.lan/d", "u\x7f", "p"
		}, model.FieldErrors{"webdav_username": model.FieldInvalid}},
		{"未启用也校验用户名格式", func(i *model.BackupRemoteInput) {
			*i = model.BackupRemoteInput{Type: "smb", Keep: 30, WebDAVUsername: "a\tb"}
		}, model.FieldErrors{"webdav_username": model.FieldInvalid}},
		{"未启用允许空必填", func(i *model.BackupRemoteInput) { *i = model.BackupRemoteInput{Type: "smb", Keep: 30} }, nil},
		{"未启用仍校验格式", func(i *model.BackupRemoteInput) { *i = model.BackupRemoteInput{Type: "smb", Keep: 30, SMBShare: "a/b"} }, model.FieldErrors{"smb_share": model.FieldInvalid}},
		{"未启用口令过短也拒绝", func(i *model.BackupRemoteInput) {
			*i = model.BackupRemoteInput{Type: "smb", Keep: 30, Passphrase: "short"}
		}, model.FieldErrors{"passphrase": model.FieldOutOfRange}},
		{"WebDAV 公网 http", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "http://example.com/dav", "u", "p"
		}, model.FieldErrors{"webdav_url": model.FieldInvalid}},
		{"WebDAV 缺地址", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVUsername, i.WebDAVPassword = "webdav", "u", "p"
		}, model.FieldErrors{"webdav_url": model.FieldRequired}},
		{"WebDAV 内嵌凭据", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "https://u:p@nas.lan/d", "u", "p"
		}, model.FieldErrors{"webdav_url": model.FieldInvalid}},
		{"WebDAV 局域网 http", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "http://nas.lan:5005/d", "u", "p"
		}, nil},
		{"只校验当前类型", func(i *model.BackupRemoteInput) {
			i.Type, i.WebDAVURL, i.WebDAVUsername, i.WebDAVPassword = "webdav", "https://nas.lan/d", "u", "p"
			i.SMBHost = ""
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := smbInput()
			c.mod(&in)
			_, err := r.Resolve(ctx, in)
			if c.want == nil {
				if err != nil {
					t.Fatalf("期望通过: %v", err)
				}
				return
			}
			fe := fieldErr(t, err)
			if len(fe) != len(c.want) {
				t.Fatalf("得到 %v，期望 %v", fe, c.want)
			}
			for k, v := range c.want {
				if fe[k] != v {
					t.Errorf("%s = %q，期望 %q", k, fe[k], v)
				}
			}
		})
	}
}

func TestResolveUsesStoredSecretsWithoutSaving(t *testing.T) {
	r, _, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Put(ctx, smbInput()); err != nil {
		t.Fatal(err)
	}
	in := smbInput()
	in.SMBPassword, in.Passphrase, in.SMBHost = "", "", "other.lan"
	cfg, err := r.Resolve(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMBPassword != "smb-secret-pw" || cfg.Passphrase != "correct horse battery" || cfg.SMBHost != "other.lan" {
		t.Errorf("合并不符: %+v", cfg)
	}
	saved, _ := r.Config(ctx)
	if saved.SMBHost != "nas.lan" {
		t.Error("Resolve 不应落库")
	}
}

func TestValidationErrorLeaksNoSecret(t *testing.T) {
	r, _, _ := newRepo(t)
	in := smbInput()
	in.SMBHost = "bad host"
	_, err := r.Put(context.Background(), in)
	if err == nil || strings.Contains(err.Error(), "smb-secret-pw") || strings.Contains(err.Error(), "correct horse") {
		t.Errorf("错误信息不应含明文: %v", err)
	}
}

func TestUploadsRetentionAndLookup(t *testing.T) {
	r, db, clk := newRepo(t)
	ctx := context.Background()
	if last, err := r.LastUpload(ctx, false); err != nil || last != nil {
		t.Fatalf("空表应返回 nil: %v %v", last, err)
	}
	start := clk.Now()
	for i := 0; i < MaxUploads+5; i++ {
		clk.Advance(time.Minute)
		_, err := r.RecordUpload(ctx, model.BackupUpload{
			Name: "n.tar.gz.age", TargetType: "smb", StartedAt: start, FinishedAt: clk.Now(),
			Size: int64(i), Success: i != MaxUploads+4, Reason: map[bool]string{true: "", false: "connect"}[i != MaxUploads+4],
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM backup_uploads`).Scan(&n)
	if n != MaxUploads {
		t.Fatalf("记录数 = %d，期望 %d", n, MaxUploads)
	}
	list, err := r.Uploads(ctx, 10)
	if err != nil || len(list) != 10 || list[0].Size != int64(MaxUploads+4) || list[0].Success || list[0].Reason != "connect" {
		t.Fatalf("列表不符: %+v %v", list, err)
	}
	if all, _ := r.Uploads(ctx, 0); len(all) != MaxUploads {
		t.Errorf("limit 0 应取全部，得到 %d", len(all))
	}
	att, _ := r.LastUpload(ctx, false)
	suc, _ := r.LastUpload(ctx, true)
	if att == nil || att.Success || suc == nil || !suc.Success || suc.Size != int64(MaxUploads+3) {
		t.Errorf("最近尝试/成功不符: %+v %+v", att, suc)
	}
	if !suc.FinishedAt.Equal(clk.Now().Add(-time.Minute)) {
		t.Errorf("时间应往返一致: %v", suc.FinishedAt)
	}
}

func TestUploadReasonFieldsAndLastResultSkipsCanceled(t *testing.T) {
	r, _, clk := newRepo(t)
	ctx := context.Background()
	if got, err := r.LastResult(ctx); err != nil || got != nil {
		t.Fatalf("空表：%v %v", got, err)
	}
	rec := func(success bool, code, detail string) model.BackupUpload {
		u, err := r.RecordUpload(ctx, model.BackupUpload{Name: "a.age", TargetType: "smb", StartedAt: clk.Now(), FinishedAt: clk.Now(),
			Success: success, Reason: model.FormatUploadReason(code, detail)})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ok := rec(true, "", "")
	if ok.Reason != "" || ok.ReasonCode != "" || ok.ReasonDetail != "" {
		t.Fatalf("成功无附注：%+v", ok)
	}
	failed := rec(false, model.UploadReasonAuth, "用户名或密码错误: 被拒")
	if failed.ReasonCode != "auth" || failed.ReasonDetail != "用户名或密码错误: 被拒" {
		t.Fatalf("解析：%+v", failed)
	}
	rec(false, model.UploadReasonCanceled, "")
	got, err := r.LastResult(ctx)
	if err != nil || got == nil || got.ID != failed.ID || got.ReasonCode != "auth" {
		t.Fatalf("应跳过取消记录取到失败记录：%+v %v", got, err)
	}
	list, _ := r.Uploads(ctx, 0)
	if list[0].ReasonCode != model.UploadReasonCanceled || list[1].ReasonDetail == "" {
		t.Fatalf("列表应带解析字段：%+v", list[:2])
	}
}
