package hostmetrics

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
)

// 三类机器的分区样本。macOS 样本取自开发机 gopsutil 的真实返回（已抹掉用户名、地址与卷名）；
// 飞牛与 Ubuntu 样本按两类系统常见的 /proc/mounts 布局整理，不是真机录制。
var machineCases = []struct {
	name  string
	parts []Partition
	want  []string
}{
	{
		name: "飞牛 NAS：存储卷、Docker 数据目录、Btrfs 子卷、overlay 与 NFS",
		parts: []Partition{
			{Mount: "/", FSType: "ext4", Device: "/dev/mapper/trim-root"},
			{Mount: "/boot/efi", FSType: "vfat", Device: "/dev/nvme0n1p1"},
			{Mount: "/vol1", FSType: "btrfs", Device: "/dev/mapper/trim_vg-vol1"},
			{Mount: "/vol1/1000", FSType: "btrfs", Device: "/dev/mapper/trim_vg-vol1"},
			{Mount: "/vol1/@appcenter", FSType: "btrfs", Device: "/dev/mapper/trim_vg-vol1"},
			{Mount: "/vol1/docker", FSType: "btrfs", Device: "/dev/mapper/trim_vg-vol1"},
			{Mount: "/var/lib/docker", FSType: "btrfs", Device: "/dev/mapper/trim_vg-vol1"},
			{Mount: "/var/lib/docker/overlay2/abc/merged", FSType: "overlay", Device: "overlay"},
			{Mount: "/mnt/remote", FSType: "nfs4", Device: "192.0.2.5:/export"},
		},
		want: []string{"disk[/]", "disk[/vol1]"},
	},
	{
		name: "Ubuntu x86：snap 的 squashfs、/boot/efi、loop 设备",
		parts: []Partition{
			{Mount: "/", FSType: "ext4", Device: "/dev/sda2"},
			{Mount: "/boot", FSType: "ext4", Device: "/dev/sda3"},
			{Mount: "/boot/efi", FSType: "vfat", Device: "/dev/sda1"},
			{Mount: "/snap/core22/1380", FSType: "squashfs", Device: "/dev/loop0", ReadOnly: true},
			{Mount: "/snap/snapd/21759", FSType: "squashfs", Device: "/dev/loop1", ReadOnly: true},
			{Mount: "/data", FSType: "xfs", Device: "/dev/sdb1"},
			{Mount: "/mnt/share", FSType: "cifs", Device: "//192.0.2.9/share"},
		},
		want: []string{"disk[/]", "disk[/boot]", "disk[/data]"},
	},
	{
		name: "macOS：只读系统卷、/System/Volumes/*、Data 数据卷、smbfs、只读磁盘映像",
		parts: []Partition{
			{Mount: "/", FSType: "apfs", Device: "/dev/disk3s1s1", ReadOnly: true},
			{Mount: "/dev", FSType: "devfs", Device: "devfs"},
			{Mount: "/System/Volumes/VM", FSType: "apfs", Device: "/dev/disk3s6"},
			{Mount: "/System/Volumes/Preboot", FSType: "apfs", Device: "/dev/disk3s2"},
			{Mount: "/System/Volumes/Update", FSType: "apfs", Device: "/dev/disk3s4"},
			{Mount: "/System/Volumes/xarts", FSType: "apfs", Device: "/dev/disk1s2"},
			{Mount: "/System/Volumes/iSCPreboot", FSType: "apfs", Device: "/dev/disk1s1"},
			{Mount: "/System/Volumes/Hardware", FSType: "apfs", Device: "/dev/disk1s3"},
			{Mount: "/System/Volumes/Data", FSType: "apfs", Device: "/dev/disk3s5"},
			{Mount: "/System/Volumes/Data/home", FSType: "autofs", Device: "map auto_home"},
			{Mount: "/Volumes/外置盘", FSType: "apfs", Device: "/dev/disk7s1"},
			{Mount: "/Volumes/共享", FSType: "smbfs", Device: "//user@192.0.2.9/share"},
			{Mount: "/Volumes/App 1.0", FSType: "hfs", Device: "/dev/disk4s1", ReadOnly: true},
		},
		want: []string{"disk[/System/Volumes/Data]", "disk[/Volumes/外置盘]"},
	},
}

