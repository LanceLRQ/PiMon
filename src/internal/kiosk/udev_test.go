package kiosk

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeRdev 模拟 eventN 的设备号：主设备号 13，次设备号 64+N。
func fakeRdev(path string) (uint32, uint32, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "event"))
	if err != nil {
		return 0, 0, err
	}
	return 13, uint32(64 + n), nil //nolint:gosec // 测试用小整数
}

const (
	udevMouse    = "S:input/by-id/mouse\nE:ID_INPUT=1\nE:ID_INPUT_MOUSE=1\n"
	udevTouch    = "E:ID_INPUT=1\nE:ID_INPUT_TOUCHSCREEN=1\nE:ID_INPUT_WIDTH_MM=154\n"
	udevTouchOff = "E:ID_INPUT=1\nE:ID_INPUT_TOUCHSCREEN=0\n"
)

func TestDetectTouch_有触摸屏(t *testing.T) {
	root := t.TempDir()
	in, udev := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, in, map[string]string{"event0": "", "event3": "", "mouse0": "", "js0": "", "eventX": ""})
	// event0 -> c13:64，event3 -> c13:67
	writeFiles(t, udev, map[string]string{"c13:64": udevMouse, "c13:67": udevTouch})
	got := DetectTouchWith(in, udev, fakeRdev)
	want := TouchResult{Known: true, Touch: true, Devices: []string{filepath.Join(in, "event3")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestDetectTouch_无触摸屏(t *testing.T) {
	root := t.TempDir()
	in, udev := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, in, map[string]string{"event0": "", "event1": ""})
	writeFiles(t, udev, map[string]string{"c13:64": udevMouse, "c13:65": udevTouchOff})
	got := DetectTouchWith(in, udev, fakeRdev)
	if !got.Known || got.Touch || len(got.Devices) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectTouch_读不到udev数据库为未知(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "input")
	writeFiles(t, in, map[string]string{"event0": "", "event1": ""})
	if got := DetectTouchWith(in, filepath.Join(root, "no-udev"), fakeRdev); got.Known || got.Touch {
		t.Fatalf("udev 目录不存在应为未知: %+v", got)
	}
	// 目录存在但没有任何设备条目也算未知
	empty := filepath.Join(root, "udev-empty")
	writeFiles(t, empty, map[string]string{})
	if got := DetectTouchWith(in, empty, fakeRdev); got.Known {
		t.Fatalf("没有可读条目应为未知: %+v", got)
	}
}

func TestDetectTouch_没有输入设备为未知(t *testing.T) {
	root := t.TempDir()
	if got := DetectTouchWith(filepath.Join(root, "nope"), root, fakeRdev); got.Known {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectTouch_部分设备读不到仍按可读部分判定(t *testing.T) {
	root := t.TempDir()
	in, udev := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, in, map[string]string{"event0": "", "event1": ""})
	writeFiles(t, udev, map[string]string{"c13:65": udevTouch})
	got := DetectTouchWith(in, udev, fakeRdev)
	if !got.Known || !got.Touch || len(got.Devices) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestTouchResult_Touchscreen指针(t *testing.T) {
	if (TouchResult{}).Touchscreen() != nil {
		t.Fatal("未知应为 nil")
	}
	if v := (TouchResult{Known: true}).Touchscreen(); v == nil || *v {
		t.Fatal("已知无触摸应为 false")
	}
	if v := (TouchResult{Known: true, Touch: true}).Touchscreen(); v == nil || !*v {
		t.Fatal("已知有触摸应为 true")
	}
}

func TestDetectTouch_次设备号取自stat而不是按64加N推算(t *testing.T) {
	root := t.TempDir()
	in, udev := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, in, map[string]string{"event3": "", "event40": ""})
	// event40 的真实次设备号是 200（不是 104）；event3 的是 67。
	writeFiles(t, udev, map[string]string{"c13:67": udevMouse, "c13:200": udevTouch, "c13:104": udevMouse})
	rdev := func(path string) (uint32, uint32, error) {
		switch filepath.Base(path) {
		case "event3":
			return 13, 67, nil
		case "event40":
			return 13, 200, nil
		}
		return 0, 0, errors.New("unexpected")
	}
	got := DetectTouchWith(in, udev, rdev)
	want := TouchResult{Known: true, Touch: true, Devices: []string{filepath.Join(in, "event40")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestDetectTouch_stat失败的设备视为未知(t *testing.T) {
	root := t.TempDir()
	in, udev := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, in, map[string]string{"event0": ""})
	writeFiles(t, udev, map[string]string{"c13:64": udevTouch})
	got := DetectTouchWith(in, udev, func(string) (uint32, uint32, error) { return 0, 0, errors.New("stat 失败") })
	if got.Known || got.Touch || len(got.Devices) != 0 {
		t.Fatalf("stat 失败应为未知: %+v", got)
	}
}
