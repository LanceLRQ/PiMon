package install

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestXcursorBytesLayout(t *testing.T) {
	b := xcursorBytes()
	if len(b) != 15776 {
		t.Fatalf("总长 = %d，期望 15776", len(b))
	}
	head := []byte{
		0x58, 0x63, 0x75, 0x72, 0x10, 0, 0, 0, 0, 0, 1, 0, 3, 0, 0, 0,
		0x02, 0, 0xfd, 0xff, 0x18, 0, 0, 0, 0x34, 0, 0, 0,
		0x02, 0, 0xfd, 0xff, 0x20, 0, 0, 0, 0x58, 0x09, 0, 0,
		0x02, 0, 0xfd, 0xff, 0x30, 0, 0, 0, 0x7c, 0x19, 0, 0,
		0x24, 0, 0, 0, 0x02, 0, 0xfd, 0xff, 0x18, 0, 0, 0, 1, 0, 0, 0, 0x18, 0, 0, 0, 0x18, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	if !bytes.Equal(b[:len(head)], head) {
		t.Fatalf("文件头不一致:\n got %x\nwant %x", b[:len(head)], head)
	}
	for _, c := range []struct{ pos, size int }{{2392, 32}, {6524, 48}} {
		if got := binary.LittleEndian.Uint32(b[c.pos+16:]); int(got) != c.size {
			t.Errorf("位置 %d 的宽 = %d，期望 %d", c.pos, got, c.size)
		}
	}
	for i := 52; i < len(b); i++ {
		// 像素区全 0；图像块头部的非 0 字节只在块头里，这里只检查最后一张图的像素段
		if i >= 6524+36 && b[i] != 0 {
			t.Fatalf("偏移 %d 的像素非 0", i)
		}
	}
}

func TestCursorNames(t *testing.T) {
	if len(cursorNames) != 39 {
		t.Fatalf("光标名数量 = %d，期望 39", len(cursorNames))
	}
	seen := map[string]bool{}
	for _, n := range cursorNames {
		if seen[n] {
			t.Errorf("重复的光标名 %s", n)
		}
		seen[n] = true
	}
	for _, n := range []string{"left_ptr", "resize_", "wait", "w-resize"} {
		if !seen[n] {
			t.Errorf("缺少光标名 %s", n)
		}
	}
}
