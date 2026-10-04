# testdata 出处

smartctl 的 JSON 输出是设备与工具产生的事实数据。下列文件中的序列号、WWN 等标识已删除，其余内容保持原样（另有说明的除外）。

| 文件 | 来源 | 说明 |
|---|---|---|
| `smart-nvme-macos.json` | 本机实录：macOS 25.6、smartctl 7.5，`smartctl -j -a disk0`（无需 sudo） | Apple NVMe；`exit_status` 为 4（bit2，读 Error Log 失败），数据完整。已删 `serial_number` |
| `smart-ata-healthy.json` | [Scrutiny](https://github.com/AnalogJ/scrutiny)（MIT）`webapp/backend/pkg/models/testdata/smart-ata.json` | 健康 SATA 盘（`-d sat`），smartctl 7.0。已删 `serial_number`、`wwn` |
| `smart-ata-failing.json` | Scrutiny（MIT）`.../testdata/smart-fail2.json` | USB JMicron 桥接的失败盘：`smart_status.passed=false`，重映射 1975、待定 8，`exit_status` 216（bit3/4/6/7）。已删 `serial_number`、`wwn` |
| `smart-usb-sat-partial.json` | Scrutiny（MIT）`.../testdata/smart-sat.json` | USB SAT 盘的 `smartctl -jA` 输出：缺 `smart_status` 与 `power_on_time`，用来验证缺失字段判未知 |
| `smart-nvme-media-errors.json` | Scrutiny（MIT）`.../testdata/smart-nvme-failed.json` | NVMe `media_errors` 为 7。已删 `serial_number` |
| `smart-nvme-degraded.json` | 由 `smart-nvme-macos.json` 改写（构造样本） | `critical_warning=4`（可靠性下降）、`available_spare` 5 低于阈值 10、`percentage_used` 97、温度 72℃，设备名改为 `/dev/nvme0` |
| `smart-standby.json` | 按 smartmontools 源码构造（`ataprint.cpp` 的 `ataPrintMain`：`-n standby` 且盘处于 STANDBY 时 `jinf("Device is in %s mode, exit(%d)")` 并返回 `powerexit`，默认值 `FAILPOWER` = 2；同时写 `power_mode{ata_value,name}`；`jinf` 在 `--json` 下进入 `smartctl.messages`，severity 为 `information`） | 休眠中的 SATA 盘 |
| `smart-open-failed.json` | 按 Scrutiny `smart-fail.json` 的形状改写为 Linux 设备（构造样本） | 打开设备失败：`exit_status` 2，`messages` 带 severity `error` |
| `scan-open-linux.txt` | 按 smartmontools 源码 `smartctl.cpp` 的 `scan_devices` 输出格式构造 | 正常行 `<设备> -d <类型> # <info>, <协议> device`；打开失败的行以 `# ` 开头并带 `open failed:` |

smartmontools 源码参考：<https://github.com/smartmontools/smartmontools>（`smartctl.cpp`、`ataprint.cpp`、`scsiprint.cpp`、`nvmeprint.cpp`、`smartctl.h` 的退出位定义）。

## RAID 与 Btrfs 样本（D3b）

全部是按公开资料与文档构造的样本（设备名、UUID 为示例值），真机输出在 M2g 验收时补录。

| 文件 | 依据 |
|---|---|
| `mdstat-fnos.txt` | 飞牛 fnOS 单盘 RAID1：`docs/_internal/research/02-飞牛fnOS存储结构调研.md`（单盘也建 RAID1、`--metadata=1.2`、阵列名 `fnOS:0`）；`/proc/mdstat` 格式见 Linux 内核文档 `Documentation/admin-guide/md.rst` 与 mdadm 手册 |
| `mdstat-none.txt` | 无 md 阵列时内核输出（Personalities 行为空、`unused devices: <none>`），即普通 Ubuntu 的 mdstat |
| `mdstat-degraded.txt` | 降级阵列：成员带 `(F)`、`[2/1] [U_]`，mdadm 文档与内核 md 驱动输出格式 |
| `mdstat-rebuilding.txt` | 重建中阵列：`recovery = 12.6% (...) finish=...min speed=...K/sec`；另含 `resync=DELAYED`（内核 `md_seq_show`） |
| `mdstat-check.txt` | 例行 `check`（Debian `checkarray` 触发）进度行 |
| `mdstat-inactive.txt` | 研究文档所述异常特征：`inactive`、成员均为 `(S)`（断电后 RAID5 不识别） |
| `mdadm-detail-fnos.txt` / `mdadm-detail-degraded.txt` / `mdadm-detail-rebuilding.txt` | `mdadm --detail` 的输出格式（mdadm 手册 `--detail`、`Detail.c`）；`Name : fnOS:0  (local to host fnOS)`、`State : clean, degraded, recovering` |
| `mdadm-detail-notstarted.txt` | 研究文档：`State : active, FAILED, Not Started` |
| `mounts-fnos.txt` | fnOS 的 `/dev/mapper/trim_…-0` 挂在 `/vol1`，加两个子卷挂载（含 `\040` 转义的挂载点）以测去重；格式见 `proc(5)` |
| `mounts-ubuntu.txt` | 普通 Ubuntu（ext4，无 btrfs） |
| `btrfs-device-stats-*.txt` | `btrfs device stats` 固定格式 `[<设备>].<计数名> <值>`（btrfs-progs `cmds/device.c`） |
| `btrfs-scrub-new-*.txt` | btrfs-progs 5.x/6.x 的 `btrfs scrub status` 格式（`cmds/scrub.c`：`Scrub started:`、`Status:`、`Error summary:` 及 `csum=N` 加 `Corrected:`/`Uncorrectable:`/`Unverified:` 明细）；`aborted` 为中止的一次 |
| `btrfs-scrub-old-*.txt` | btrfs-progs 4.x 格式：`scrub started at … and finished after …`、`total bytes scrubbed: … with N errors`、`error details:` |
| `btrfs-scrub-never.txt` | 从未执行：`no stats available` |

参考：mdadm <https://github.com/md-raid-utilities/mdadm>，btrfs-progs <https://github.com/kdave/btrfs-progs>，内核 md 文档 <https://docs.kernel.org/admin-guide/md.html>。
