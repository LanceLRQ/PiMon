# python-file-watch

用 Python（仅标准库）写的 exec 插件示例：报告一个本地文件的大小、行数以及较上次运行的增长量，不访问网络。

## 文件

- `plugin.yaml`：插件描述。
- `run`：可执行入口，带 `#!/usr/bin/env python3`，必须有执行权限。

## 配置

| 字段 | 说明 |
|---|---|
| `path` | 必填，要监视的文件 |
| `max_bytes` | 可选，文件超过该大小时状态为 warning |
| `token` | 可选密钥，仅用于演示密钥字段；它在 stdin 的 `secrets.token` 里，本示例不使用 |

## 演示要点

- 读取 stdin 的 `config`、`secrets`、`state`。
- 用报告的 `state` 保存上次文件大小，下次运行从 stdin 的 `state` 取回，算出增长量。
- 文件读不了时仍以退出码 0 输出 `critical` 报告，而不是崩溃；只有插件自身出错才用非 0 退出码。

## 本地试运行

```bash
cd src
go run ./cmd/pimon-hub plugin validate ../examples/plugins/python-file-watch

echo '{"path": "/etc/hosts"}' > /tmp/cfg.json
go run ./cmd/pimon-hub plugin run ../examples/plugins/python-file-watch --config /tmp/cfg.json
```

`plugin run` 每次都是全新的一次运行，不带上次的 `state`，所以增长量总是 0。
