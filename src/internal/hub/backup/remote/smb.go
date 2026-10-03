package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
)

// 已知风险：SMB 协议层（NTLM 协商、重命名语义、飞牛 NAS 的 Samba 行为）没有可用的测试服务器，
// 本文件对 go-smb2 的调用只经 smbShare 接口用测试替身覆盖上层逻辑；协议层的真实行为
// （登录失败与拒绝访问的 NTSTATUS、Remove 后 Rename 覆盖、大文件写入）留待 M2g 飞牛 SMB 共享真机验收验证。

const (
	smbDefaultPort = 445
	// smbCloseTimeout 是优雅关闭（卸载共享、注销会话）的时限。
	smbCloseTimeout = 5 * time.Second
)

// SMBConfig 是 SMB 目标的配置。
type SMBConfig struct {
	Host string
	// Port 为 0 时用 445。
	Port int
	// Share 是共享名，不含主机与路径分隔符。
	Share string
	// Dir 是共享内存放备份的目录，用 / 分隔，可多级，不得含 ..；为空表示共享根目录。
	Dir      string
	Username string
	Password string
	// Domain 可选。
	Domain string
}

// NTSTATUS 取值（go-smb2 未导出这些常量，只在 ResponseError.Code 里给出数值）。
const (
	statusObjectNameNotFound  uint32 = 0xC0000034
	statusObjectPathNotFound  uint32 = 0xC000003A
	statusNoSuchUser          uint32 = 0xC0000064
	statusWrongPassword       uint32 = 0xC000006A
	statusLogonFailure        uint32 = 0xC000006D
	statusAccountRestriction  uint32 = 0xC000006E
	statusInvalidLogonHours   uint32 = 0xC000006F
	statusInvalidWorkstation  uint32 = 0xC0000070
	statusPasswordExpired     uint32 = 0xC0000071
	statusAccountDisabled     uint32 = 0xC0000072
	statusNoLogonServers      uint32 = 0xC000005E
	statusLogonTypeNotGranted uint32 = 0xC000015B
	statusAccountExpired      uint32 = 0xC0000193
	statusPasswordMustChange  uint32 = 0xC0000224
	statusAccountLockedOut    uint32 = 0xC0000234
	statusAccessDenied        uint32 = 0xC0000022
	statusNetworkAccessDenied uint32 = 0xC00000CA
	statusBadNetworkName      uint32 = 0xC00000CC
	statusMediaWriteProtected uint32 = 0xC00000A2
	statusCannotDelete        uint32 = 0xC0000121
	statusDiskFull            uint32 = 0xC000007F
	statusSharingViolation    uint32 = 0xC0000043
)

// smbShare 是已挂载共享上的操作集合，是 go-smb2 的薄封装，便于测试替身。
// 各方法的 ctx 取消时应尽快返回；path 用 / 分隔、相对共享根，空串表示根目录。
type smbShare interface {
	MkdirAll(ctx context.Context, dir string) error
	Create(ctx context.Context, name string) (io.WriteCloser, error)
	Rename(ctx context.Context, from, to string) error
	Remove(ctx context.Context, name string) error
	ReadDir(ctx context.Context, dir string) ([]fs.FileInfo, error)
	// Abort 不经协议直接切断底层连接，用于打断阻塞中的读写；之后该连接不可再用。
	Abort()
	// Close 优雅卸载共享并注销会话，然后关闭连接。
	Close() error
}

// smbDialFunc 建立到共享的连接；连接超时与 ctx 取消由实现负责。
type smbDialFunc func(ctx context.Context, cfg SMBConfig) (smbShare, error)

// SMB 是基于 go-smb2（NTLM 认证）的 SMB 目标。连接在第一次操作时建立并复用；
// 上传被取消时底层连接会被切断，下一次操作自动重连。
type SMB struct {
	cfg      SMBConfig
	segments []string
	dial     smbDialFunc

	mu    sync.Mutex
	share smbShare
}

var _ Target = (*SMB)(nil)

// NewSMB 校验配置并构造目标，此时不连接。
func NewSMB(cfg SMBConfig) (*SMB, error) {
	return newSMB(cfg, dialSMB)
}

func newSMB(cfg SMBConfig, dial smbDialFunc) (*SMB, error) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, " \t/\\@") {
		return nil, errors.New("SMB 主机地址无效")
	}
	if cfg.Port == 0 {
		cfg.Port = smbDefaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("SMB 端口无效")
	}
	if cfg.Share == "" || strings.ContainsAny(cfg.Share, "/\\") {
		return nil, errors.New("SMB 共享名无效")
	}
	if cfg.Username == "" {
		return nil, errors.New("SMB 用户名不能为空")
	}
	segs := splitSegments(strings.ReplaceAll(cfg.Dir, "\\", "/"))
	for _, s := range segs {
		if s == ".." || s == "." {
			return nil, errors.New("SMB 目录不能含有点号路径段")
		}
	}
	return &SMB{cfg: cfg, segments: segs, dial: dial}, nil
}

