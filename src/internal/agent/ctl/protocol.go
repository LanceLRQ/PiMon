// Package ctl 是 agent 本地控制套接字：一行一个 JSON 的请求/应答协议、服务端（权限与对端 uid 校验）与客户端。
// 它只做编解码与权限，具体操作交给守护进程（Backend）。
//
// 请求 {"op":"status"|"pair"|"repin"|"reload"|"upgrade_info","fp":"SHA256:…"}，
// 应答 {"ok":true,"data":{…}} 或 {"ok":false,"error":"…"}；单行上限 64 KiB，每个连接只处理一个请求。
// M3 的 Hooks 中继复用同一个套接字，所以 op 是字符串，未知 op 只回错误、不影响监听。
package ctl

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"
)

// 操作。
const (
	OpStatus      = "status"
	OpPair        = "pair"
	OpRepin       = "repin"
	OpReload      = "reload"
	OpUpgradeInfo = "upgrade_info"
)

const (
	// MaxLine 是单行请求或应答（不含换行）的字节上限。
	MaxLine = 64 << 10
	// IOTimeout 是读请求、写应答各自的超时。
	IOTimeout = 5 * time.Second
	// OpTimeout 是单个操作的执行上限（upgrade_info 向 hub 请求最多 10 秒）。
	OpTimeout = 15 * time.Second
	// MaxConns 是同时处理的连接上限，超出的连接直接关闭。
	MaxConns = 16
)

// Request 是一行请求。
type Request struct {
	Op string `json:"op"`
	// FP 是 repin 的新指纹（SHA256:…）。
	FP string `json:"fp,omitempty"`
}

// Response 是一行应答。
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Incompatible 是 hub 判定不兼容时给出的信息。
type Incompatible struct {
	Reason      string `json:"reason"`
	RequiredMin int    `json:"required_min"`
	HubVersion  string `json:"hub_version"`
}

// Status 是守护进程的状态快照（status 的 data）。不含凭据、凭据哈希与配对码。
type Status struct {
	Mode   string `json:"mode"`
	HubURL string `json:"hub_url,omitempty"`
	// Pin 是拨入模式钉扎的 hub SPKI 指纹。
	Pin string `json:"pin,omitempty"`
	// Listen 是拨出模式的监听地址（已就绪时为实际地址）。
	Listen    string `json:"listen,omitempty"`
	Connected bool   `json:"connected"`
	// Since 是当前会话收到 hello_ok 的时刻，未连接为零值。
	Since time.Time `json:"since,omitzero"`
	// LastError 是最近错误或停止原因（如「已被吊销，需要重新配对」）。
	LastError string `json:"last_error,omitempty"`
	// State 取 running|revoked|bad_credential|config_error|incompatible|idle。
	State string `json:"state"`
	// Incompatible 只在 State 为 incompatible 时非空。
	Incompatible *Incompatible `json:"incompatible,omitempty"`
	// Instances 是当前会话最近一次 assign 的实例数。
	Instances            int    `json:"instances"`
	Version              string `json:"version"`
	ProtocolVersion      int    `json:"protocol_version"`
	MinCompatibleVersion int    `json:"min_compatible_version"`
	// PairWindowExpiresAt 是拨出配对窗口的到期时刻，没有打开的窗口为零值。
	PairWindowExpiresAt time.Time `json:"pair_window_expires_at,omitzero"`
}

// PairWindow 是打开的拨出配对窗口（pair 的 data），供 pair 命令打印。
// Code 只在这次应答与终端输出中出现，不写日志、不进状态。
type PairWindow struct {
	Code        string    `json:"code"`
	Fingerprint string    `json:"fingerprint"`
	Port        int       `json:"port"`
	IPs         []string  `json:"ips"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LogValue 隐去配对码。
func (w PairWindow) LogValue() slog.Value {
	return slog.GroupValue(slog.String("fingerprint", w.Fingerprint), slog.Int("port", w.Port),
		slog.Time("expires_at", w.ExpiresAt))
}

// UpgradeInfo 是 hub 经 agent 通道告知的升级信息（upgrade_info 的 data，C7 用它下载并校验）。
type UpgradeInfo struct {
	HubVersion string `json:"hub_version"`
	WebPort    int    `json:"web_port"`
	// SHA256 是本平台 agent 二进制的 SHA-256 十六进制；hub 未内嵌本平台时为空。
	SHA256 string `json:"sha256"`
	// HubHost 是当前会话对端的 IP（经 mDNS 兜底连上时比配置里的地址更准）。
	HubHost string `json:"hub_host"`
}

// 错误。
var (
	// ErrNotRunning 表示控制套接字不存在或没有进程监听（守护进程未运行）。
	ErrNotRunning = errors.New("守护进程未运行")
	// ErrPermission 表示没有权限连接控制套接字（系统模式下请用 sudo）。
	ErrPermission = errors.New("没有权限访问控制套接字")
	errTooLarge   = errors.New("请求超过 64 KiB")
	errEmpty      = errors.New("请求为空")
)

// RemoteError 是守护进程返回的错误（{"ok":false,"error":…}）。
type RemoteError struct{ Msg string }

func (e *RemoteError) Error() string { return e.Msg }

// readLine 读一行（不含换行），内容超过 MaxLine 返回 errTooLarge；内存占用以 MaxLine 为界。
// 对端写完不带换行就关闭写端时，读到的内容也算一行。
func readLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(io.LimitReader(r, MaxLine+1))
	line, err := br.ReadBytes('\n')
	switch {
	case err == nil:
		line = line[:len(line)-1]
	case errors.Is(err, io.EOF):
		if len(line) > MaxLine {
			return nil, errTooLarge
		}
	default:
		return nil, err
	}
	line = []byte(strings.TrimRight(string(line), "\r"))
	if len(line) == 0 {
		return nil, errEmpty
	}
	return line, nil
}

// clip 把对端可控的字符串截到 n 字节并保持 UTF-8 合法，再进日志或错误文本。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