func TestMachinePartitionFiltering(t *testing.T) {
	for _, c := range machineCases {
		t.Run(c.name, func(t *testing.T) {
			s := diskSampler(c.parts...)
			rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
			if got := diskKeys(rep); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("磁盘条目 = %v，期望 %v", got, c.want)
			}
			assertValidAgainstManifest(t, New(s), rep)
		})
	}
}

func TestRealMountNetworkAndReadOnly(t *testing.T) {
	for _, c := range []struct {
		p    Partition
		want bool
	}{
		{Partition{Mount: "/Volumes/x", FSType: "smbfs"}, false},
		{Partition{Mount: "/mnt/x", FSType: "NFS4"}, false},
		{Partition{Mount: "/mnt/x", FSType: "fuse.sshfs"}, false},
		{Partition{Mount: "/mnt/x", FSType: "ext4", ReadOnly: true}, false},
		{Partition{Mount: "/boot/efi", FSType: "vfat"}, false},
		{Partition{Mount: "/boot/efi/EFI", FSType: "vfat"}, false},
		{Partition{Mount: "/boot/firmware", FSType: "vfat"}, true},
		{Partition{Mount: "/System/Volumes/Data", FSType: "apfs"}, true},
	} {
		if got := realMount(c.p); got != c.want {
			t.Errorf("%+v 应为 %v", c.p, c.want)
		}
	}
}

// 非树莓派读不到欠压标志时不输出 throttled；温度读不到时也不输出（前端显示未知），都不能是 0。
func TestNonPiOmitsThrottledAndMissingTempIsNotZero(t *testing.T) {
	s := diskSampler(Partition{Mount: "/", FSType: "ext4", Device: "/dev/sda1"})
	s.throttleErr, s.tempErr = errNA, errNA
	rep := run(t, New(s), clock.NewFake(time.Unix(1_800_000_000, 0)), "", nil)
	if rep.Find("throttled") != nil {
		t.Fatal("非树莓派不应输出 throttled")
	}
	if rep.Find("temp") != nil {
		t.Fatal("温度读不到时不应输出 temp（不能写 0）")
	}
}

// host-metrics 在设备配对时以空配置自动创建，schema 不得有必填项，否则自动创建永不重试。
func TestConfigSchemaHasNoRequiredFields(t *testing.T) {
	m := New(baseSampler()).Manifest()
	for _, f := range m.ConfigSchema {
		if f.Required {
			t.Errorf("配置项 %s 不得必填", f.Key)
		}
	}
	if _, errs := schema.Prepare(m.ConfigSchema, map[string]any{}); errs != nil {
		t.Fatalf("空配置应校验通过: %v", errs)
	}
}

func TestCommandRunner(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte("throttled=0x5\n")}, nil, "vcgencmd", "get_throttled")
	f.Set(cmdexec.Result{Stdout: []byte("x"), ExitCode: 1}, nil, "bad")
	f.Set(cmdexec.Result{}, cmdexec.ErrSignaled, "sig")
	run := commandRunner(f)
	ctx := context.Background()
	if out, err := run(ctx, "vcgencmd", "get_throttled"); err != nil || string(out) != "throttled=0x5\n" {
		t.Fatalf("成功命令应返回 stdout: %q %v", out, err)
	}
	if _, err := run(ctx, "bad"); err == nil {
		t.Fatal("非 0 退出应按失败处理，行为与原 exec.Output 一致")
	}
	if _, err := run(ctx, "sig"); err == nil {
		t.Fatal("被信号终止应返回错误")
	}
	if v, err := readThrottled(ctx, throttleIO{run: run, readFile: func(string) ([]byte, error) { return nil, errNA }}); err != nil || v != 5 {
		t.Fatalf("经 cmdexec 读取失败: %v %v", v, err)
	}
}
