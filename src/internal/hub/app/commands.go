package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
)

var (
	// ErrAdminExists 表示已设置管理员，不需要设置码。
	ErrAdminExists = errors.New("已设置管理员，不需要设置码")
	// ErrNoAdmin 表示尚未设置管理员，无法重置密码。
	ErrNoAdmin = errors.New("尚未设置管理员，请先在网页完成首次设置")
)

// SetupCode 生成新的首次设置码，返回码与到期时间；已有管理员时返回 ErrAdminExists。
func (a *App) SetupCode(ctx context.Context) (string, time.Time, error) {
	exists, err := a.admins.Exists(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if exists {
		return "", time.Time{}, ErrAdminExists
	}
	return a.setupCodes.Generate(ctx)
}

// ResetPassword 从 in 读取新密码并替换管理员密码，同时删除全部管理员会话。
// isTerminal 为真时 in 必须是 *os.File，读取时不回显并要求输入两次；
// 否则读取一行。提示写入 out，密码不会写到 out。
func (a *App) ResetPassword(ctx context.Context, in io.Reader, out io.Writer, isTerminal bool) error {
	exists, err := a.admins.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNoAdmin
	}
	pw, err := readNewPassword(in, out, isTerminal)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(pw) < auth.MinPasswordLen {
		return fmt.Errorf("密码至少 %d 个字符", auth.MinPasswordLen)
	}
	hash, err := auth.Hasher{Params: a.opts.params}.Hash(pw)
	if err != nil {
		return err
	}
	if err := a.admins.SetPasswordHash(ctx, hash); err != nil {
		return err
	}
	if err := a.sessions.DeleteKind(ctx, auth.KindAdmin); err != nil {
		return fmt.Errorf("删除旧会话: %w", err)
	}
	_, _ = fmt.Fprintln(out, "管理员密码已更新，所有已登录的管理会话已失效")
	return nil
}

func readNewPassword(in io.Reader, out io.Writer, isTerminal bool) (string, error) {
	if !isTerminal {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", fmt.Errorf("读取新密码: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	f, ok := in.(*os.File)
	if !ok {
		return "", errors.New("终端输入必须是文件描述符")
	}
	ask := func(prompt string) (string, error) {
		_, _ = fmt.Fprint(out, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(out)
		return string(b), err
	}
	first, err := ask("新密码: ")
	if err != nil {
		return "", fmt.Errorf("读取新密码: %w", err)
	}
	second, err := ask("再次输入: ")
	if err != nil {
		return "", fmt.Errorf("读取新密码: %w", err)
	}
	if first != second {
		return "", errors.New("两次输入的密码不一致")
	}
	return first, nil
}

// Restore 用备份包覆盖数据库与密钥。不打开数据库，服务必须已停止。
func Restore(cfg config.Config, archive string, out io.Writer) error {
	_, _ = fmt.Fprintln(out, "注意：请先停止服务，恢复后重新启动（本命令不会检查服务是否仍在运行）")
	if err := backup.Restore(archive, cfg.DBPath(), cfg.SecretKeyPath()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "恢复完成，请重新启动服务")
	return nil
}
