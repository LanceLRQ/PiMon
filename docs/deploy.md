# PiMon 部署指南

本文说明如何把 PiMon 中枢（hub）部署到一台树莓派上，并让它驱动本机的 HDMI 小屏。涉及的命令都在树莓派上执行。

目录：

- [硬件与系统准备](#硬件与系统准备)
- [安装](#安装)
- [升级](#升级)
- [卸载](#卸载)
- [备份与恢复](#备份与恢复)
- [忘记密码](#忘记密码)
- [HTTPS 与 nginx 反向代理](#https-与-nginx-反向代理)
- [kiosk 与屏幕](#kiosk-与屏幕)
- [常见问题](#常见问题)

## 硬件与系统准备

### 推荐配置

- 树莓派 4B，系统 Raspberry Pi OS Trixie 64 位（桌面环境为 labwc，登录管理器为 lightdm，并开启桌面自动登录）。
- 给树莓派设固定 IP（在路由器里做 DHCP 绑定，或在系统里配静态地址），方便浏览器和其他设备稳定访问。
- 推荐从 USB SSD 启动。hub 会持续写入历史数据与备份，SD 卡的写入寿命有限，长期运行容易损坏。这是推荐而不是要求，用 SD 卡也能运行，请保持每日备份开启并定期把备份下载到别处。
- 使用官方 5V/3A 电源。电压不足会导致黑屏、重启或 USB 设备掉线。可以用下面的命令检查，输出 `throttled=0x0` 表示正常：

  ```bash
  vcgencmd get_throttled
  ```

  非 0 的值里，最低位（`0x1`）表示当前欠压，`0x50000` 表示开机以来发生过欠压和降频。

### 时间同步

树莓派没有实时时钟（RTC），开机初期的系统时间可能不准，联网后由 NTP 校正。请确认时间同步已开启：

```bash
timedatectl
```

输出里应有 `System clock synchronized: yes`。时区请与 PiMon「设置」里的时区保持一致，时段计划、每日备份和日志时间都按它计算。

### 显示器

- 在显示器自带的 OSD 菜单里关闭「无信号自动关机」「节能」之类的选项。树莓派用软件方式关屏后，显示器会检测到无信号，开启这些选项会让它进入待机而唤不醒。
- 一些屏幕不提供 EDID（分辨率信息），树莓派无法自动识别。这时在 `/boot/firmware/cmdline.txt` 的**同一行末尾**追加一段空格加 `video=HDMI-A-1:<宽>x<高>@60D`，保存后重启。例如 10 寸 1280×800 的屏：

  ```text
  ... rootwait video=HDMI-A-1:1280x800@60D
  ```

  `cmdline.txt` 只能有一行，不要换行。修改前先备份：`sudo cp /boot/firmware/cmdline.txt /boot/firmware/cmdline.txt.bak`。
- 只有一个 Type-C 口、供电和触摸数据共用同一根线的触摸屏，不能直接接树莓派的 USB 口供电。请使用带外接电源的 USB HUB，或者供电与数据分离的线，否则会出现供电不足、触摸时断时续。
- 外接键盘时，布局默认是 `gb`。需要其他布局时，在 `/etc/xdg/labwc/environment` 里修改 `XKB_DEFAULT_LAYOUT`（例如改成 `us`），重新登录后生效。

## 安装

### 获取二进制

下载与树莓派对应的二进制（linux/arm64 的 `pimon-hub`），放到树莓派上，并加上可执行权限：

```bash
chmod +x ./pimon-hub
```

### 只部署 hub

```bash
sudo ./pimon-hub install
```

### 同时配置本机屏幕（kiosk）

```bash
sudo ./pimon-hub install --kiosk
```

`--kiosk` 在上面的基础上，还会配置本机桌面会话，让小屏自动显示 PiMon。它需要知道桌面用户：默认读取 lightdm 的自动登录用户；检测不到或想指定别人时加 `--desktop-user`：

```bash
sudo ./pimon-hub install --kiosk --desktop-user <桌面用户>
```

install 需要 root，只支持 Linux + systemd。运行 install 期间，请不要同时在桌面会话里修改该用户家目录下的 labwc 配置。install 可以反复运行（幂等）。

### install 做了什么

每一步输出一行 `[完成]`、`[已存在]`、`[跳过]` 或 `[警告]`，失败时输出 `[失败]` 和原因，并列出已经完成的步骤。步骤依次是：

1. 检测运行环境（Linux、systemd、root）。
2. 创建系统用户 `pimon` 和数据目录 `/var/lib/pimon`（权限 0750）。
3. 把自身安装为 `/usr/local/bin/pimon-hub`。
4. 检查 31415 端口是否空闲。
5. 生成首次设置码（已有管理员则跳过）。
6. 写入 `/etc/systemd/system/pimon-hub.service`，启用并启动服务，等待 `/healthz` 就绪。
7. 把桌面用户加入 `pimon` 组，使其能读取屏幕令牌（`/var/lib/pimon/screen.token`）。
8. 检查 `net.ipv4.ping_group_range` 是否包含 `pimon` 组（Ping 探测需要）；不包含时写入 `/etc/sysctl.d/99-pimon.conf` 并生效。

加了 `--kiosk` 之后，还会：

1. 在桌面用户的 `~/.config/labwc/autostart` 末尾追加 kiosk 启动行，并删除其中的 swayidle 行（系统空闲息屏）。
2. 删除登录界面 `/etc/xdg/labwc-greeter/autostart` 里的 swayidle 行。
3. 安装透明鼠标指针主题到 `~/.icons/pimon-hidden`，并把 `~/.config/labwc/environment` 里的 `XCURSOR_THEME` 设为 `pimon-hidden`。
4. 写入并启用会话看门狗服务 `pimon-session-watchdog`（见 [kiosk 与屏幕](#kiosk-与屏幕)）。

改动已有文件前，install 会在文件旁保存备份 `<文件>.pimon-bak-<UTC 时间>`，被删除的 swayidle 原行会打印出来。如果 autostart 里还有别的疑似 kiosk 的启动行（例如你之前手工配置的），install 只会警告，不会删除，请自行确认是否清理，否则会重复启动。

### 设置码与首次设置

install 结束时会打印首次设置码和访问地址，例如：

```text
========================================================
  PiMon 安装完成
  首次设置码：ABCD-2345（有效期至 2026-10-03 10:00:00）
  访问地址：
    http://192.168.1.50:31415
========================================================
```

设置码过期或没记下来时，重新生成（尚未设置管理员时有效）：

```bash
sudo -u pimon pimon-hub setup-code
```

用浏览器打开访问地址，输入设置码，创建管理员账号。

首次设置必须直连 `http://<树莓派 IP>:31415` 完成。如果经 HTTPS 反代访问，页面的 Origin 协议与 hub 看到的不一致，请求会被拒绝。设置完成后，再按下文配置 nginx。

### 让 --kiosk 生效

`--kiosk` 改的是桌面会话配置，需要桌面用户重新登录后才生效；桌面用户刚加入 `pimon` 组，也要重新登录才能读到屏幕令牌。最简单的方法是重启树莓派，或者：

```bash
sudo systemctl restart lightdm
```

这会结束当前桌面会话，随后自动登录并启动 kiosk。

### 验证

```bash
systemctl status pimon-hub
curl -s http://127.0.0.1:31415/healthz
```

## 升级

用新版本的二进制再运行一次 install：

```bash
sudo ./pimon-hub install
```

它会自动替换 `/usr/local/bin/pimon-hub`、重启 hub 服务。数据库需要升级时，hub 启动前会先自动备份一份（原因为 `pre-upgrade`）。如果已配置 kiosk，屏幕守护进程发现 hub 版本变化后，会自动换用新版本，无需手工处理。升级到 kiosk 配置有变化的版本时，用 `sudo ./pimon-hub install --kiosk` 再运行一次。

## 卸载

下面的命令按顺序执行。`<桌面用户>` 是配置过 kiosk 的桌面用户，没有配置 kiosk 的话跳过相关步骤。

1. 停止并禁用服务，删除 unit：

   ```bash
   sudo systemctl disable --now pimon-hub pimon-session-watchdog
   sudo rm -f /etc/systemd/system/pimon-hub.service /etc/systemd/system/pimon-session-watchdog.service
   sudo systemctl daemon-reload
   ```

   没有安装过会话看门狗时，`disable` 会提示该服务不存在，忽略即可。

2. 结束本机屏幕守护进程，并从桌面用户的 labwc 配置里还原：

   ```bash
   pkill -f 'pimon-hub kiosk'
   ```

   - 编辑 `~/.config/labwc/autostart`，删除包含 `pimon-hub kiosk` 的那一行。
   - install 删除过 swayidle 行的文件（`~/.config/labwc/autostart` 与 `/etc/xdg/labwc-greeter/autostart`）旁边有 `autostart.pimon-bak-*` 备份。需要恢复系统空闲息屏时，把备份里的 swayidle 行加回对应文件。
   - `~/.config/labwc/environment` 同样有 `environment.pimon-bak-*` 备份，按它还原 `XCURSOR_THEME`。

3. 删除透明鼠标指针主题和 kiosk 的浏览器配置：

   ```bash
   rm -rf ~/.icons/pimon-hidden ~/.local/state/pimon
   ```

   上面的命令以桌面用户身份执行。确认不再需要后，也可以删除各处的 `.pimon-bak-*` 备份文件。

4. 把桌面用户移出 `pimon` 组：

   ```bash
   sudo gpasswd -d <桌面用户> pimon
   ```

5. 数据目录里是你的全部配置和历史数据。删除前先备份，需要的话把 `/var/lib/pimon/backups` 下的备份文件拷到别处，再删除：

   ```bash
   sudo rm -rf /var/lib/pimon
   ```

6. 删除用户、二进制和可能存在的 sysctl 配置：

   ```bash
   sudo userdel pimon
   sudo rm -f /usr/local/bin/pimon-hub
   sudo rm -f /etc/sysctl.d/99-pimon.conf
   ```

   安装时如果没有写过 `/etc/sysctl.d/99-pimon.conf`，最后一条命令什么也不会做。

## 备份与恢复

### 自动备份

hub 每天在设定时刻（默认 04:00，按「设置」里的时区）把整库备份到 `/var/lib/pimon/backups`，默认保留最近 7 份，时刻和份数可以在设置里修改。升级前还会额外做一份 `pre-upgrade` 备份。备份文件名形如 `pimon-backup-20261002-200000-daily.tar.gz`（时间为 UTC）。

在网页管理端的备份页面可以立即备份，也可以下载备份文件。建议定期把备份下载或拷贝到树莓派之外的地方，尤其是使用 SD 卡时。

### 恢复

恢复必须先停服务，并且以 `pimon` 身份运行。以 root 运行会被拒绝（否则会产生 root 属主的文件，让服务打不开数据库）；服务还在运行时也会被拒绝。

```bash
sudo systemctl stop pimon-hub
sudo -u pimon pimon-hub restore /var/lib/pimon/backups/pimon-backup-20261002-200000-daily.tar.gz
sudo systemctl start pimon-hub
```

备份文件必须是 `pimon` 用户能读取的位置。从电脑上传来的备份，请先放到 `/var/lib/pimon/backups/`（`sudo cp` 之后 `sudo chown pimon:pimon`），或者放在所有人可读的目录，不要放在某个用户的家目录里。

恢复过程会先校验备份内容，再把当前的数据库和密钥改名为 `*.pre-restore` 保留，换入备份里的文件，成功后才删除这些旧文件。如果恢复中途失败，会自动回退到原来的文件。

凡是直接操作数据目录的命令（`setup-code`、`reset-password`、`restore`），一律用 `sudo -u pimon` 运行。

## 忘记密码

管理员忘记密码时，在树莓派上重置：

```bash
sudo -u pimon pimon-hub reset-password
```

按提示输入两次新密码（至少 8 个字符，输入不回显）。成功后输出「管理员密码已更新，所有已登录的管理会话已失效」，用新密码重新登录即可。重置密码时服务可以保持运行。

## HTTPS 与 nginx 反向代理

hub 默认通过明文 HTTP 提供服务。登录密码和会话 Cookie 在局域网里以明文传输，同一网络里的其他设备可以嗅探到。只在完全信任的家庭网络里使用 HTTP；需要在不可信网络或经公网访问时，请用 HTTPS。

推荐用 nginx 反向代理提供 HTTPS。示例配置在仓库的 [`deploy/nginx/pimon.conf`](../deploy/nginx/pimon.conf)，里面有 HTTP 与 HTTPS 两个示例。使用要点：

1. 把示例放到 `/etc/nginx/conf.d/pimon.conf`，修改域名和证书路径，执行 `sudo nginx -t && sudo systemctl reload nginx`。
2. WebSocket（路径 `/ws`）需要 Upgrade 头，并且是长连接，示例里已把读写超时设为 3600 秒。
3. 示例设置了 `X-Forwarded-For`、`X-Forwarded-Proto`、`X-Forwarded-Host`。其中 `X-Forwarded-Host` 用 `$http_host`（带端口），不要改成 `$host`，否则非默认端口下 Origin 校验会不匹配。
4. **必须**到 PiMon 的「设置 → 网络与安全 → 受信任反代」里添加 nginx 所在地址：nginx 与 hub 在同一台机器时，添加 `127.0.0.1` 与 `::1`；在别的机器上时，添加那台机器的 IP。hub 只对受信任的来源采信 `X-Forwarded-*`。不添加的话，登录失败锁定会把所有人当成同一个来源（nginx 的地址），HTTPS 下的 Secure Cookie 也不会生效。
5. 首次设置要直连 hub 完成（见上文），之后才能配置受信任反代。

hub 设置里还有一个「直连 HTTPS」开关，使用自签名证书。**有本机屏幕时请不要打开它**：本机 kiosk 屏幕通过 `http://127.0.0.1:31415` 连接 hub，开启后屏幕会断开。需要 HTTPS 就用 nginx。

## kiosk 与屏幕

### kiosk 做什么

`--kiosk` 安装后，桌面用户登录时由 labwc 自动启动 `pimon-hub kiosk`。它是一个常驻守护进程，负责：

- 拉起 Chromium 全屏显示 PiMon 屏幕页，并用屏幕令牌自动登录。
- Chromium 崩溃后自动重启；连续崩溃时间隔从 1 秒起逐次加倍，最长 60 秒，稳定运行 5 分钟后恢复。
- 屏幕令牌或界面缩放变化时自动重启 Chromium；开启「每日重启」后，到点重启 Chromium（在「设置」的屏幕显示参数里配置）。
- 按「设置」里的时段计划开关屏，使用 `wlopm` 控制 HDMI 输出。
- 关屏期间触摸屏幕即可唤醒（需要触摸屏）。
- hub 升级后自动换用新版本的二进制。
- 桌面会话消失（labwc 退出）时，守护进程自己退出。

kiosk 的日志在 systemd journal 里，tag 是 `pimon-kiosk`：

```bash
journalctl -t pimon-kiosk -e
journalctl -t pimon-kiosk -f
```

hub 与看门狗的日志：

```bash
journalctl -u pimon-hub -e
journalctl -u pimon-session-watchdog -e
```

### 会话看门狗

图形会话本身出问题（labwc 崩溃、没有自动登录）时，kiosk 守护进程也跟着不在了。`pimon-session-watchdog` 是一个以 root 运行的 systemd 服务，每 30 秒检查一次桌面用户的图形会话是否正常；连续两次检查失败，就重启 lightdm，让桌面自动重新登录。

为避免陷入重启循环，它有几层保护：

- 开机启动后有 90 秒宽限期，期间不检查。
- 每次重启 lightdm 后冷却 5 分钟。
- 每小时最多重启 3 次，超过后只记日志、不再重启。
- 只有在 lightdm 配置的自动登录用户就是 `--kiosk` 所用的桌面用户时才生效；不匹配时它只记一条日志然后空转。

### 息屏检查告警

屏幕不应被系统自己的空闲息屏（swayidle）关掉，因为开关屏由 PiMon 的时段计划统一管理。`install --kiosk` 会删除桌面用户与登录界面 autostart 里的 swayidle 行。kiosk 守护进程启动时和之后每小时会再检查一次，用户的 autostart、登录界面 autostart、系统的 `/etc/xdg/labwc/autostart` 三处有 swayidle 行，或者 swayidle 进程在运行，管理端系统页就会告警「检测到 swayidle 被重新启用」。

手工处理：编辑告警里指出的、含 swayidle 行的 autostart 文件，删除其中的 swayidle 行；如果 swayidle 进程还在运行，执行 `pkill swayidle` 或重启树莓派。桌面升级后系统文件可能被恢复，`/etc/xdg/labwc/autostart` 里的行 install 不会代为修改，需要自己删除。

## 常见问题

### 31415 端口被占用

install 会检查端口，被占用时输出占用者的进程号和程序，类似：

```text
[失败] 检查端口：端口 31415 已被占用：pid 777（/usr/bin/python3）
```

结束该进程，或者在设置里改用别的端口后再运行 install。可以用下面的命令自己查：

```bash
sudo ss -ltnp | grep 31415
```

### 屏幕黑屏

依次检查：

1. 看 kiosk 日志有没有报错：`journalctl -t pimon-kiosk -e`。
2. 屏幕是否被软件关了：在桌面用户的终端里执行 `wlopm`，看输出是 `on` 还是 `off`；手动打开用 `wlopm --on '*'`。时段计划设了夜间关屏时，黑屏可能是预期行为。
3. 显示器 OSD 里的「无信号自动关机」「节能」是否关闭。
4. 识别不到屏幕或分辨率不对时，检查 `/boot/firmware/cmdline.txt` 里的 `video=` 参数（见上文）。
5. 检查电源：`vcgencmd get_throttled`，欠压会导致黑屏和重启。
6. 图形会话没起来时，看 `systemctl status lightdm` 和 `journalctl -u pimon-session-watchdog -e`。

### Chromium 处于降级或退避状态

管理端系统页显示 Chromium 重启次数或退避中，说明 Chromium 在反复崩溃。看 `journalctl -t pimon-kiosk -e` 里的原因。常见原因是屏幕令牌文件读不到（桌面用户刚加入 `pimon` 组、还没有重新登录）、内存不足、`/usr/bin/chromium` 不存在。排除后无需手工操作，守护进程会按退避间隔自己重试，稳定运行 5 分钟后退避清零。

### 欠压

`vcgencmd get_throttled` 返回非 0，或者桌面右上角出现闪电图标，说明电源或线材不足。换官方 5V/3A 电源和质量好的线，检查有没有把耗电的 USB 设备接在树莓派上（触摸屏、SSD 建议用带电源的 USB HUB）。

### kiosk 配置之后没有生效

确认已经重新登录桌面（或 `sudo systemctl restart lightdm`），并检查 `~/.config/labwc/autostart` 里有 `pimon-hub kiosk` 那一行，`id <桌面用户>` 的输出里有 `pimon` 组。
