package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

// Backend 是控制套接字背后的守护进程操作（*daemon.Daemon 实现它）。
type Backend interface {
	Status() Status
	// OpenPairWindow 只在拨出模式有效；拨入模式返回的错误文本应提示用 install --code 重新配对。
	OpenPairWindow(ctx context.Context) (PairWindow, error)
	Repin(ctx context.Context, pin certs.SPKI) error
	Reload(ctx context.Context) error
	// UpgradeInfo 没有就绪会话时返回「未连接 hub」。
	UpgradeInfo(ctx context.Context) (UpgradeInfo, error)
}

// ServerConfig 是控制套接字服务端的配置。
type ServerConfig struct {
	// Path 是套接字文件路径；父目录不存在时以 0700 创建，存在时不得让属组或其他用户可写。
	Path    string
	Backend Backend
	// Log 缺省 slog.Default()。
	Log *slog.Logger
	// PeerUID 读取对端 uid，缺省用平台对端凭据（Linux SO_PEERCRED、macOS LOCAL_PEERCRED）；测试可注入。
	PeerUID func(*net.UnixConn) (int, error)
	// Authorize 判定 uid 是否可以访问，缺省只允许与本进程相同的有效用户和 root。
	Authorize func(uid int) bool
}

// Server 是控制套接字服务端：Listen 创建，Serve 运行。
type Server struct {
	cfg  ServerConfig
	log  *slog.Logger
	ln   *net.UnixListener
	sem  chan struct{}
	once sync.Once
}

// staleDialTimeout 是探测旧套接字是否仍有进程监听的超时。
const staleDialTimeout = time.Second

// defaultAuthorize 只允许 euid 与 root。
func defaultAuthorize(euid int) func(int) bool {
	return func(uid int) bool { return uid >= 0 && (uid == euid || uid == 0) }
}

// Listen 准备父目录、清理残留的旧套接字（先确认没有进程在监听）、监听并把套接字文件设为 0600。
func Listen(cfg ServerConfig) (*Server, error) {
	if cfg.Backend == nil {
		return nil, errors.New("控制套接字缺少 Backend")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.PeerUID == nil {
		cfg.PeerUID = peerUID
	}
	if cfg.Authorize == nil {
		cfg.Authorize = defaultAuthorize(os.Geteuid())
	}
	path := cfg.Path
	if n := len(path); n == 0 || n > maxPathLen {
		return nil, fmt.Errorf("控制套接字路径 %q 长 %d 字节，本平台上限 %d 字节", path, n, maxPathLen)
	}
	if err := prepareDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("监听控制套接字 %s（路径 %d 字节）: %w", path, len(path), err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("设置控制套接字权限: %w", err)
	}
	return &Server{cfg: cfg, log: cfg.Log, ln: ln, sem: make(chan struct{}, MaxConns)}, nil
}

// prepareDir 确认套接字所在目录存在且不能被属组或其他用户写入（否则别人可以替换套接字）。
func prepareDir(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建控制套接字目录: %w", err)
		}
		fi, err = os.Stat(dir)
	}
	if err != nil {
		return fmt.Errorf("检查控制套接字目录: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("控制套接字目录 %s 不是目录", dir)
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("控制套接字目录 %s 权限过宽（%04o），属组或其他用户可写，请执行: chmod go-w %s", dir, perm, dir)
	}
	return nil
}

// removeStale 删除没有进程监听的旧套接字；仍有进程监听时报错，不是套接字的文件不碰。
func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查控制套接字 %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s 已存在且不是套接字，拒绝覆盖", path)
	}
	if c, err := net.DialTimeout("unix", path, staleDialTimeout); err == nil {
		_ = c.Close()
		return fmt.Errorf("控制套接字 %s 已有进程在监听（守护进程已在运行？）", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("删除残留的控制套接字 %s: %w", path, err)
	}
	return nil
}

// Close 关闭监听（Serve 未运行时用它释放套接字）；可重复调用。
func (s *Server) Close() error {
	var err error
	s.once.Do(func() { err = s.ln.Close() })
	return err
}

// Serve 接受连接直到 ctx 结束或 Close，返回前取消并等待全部在途请求；监听关闭时删除套接字文件。
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	var wg sync.WaitGroup
	backoff := 10 * time.Millisecond
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			s.log.Warn("控制套接字接受连接失败", "err", err)
			select { // 文件描述符耗尽之类的错误：短暂退避（真实时间，属系统操作）
			case <-time.After(backoff):
				backoff = min(2*backoff, time.Second)
				continue
			case <-ctx.Done():
			}
			break
		}
		backoff = 10 * time.Millisecond
		select {
		case s.sem <- struct{}{}:
		default:
			s.log.Debug("控制套接字连接过多，关闭新连接")
			_ = c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			s.handle(ctx, c)
		}()
	}
	cancel()
	_ = s.Close()
	wg.Wait()
	return nil
}

// handle 处理一个连接：校验对端 uid、读一行请求、执行、写一行应答后关闭。ctx 结束时连接随之关闭。
func (s *Server) handle(ctx context.Context, c *net.UnixConn) {
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	uid, err := s.cfg.PeerUID(c)
	if err != nil || !s.cfg.Authorize(uid) {
		s.log.Warn("拒绝控制套接字连接：对端不是同一用户或 root", "uid", uid, "err", err)
		s.reply(c, Response{Error: "无权访问控制套接字：只允许运行 agent 的用户或 root"})
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(IOTimeout))
	line, err := readLine(c)
	if err != nil {
		if errors.Is(err, errTooLarge) || errors.Is(err, errEmpty) {
			s.reply(c, Response{Error: err.Error()})
		}
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.reply(c, Response{Error: "请求不是合法的 JSON"})
		return
	}
	if req.Op == "" {
		s.reply(c, Response{Error: "请求缺少 op"})
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, OpTimeout)
	resp := s.dispatch(opCtx, req)
	cancel()
	s.reply(c, resp)
}

// dispatch 执行一个操作。应答数据（含配对码）不写日志。
func (s *Server) dispatch(ctx context.Context, req Request) Response {
	s.log.Debug("控制套接字请求", "op", clip(req.Op, 64))
	switch req.Op {
	case OpStatus:
		return result(s.cfg.Backend.Status(), nil)
	case OpPair:
		return result(s.cfg.Backend.OpenPairWindow(ctx))
	case OpRepin:
		pin, err := certs.ParseSPKI(req.FP)
		if err != nil {
			return Response{Error: "指纹格式无效：" + err.Error()}
		}
		return ack(s.cfg.Backend.Repin(ctx, pin))
	case OpReload:
		return ack(s.cfg.Backend.Reload(ctx))
	case OpUpgradeInfo:
		return result(s.cfg.Backend.UpgradeInfo(ctx))
	default:
		return Response{Error: fmt.Sprintf("未知操作 %q", clip(req.Op, 64))}
	}
}

// ack 把没有数据的操作结果转成应答。
func ack(err error) Response {
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

// result 把带数据的操作结果转成应答。
func result[T any](data T, err error) Response {
	if err != nil {
		return Response{Error: err.Error()}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return Response{Error: "编码应答失败：" + err.Error()}
	}
	return Response{OK: true, Data: b}
}

func (s *Server) reply(c *net.UnixConn, r Response) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(IOTimeout))
	if _, err := c.Write(append(b, '\n')); err != nil {
		s.log.Debug("写控制套接字应答失败", "err", err)
	}
}
