# shell-disk-load

用 POSIX Shell 写的 exec 插件示例：读取本机某个路径所在文件系统的使用率和 1 分钟系统负载，不访问网络。

## 文件

- `plugin.yaml`：插件描述，声明配置表单、数据项与小组件。
- `run`：可执行入口，固定名为 `run`，必须有执行权限。

## 配置

| 字段 | 说明 | 默认值 |
|---|---|---|
| `path` | 检查的路径 | `/` |
| `warn_pct` | 使用率达到该值时状态为 warning | 80 |
| `critical_pct` | 使用率达到该值时状态为 critical | 90 |

## 本地试运行

```bash
cd src
go run ./cmd/pimon-hub plugin validate ../examples/plugins/shell-disk-load

echo '{"path": "/"}' > /tmp/cfg.json
go run ./cmd/pimon-hub plugin run ../examples/plugins/shell-disk-load --config /tmp/cfg.json
```

## 安装

把整个目录复制到 hub 的插件目录（目录名必须等于 `id`），目录与 `run` 的属主须是 hub 运行用户或 root，且不能被组或其他用户写。详见 `docs/plugin-development.md`。

## 说明

示例用 `sed` 解析 stdin 的 JSON，只适合简单字符串和数字字段；复杂配置请用 `jq` 或 Python。