func (s *SMB) dirPath() string { return strings.Join(s.segments, "/") }

func (s *SMB) filePath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", fmt.Errorf("非法文件名 %q", name)
	}
	return path.Join(s.dirPath(), name), nil
}

// conn 返回已建立的连接，没有则新建。
func (s *SMB) conn(ctx context.Context) (smbShare, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.share != nil {
		return s.share, nil
	}
	sh, err := s.dial(ctx, s.cfg)
	if err != nil {
		return nil, s.classify(ctx, err)
	}
	s.share = sh
	return sh, nil
}

// drop 切断并丢弃当前连接（若仍是 sh）。
func (s *SMB) drop(sh smbShare) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.share == sh && sh != nil {
		sh.Abort()
		s.share = nil
	}
}

// redact 去掉文本里的用户名、口令与域。
func (s *SMB) redact(msg string) string {
	for _, secret := range []string{s.cfg.Password, s.cfg.Username, s.cfg.Domain} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return scrub(msg)
}

// classify 把 go-smb2 与网络错误显式归类：登录类 NTSTATUS → auth，拒绝访问 → not_writable，
// 传输与拨号错误 → connect，其余 → io。调用方取消原样返回 ctx 错误。
func (s *SMB) classify(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var re *Error
	if errors.As(err, &re) {
		return re
	}
	msg := s.redact(err.Error())
	var rerr *smb2.ResponseError
	if errors.As(err, &rerr) {
		return classifyStatus(rerr.Code, msg, err)
	}
	var terr *smb2.TransportError
	var ne net.Error
	switch {
	case errors.As(err, &terr), errors.As(err, &ne),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return NewError(ReasonConnect, msg, err)
	case errors.Is(err, fs.ErrPermission):
		return NewError(ReasonNotWritable, msg, err)
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return NewError(ReasonConnect, msg, err)
	}
	return NewError(ReasonIO, msg, err)
}

// classifyStatus 按 NTSTATUS 归类。
func classifyStatus(code uint32, msg string, cause error) *Error {
	switch code {
	case statusLogonFailure, statusWrongPassword, statusNoSuchUser, statusAccountDisabled,
		statusAccountLockedOut, statusAccountExpired, statusPasswordExpired, statusPasswordMustChange,
		statusAccountRestriction, statusInvalidLogonHours, statusInvalidWorkstation,
		statusLogonTypeNotGranted, statusNoLogonServers:
		return NewError(ReasonAuth, "用户名或密码被拒绝（"+msg+"）", cause)
	case statusAccessDenied, statusNetworkAccessDenied, statusMediaWriteProtected, statusCannotDelete:
		return NewError(ReasonNotWritable, "没有权限（"+msg+"）", cause)
	case statusBadNetworkName:
		return NewError(ReasonIO, "共享不存在（"+msg+"）", cause)
	case statusDiskFull:
		return NewError(ReasonIO, "远端磁盘空间不足（"+msg+"）", cause)
	default:
		return NewError(ReasonIO, msg, cause)
	}
}

func isNotFound(err error) bool {
	var rerr *smb2.ResponseError
	if errors.As(err, &rerr) {
		return rerr.Code == statusObjectNameNotFound || rerr.Code == statusObjectPathNotFound
	}
	return errors.Is(err, fs.ErrNotExist)
}

// run 取得连接并执行 fn；连接层失败时丢弃连接，下次重连。
func (s *SMB) run(ctx context.Context, fn func(sh smbShare) error) error {
	sh, err := s.conn(ctx)
	if err != nil {
		return err
	}
	if err := fn(sh); err != nil {
		cerr := s.classify(ctx, err)
		var re *Error
		if ctx.Err() != nil || (errors.As(cerr, &re) && re.Reason == ReasonConnect) {
			s.drop(sh)
		}
		return cerr
	}
	return nil
}

// EnsureDir 逐级创建备份目录；已存在不是错误。目录为空（共享根）时只确认能连上。
func (s *SMB) EnsureDir(ctx context.Context) error {
	return s.run(ctx, func(sh smbShare) error {
		if len(s.segments) == 0 {
			return nil
		}
		if err := sh.MkdirAll(ctx, s.dirPath()); err != nil {
			var rerr *smb2.ResponseError
			if errors.As(err, &rerr) && rerr.Code == statusAccessDenied {
				return NewError(ReasonNotWritable, "无法创建目录："+s.redact(err.Error()), err)
			}
			return err
		}
		return nil
	})
}

