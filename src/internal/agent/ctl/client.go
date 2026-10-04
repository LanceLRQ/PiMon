package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"
	"time"
)

// Client 是控制套接字客户端（CLI 与升级命令使用）；每次调用新建一条连接。
type Client struct {
	// Path 是控制套接字路径。
	Path string
}

// Do 发送一个请求并把应答的 data 解码进 out（out 可为 nil）。
// 套接字不存在或无人监听返回包装了 ErrNotRunning 的错误，没有权限返回包装了 ErrPermission 的错误，
// 守护进程回错误时返回 *RemoteError；ctx 结束时立即返回 ctx.Err()。
func (c Client) Do(ctx context.Context, req Request, out any) error {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, IOTimeout)
	conn, err := d.DialContext(dctx, "unix", c.Path)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.dialError(err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(OpTimeout + 2*IOTimeout))

	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	// 写失败（例如服务端拒绝后已关闭）时仍尝试读应答，拿到拒绝原因。
	_, werr := conn.Write(append(b, '\n'))
	line, err := readLine(conn)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			return fmt.Errorf("发送控制请求: %w", werr)
		}
		return fmt.Errorf("读取控制应答: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("控制应答不是合法的 JSON: %w", err)
	}
	if !resp.OK {
		return &RemoteError{Msg: resp.Error}
	}
	if out != nil && len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("解析控制应答: %w", err)
		}
	}
	return nil
}

func (c Client) dialError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w（控制套接字 %s 不存在或无人监听）", ErrNotRunning, c.Path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w %s", ErrPermission, c.Path)
	}
	return fmt.Errorf("连接控制套接字 %s: %w", c.Path, err)
}

// Status 返回守护进程状态。
func (c Client) Status(ctx context.Context) (Status, error) {
	var st Status
	err := c.Do(ctx, Request{Op: OpStatus}, &st)
	return st, err
}

// Pair 打开拨出配对窗口，返回要打印的内容（含配对码）。
func (c Client) Pair(ctx context.Context) (PairWindow, error) {
	var w PairWindow
	err := c.Do(ctx, Request{Op: OpPair}, &w)
	return w, err
}

// Repin 把 hub 指纹更新为 fp（SHA256:…，由守护进程校验格式）并重连。
func (c Client) Repin(ctx context.Context, fp string) error {
	return c.Do(ctx, Request{Op: OpRepin, FP: fp}, nil)
}

// Reload 让守护进程重新扫描插件目录并重新读取配置。
func (c Client) Reload(ctx context.Context) error {
	return c.Do(ctx, Request{Op: OpReload}, nil)
}

// UpgradeInfo 经守护进程的当前会话向 hub 请求升级信息；未连接 hub 时返回 *RemoteError。
func (c Client) UpgradeInfo(ctx context.Context) (UpgradeInfo, error) {
	var ui UpgradeInfo
	err := c.Do(ctx, Request{Op: OpUpgradeInfo}, &ui)
	return ui, err
}
