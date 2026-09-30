package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Restore 把备份包还原到 dbPath 与 secretPath。必须在中枢停止后调用。
// 包内只允许出现 pimon.db 与 secret.key 两个普通文件，缺一不可。
// 先解压到同目录的 .restore 临时文件，全部成功后删除旧的 -wal/-shm 并 rename 替换；
// 任何一步失败都不会改动现有文件。
func Restore(archivePath, dbPath, secretPath string) (err error) {
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
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开备份包: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
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
		if err = extract(tmp, tr); err != nil {
			return fmt.Errorf("解压 %s: %w", hdr.Name, err)
		}
	}
	for name := range targets {
		if !seen[name] {
			return fmt.Errorf("备份缺少 %s", name)
		}
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除旧的 %s: %w", suffix, err)
		}
	}
	for name, target := range targets {
		if err = os.Rename(temps[name], target); err != nil {
			return fmt.Errorf("替换 %s: %w", name, err)
		}
	}
	return nil
}

func extract(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
