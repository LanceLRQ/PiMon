// Package agentdist 提供随 hub 内嵌的 agent 二进制（gzip）与 SHA256SUMS，
// 供下载路由、升级信息与平台列表共用同一份数据。
//
// 分发物由 make agent-dist 写入 dist/：pimon-agent-<os>-<arch>.gz、SHA256SUMS（对未压缩二进制计算，
// 文件名列为 pimon-agent-<os>-<arch>）与 VERSION。未构建时 dist/ 只有 .gitkeep，Platforms 为空。
package agentdist

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// all: 前缀让 .gitkeep 也被收录，未构建 agent 时目录仍存在、Go 侧可编译。
//
//go:embed all:dist
var embedded embed.FS

const (
	filePrefix  = "pimon-agent-"
	sumsName    = "SHA256SUMS"
	versionName = "VERSION"
)

// ErrUnavailable 表示 hub 没有内嵌该平台的 agent（开发构建常见，或内嵌版本与 hub 不一致）。
var ErrUnavailable = errors.New("agentdist: 该平台的 agent 二进制不可用")

// Dist 是一份只读的 agent 分发数据。
type Dist struct {
	fsys      fs.FS
	version   string
	platforms []string
	sums      map[string]string
}

// Embedded 返回打包进二进制的分发数据。
func Embedded() *Dist {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist 由 go:embed 保证存在，不可能走到这里。
		panic(err)
	}
	return NewFS(sub)
}

// NewFS 从给定文件系统（根即 dist 目录）构造，供测试注入。
// 内嵌 VERSION 与 version.Version 不一致时记一次 Warn，并把所有平台视为不可用，
// 以免提供版本不对的二进制（例如直接 go build 而没走 make）。
func NewFS(fsys fs.FS) *Dist {
	d := &Dist{fsys: fsys, sums: map[string]string{}}
	raw, err := fs.ReadFile(fsys, sumsName)
	if err != nil {
		return d
	}
	if v, err := fs.ReadFile(fsys, versionName); err == nil {
		d.version = strings.TrimSpace(string(v))
	}
	if d.version != version.Version {
		slog.Warn("内嵌 agent 版本与 hub 不一致，不提供 agent 下载",
			"embedded", d.version, "hub", version.Version)
		return d
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || !strings.HasPrefix(f[1], filePrefix) {
			continue
		}
		platform := strings.TrimPrefix(f[1], filePrefix)
		if _, err := fs.Stat(fsys, f[1]+".gz"); err != nil {
			continue
		}
		d.sums[platform] = f[0]
	}
	for p := range d.sums {
		d.platforms = append(d.platforms, p)
	}
	sort.Strings(d.platforms)
	return d
}

// Platforms 返回可下载的平台（如 linux-amd64），已排序；没有内嵌数据时为空。
func (d *Dist) Platforms() []string { return append([]string(nil), d.platforms...) }

// Version 返回内嵌分发物的版本（VERSION 文件内容）；没有数据时为空。
func (d *Dist) Version() string { return d.version }

// Checksum 返回平台未压缩二进制的 SHA-256（十六进制）。
func (d *Dist) Checksum(platform string) (string, bool) {
	s, ok := d.sums[platform]
	return s, ok
}

// Checksums 返回平台到 SHA-256 的映射副本。
func (d *Dist) Checksums() map[string]string {
	m := make(map[string]string, len(d.sums))
	for k, v := range d.sums {
		m[k] = v
	}
	return m
}

// SumsFile 返回 SHA256SUMS 文本（仅含可用平台，按平台排序，两个空格分隔）。
func (d *Dist) SumsFile() []byte {
	var b bytes.Buffer
	for _, p := range d.platforms {
		b.WriteString(d.sums[p] + "  " + filePrefix + p + "\n")
	}
	return b.Bytes()
}

// Open 返回平台二进制的解压流与未压缩大小；平台不可用返回 ErrUnavailable。
func (d *Dist) Open(platform string) (io.ReadCloser, int64, error) {
	if _, ok := d.sums[platform]; !ok {
		return nil, 0, ErrUnavailable
	}
	data, err := fs.ReadFile(d.fsys, filePrefix+platform+".gz")
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	if len(data) < 4 {
		return nil, 0, errors.New("agentdist: gzip 文件损坏")
	}
	// gzip 尾部 4 字节是未压缩长度（对 2^32 取模，agent 二进制远小于此）。
	size := int64(binary.LittleEndian.Uint32(data[len(data)-4:]))
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	return zr, size, nil
}
