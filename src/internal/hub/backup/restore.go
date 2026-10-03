package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"io"
	"os"
	"path/filepath"
)

// rename 是文件改名函数，测试中替换以注入替换阶段的失败。
var rename = os.Rename

// maxEntrySize 是单个备份条目解压后的大小上限，防止损坏或恶意的备份包撑满磁盘。
var maxEntrySize int64 = 4 << 30

const sqliteMagic = "SQLite format 3\x00"

// ErrPassphraseOrCorrupt 表示加密备份无法解开：口令错误，或文件已损坏、被截断。
var ErrPassphraseOrCorrupt = errors.New("口令错误或文件损坏")

// PassphraseFunc 在确认备份已加密后才被调用，返回备份口令。
type PassphraseFunc func() (string, error)

// Restore 把备份文件还原到 dbPath 与 secretPath，只接受未加密的备份；遇到 age 加密文件时报错。
// 需要处理加密备份时用 RestoreFile。
func Restore(archivePath, dbPath, secretPath string) error {
	return RestoreFile(archivePath, dbPath, secretPath, nil)
}

// RestoreFile 打开 archivePath 并还原；按文件头识别 age 加密备份，此时调用 askPass 取口令并流式解密，
// 不在数据目录落解密后的临时文件。askPass 为 nil 时遇到加密备份报错；明文备份不会调用它。
// 口令错误、密文损坏或被截断统一返回 ErrPassphraseOrCorrupt，且不会改动现有文件。
func RestoreFile(archivePath, dbPath, secretPath string, askPass PassphraseFunc) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开备份包: %w", err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(f)
	head, _ := br.Peek(len(ageMagic))
	if !IsAgeHeader(head) {
		return RestoreFrom(br, dbPath, secretPath)
	}
	if askPass == nil {
		return errors.New("备份文件已加密，需要提供备份口令")
	}
	pass, err := askPass()
	if err != nil {
		return err
	}
	if pass == "" {
		return errors.New("备份口令不能为空")
	}
	dec, err := DecryptReader(br, pass)
	if err != nil {
		if errors.Is(err, ErrWrongPassphrase) {
			return ErrPassphraseOrCorrupt
		}
		return fmt.Errorf("%w: %v", ErrPassphraseOrCorrupt, err)
	}
	tr := &errTrackReader{r: dec}
	if err := RestoreFrom(tr, dbPath, secretPath); err != nil {
		if tr.err != nil {
			return fmt.Errorf("%w: %v", ErrPassphraseOrCorrupt, tr.err)
		}
		return err
	}
	return nil
}

// errTrackReader 记录底层读取遇到的第一个非 EOF 错误，用来区分「解密失败」与「备份内容不合法」。
type errTrackReader struct {
	r   io.Reader
	err error
}

func (t *errTrackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && t.err == nil {
		t.err = err
	}
	return n, err
}

