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