// Put 写出文件。不设总时限；ctx 取消（含看门狗的无进展取消）时切断底层连接以打断阻塞中的写入。
func (s *SMB) Put(ctx context.Context, name string, r io.Reader) error {
	p, err := s.filePath(name)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	sh, err := s.conn(ctx)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, sh.Abort)
	defer stop()
	werr := func() error {
		f, err := sh.Create(ctx, p)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}()
	if werr == nil {
		if ctx.Err() != nil {
			s.drop(sh)
			return ctx.Err()
		}
		return nil
	}
	cerr := s.classify(ctx, werr)
	var re *Error
	if ctx.Err() != nil || (errors.As(cerr, &re) && re.Reason == ReasonConnect) {
		s.drop(sh)
	}
	return cerr
}

// Rename 改名；SMB 的改名不覆盖已存在的目标，所以先删除正式名再改名（非原子，调用方串行使用）。
func (s *SMB) Rename(ctx context.Context, from, to string) error {
	src, err := s.filePath(from)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	dst, err := s.filePath(to)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	return s.run(ctx, func(sh smbShare) error {
		if err := sh.Remove(ctx, dst); err != nil && !isNotFound(err) {
			return err
		}
		return sh.Rename(ctx, src, dst)
	})
}

// List 列出备份目录的直接子项。
func (s *SMB) List(ctx context.Context) ([]Entry, error) {
	var out []Entry
	err := s.run(ctx, func(sh smbShare) error {
		infos, err := sh.ReadDir(ctx, s.dirPath())
		if err != nil {
			if isNotFound(err) {
				return NewError(ReasonIO, "列目录：备份目录不存在", err)
			}
			return err
		}
		out = out[:0]
		for _, fi := range infos {
			if n := fi.Name(); n == "." || n == ".." {
				continue
			}
			out = append(out, Entry{Name: fi.Name(), Size: fi.Size(), IsDir: fi.IsDir()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Remove 删除文件；文件不存在视为成功。
func (s *SMB) Remove(ctx context.Context, name string) error {
	p, err := s.filePath(name)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	return s.run(ctx, func(sh smbShare) error {
		if err := sh.Remove(ctx, p); err != nil && !isNotFound(err) {
			return err
		}
		return nil
	})
}

// Close 优雅关闭连接。
func (s *SMB) Close() error {
	s.mu.Lock()
	sh := s.share
	s.share = nil
	s.mu.Unlock()
	if sh == nil {
		return nil
	}
	return sh.Close()
}

// realShare 是 go-smb2 的 smbShare 实现。
type realShare struct {
	tcp     net.Conn
	session *smb2.Session
	share   *smb2.Share
}

// dialSMB 建立 TCP 连接并完成 NTLM 协商、会话建立与共享挂载。
// 15 秒超时同时约束 TCP 拨号（net.Dialer.Timeout）与其后的整个握手（ctx 超时，
// 并在超时或取消时关闭连接，避免握手读写阻塞）。
func dialSMB(ctx context.Context, cfg SMBConfig) (smbShare, error) {
	ctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	d := net.Dialer{Timeout: ConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	sd := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: cfg.Username, Password: cfg.Password, Domain: cfg.Domain}}
	sess, err := sd.DialContext(ctx, conn)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	sh, err := sess.WithContext(ctx).Mount(cfg.Share)
	if err != nil {
		stop()
		_ = sess.Logoff()
		_ = conn.Close()
		return nil, err
	}
	if !stop() {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	return &realShare{tcp: conn, session: sess, share: sh}, nil
}

func (r *realShare) MkdirAll(ctx context.Context, dir string) error {
	return r.share.WithContext(ctx).MkdirAll(dir, 0o755)
}

func (r *realShare) Create(ctx context.Context, name string) (io.WriteCloser, error) {
	return r.share.WithContext(ctx).Create(name)
}

func (r *realShare) Rename(ctx context.Context, from, to string) error {
	return r.share.WithContext(ctx).Rename(from, to)
}

func (r *realShare) Remove(ctx context.Context, name string) error {
	return r.share.WithContext(ctx).Remove(name)
}

func (r *realShare) ReadDir(ctx context.Context, dir string) ([]fs.FileInfo, error) {
	return r.share.WithContext(ctx).ReadDir(dir)
}

func (r *realShare) Abort() { _ = r.tcp.Close() }

func (r *realShare) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), smbCloseTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = r.tcp.Close() })
	defer stop()
	_ = r.share.WithContext(ctx).Umount()
	_ = r.session.WithContext(ctx).Logoff()
	return r.tcp.Close()
}