// RestoreFrom 从 gzip 压缩的 tar 流 r 还原到 dbPath 与 secretPath。必须在中枢停止后调用。
// 包内只允许出现 pimon.db 与 secret.key 两个普通文件，缺一不可。
// 先把两个条目都解压并校验（密钥恰为 secret.KeySize 字节、数据库以 SQLite 文件头开头、
// 单条目不超过 maxEntrySize）到同目录的 .restore 临时文件，并把整个流读到结尾（含 gzip 校验和与
// 上游加密流的最后一块认证）；该阶段失败不会改动现有文件。
// 随后把现有的数据库、-wal、-shm、密钥改名为 .pre-restore 作回退，再把新文件 rename 就位；
// 替换阶段失败时把 .pre-restore 改回原名，成功后删除它们。
func RestoreFrom(r io.Reader, dbPath, secretPath string) (err error) {
	targets := map[string]string{entryDB: dbPath, entrySecret: secretPath}
	temps := map[string]string{entryDB: dbPath + ".restore", entrySecret: secretPath + ".restore"}
	defer func() {
		if err != nil {
			for _, t := range temps {
				_ = os.Remove(t)
			}
		}
	}()

	for _, p := range targets {
		if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return fmt.Errorf("创建目录: %w", err)
		}
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("读取备份包: %w", err)
	}
	tr := tar.NewReader(gz)

	seen := map[string]bool{}
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("读取备份条目: %w", nextErr)
		}
		tmp, ok := temps[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("备份包含未知条目 %q", hdr.Name)
		}
		if seen[hdr.Name] {
			return fmt.Errorf("备份条目 %q 重复", hdr.Name)
		}
		seen[hdr.Name] = true
		if hdr.Size > maxEntrySize {
			return fmt.Errorf("备份条目 %s 大小 %d 超过上限 %d", hdr.Name, hdr.Size, maxEntrySize)
		}
		if err = extract(tmp, tr); err != nil {
			return fmt.Errorf("解压 %s: %w", hdr.Name, err)
		}
		if err = validateEntry(hdr.Name, tmp); err != nil {
			return err
		}
	}
	if _, err = io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("读取备份包结尾: %w", err)
	}
	for name := range targets {
		if !seen[name] {
			return fmt.Errorf("备份缺少 %s", name)
		}
	}
	if err = swapIn(targets, temps, dbPath, secretPath); err != nil {
		return err
	}
	syncDir(filepath.Dir(dbPath))
	if d := filepath.Dir(secretPath); d != filepath.Dir(dbPath) {
		syncDir(d)
	}
	return nil
}

// validateEntry 校验已解压的临时文件内容。
func validateEntry(name, path string) error {
	switch name {
	case entrySecret:
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		if fi.Size() != secret.KeySize {
			return fmt.Errorf("备份中的密钥长度为 %d 字节，应为 %d 字节", fi.Size(), secret.KeySize)
		}
	case entryDB:
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = fh.Close() }()
		head := make([]byte, len(sqliteMagic))
		if _, err := io.ReadFull(fh, head); err != nil || string(head) != sqliteMagic {
			return errors.New("备份中的数据库不是有效的 SQLite 文件")
		}
	}
	return nil
}

// swapIn 把旧文件改名为 .pre-restore，再让临时文件就位；失败时回退，成功时删除回退文件。
func swapIn(targets, temps map[string]string, dbPath, secretPath string) error {
	olds := []string{dbPath, dbPath + "-wal", dbPath + "-shm", secretPath}
	var moved []string
	rollback := func() error {
		var errs []error
		for _, name := range []string{entryDB, entrySecret} {
			// 新文件若已就位则撤掉，以免回退时覆盖失败。
			if _, err := os.Stat(temps[name]); errors.Is(err, os.ErrNotExist) {
				if err := os.Remove(targets[name]); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
			}
		}
		for _, p := range moved {
			if err := rename(p+".pre-restore", p); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	// fail 回退并组合错误；回退失败时明确告知旧文件仍在 .pre-restore，避免静默丢数据。
	fail := func(err error) error {
		if rerr := rollback(); rerr != nil {
			return fmt.Errorf("%w（回退也失败，旧文件仍在 *.pre-restore，请手工改回原名后再启动服务：%v）", err, rerr)
		}
		return err
	}
	for _, p := range olds {
		if err := rename(p, p+".pre-restore"); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fail(fmt.Errorf("保留旧文件 %s: %w", filepath.Base(p), err))
		}
		moved = append(moved, p)
	}
	for _, name := range []string{entryDB, entrySecret} {
		if err := rename(temps[name], targets[name]); err != nil {
			return fail(fmt.Errorf("替换 %s: %w", name, err))
		}
	}
	for _, p := range olds {
		_ = os.Remove(p + ".pre-restore")
	}
	return nil
}

func extract(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, maxEntrySize+1))
	if err == nil && n > maxEntrySize {
		err = fmt.Errorf("解压内容超过大小上限 %d", maxEntrySize)
	}
	if err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// syncDir 尽力对目录 fsync，让 rename 落盘；失败不影响恢复结果。
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
