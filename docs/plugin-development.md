# PiMon 插件开发指南

本文面向想给 PiMon 写数据源插件的开发者，介绍 exec 插件（任意语言的可执行文件）的目录约定、协议、`plugin.yaml` 字段、报告格式，以及随 `pimon-hub` 附带的开发工具。

PiMon 有三种插件形态：内置 Go 插件、exec 插件、HTTP 插件（由内置的 `http-json` 承载，无需描述文件）。自己开发的一般是 exec 插件，本文只讲它。插件只产出数据并声明小组件，不带前端代码；屏幕上的显示由 PiMon 用通用模板渲染。

## 1. 快速开始

仓库的 `examples/plugins/` 下有两个可直接复制的例子：

- `shell-disk-load`：POSIX Shell，读取磁盘使用率与系统负载。
- `python-file-watch`：Python 标准库，读取本地文件大小与行数，演示 `state` 与密钥。

```bash
cd src
go run ./cmd/pimon-hub plugin validate ../examples/plugins/shell-disk-load
echo '{"path": "/"}' > /tmp/cfg.json
go run ./cmd/pimon-hub plugin run ../examples/plugins/shell-disk-load --config /tmp/cfg.json
```

## 2. 目录与安装

一个 exec 插件是一个目录：

```
<插件目录>/<id>/
  plugin.yaml    插件描述
  run            可执行入口（固定名为 run）
```

- `run` 必须是带执行权限的普通文件，可以是编译好的程序，也可以是带 shebang（如 `#!/bin/sh`、`#!/usr/bin/env python3`）的脚本。
- 目录名必须等于 `plugin.yaml` 里的 `id`，manifest 必须声明 `runtime: exec`。
- hub 的插件目录是数据目录下的 `plugins/`（默认 `/var/lib/pimon/plugins/`），每个插件一个子目录；以 `.` 开头的子目录被忽略。
- 与内置插件 id 冲突时，内置插件优先，该目录被忽略。
- 安全检查：hub 加载时要求插件目录、`plugin.yaml`、`run` 的属主是 hub 运行用户或 root，并且不能被组或其他用户写（权限里不能有 g+w、o+w），否则拒绝加载并记录原因。开发工具不做这项检查。

## 3. exec 协议

hub 每次采集启动一次 `run` 进程：

1. 工作目录是插件目录。
2. 环境变量只继承白名单 `PATH`、`HOME`、`LANG`、`TZ`，其余（包括令牌类变量）都不传。另外会追加代理变量：配置了代理时是 `ALL_PROXY`、`HTTPS_PROXY`、`HTTP_PROXY`（大小写两套），直连时是 `NO_PROXY=*`、`no_proxy=*`。
3. 输入通过 stdin 以一份 JSON 传入，写完即关闭。
4. 插件把一份 JSON 报告写到 stdout，以退出码 0 结束。
5. 退出码非 0、输出不是合法报告、stdout 超过 1 MiB、超过 `timeout`，都算采集失败；失败时 hub 保留上次成功的值并标记过期。stderr 只保留最后 16 KiB，失败时会附在错误信息里，错误信息中出现的密钥值与代理地址会被替换为 `***`。
6. 超时或输出超限时整个进程组（含孙进程）会被杀掉。

### stdin 输入

```json
{
  "api_version": 1,
  "config": {"path": "/", "warn_pct": 80},
  "secrets": {"token": "..."},
  "proxy": "socks5://127.0.0.1:1080",
  "state": "",
  "last": null
}
```

| 字段 | 说明 |
|---|---|
| `api_version` | 协议版本，当前为 1 |
| `config` | 实例配置的普通字段，已按 `config_schema` 校验并应用默认值；不可见字段（`visible_when` 不成立）不会出现；不含密钥 |
| `secrets` | 密钥字段，键是密钥的具体路径：顶层密钥为字段名（`token`），`kv` 的密钥值为 `字段名.键`（`headers.X-Token`），`object_list` 内为 `字段名[下标].子字段`（`accounts[1].token`）；值都是字符串。没有密钥时是空对象 |
| `proxy` | 选择了代理时为含认证信息的代理 URL，直连为 `null` |
| `state` | 插件私有状态，即上次报告里 `state` 的原样回传，首次为空字符串 |
| `last` | 上次成功的报告（结构同下文报告），首次为 `null` |

密钥只出现在 `secrets` 里，不要写日志、不要输出到 stdout 或 stderr。

### 代理

插件用哪种方式走代理由自己决定：可以读 stdin 的 `proxy`，也可以直接依赖环境变量（多数 HTTP 客户端和 `curl` 会自动识别 `HTTP_PROXY` 等）。代理协议支持 `http`、`https`、`socks5`、`socks5h`；`socks5h` 由代理端解析域名，`socks5` 在本机解析。

## 4. plugin.yaml

完整的字段约束以 JSON Schema 为准（见第 7 节）。下面是各字段的说明。

### 顶层字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `id` | 是 | 小写字母、数字和连字符，如 `my-plugin`；exec 插件须与目录名相同 |
| `version` | 是 | `x.y.z`，可带 `-预发布` 或 `+构建` 后缀 |
| `api_version` | 是 | 目前只支持 `1` |
| `name` | 是 | 多语言文本，见下 |
| `kind` | 是 | `source`（数据源）或 `notifier`（通知渠道） |
| `runtime` | 是 | 目录里的插件写 `exec` |
| `runs_on` | 是 | 非空列表，取值 `hub`、`agent` |
| `interval` | 否 | 默认刷新间隔，如 `30s`、`5m`；缺省 `5m` |
| `min_interval` | 否 | 实例刷新间隔的下限，不得大于 `interval`；不写则只受全局下限（5 秒）约束。实例设置的间隔低于它会被拒绝 |
| `timeout` | 否 | 单次采集超时；缺省 `30s` |
| `config_schema` | 否 | 配置表单定义，见下 |
| `outputs` | 否 | 产出的数据项声明 |
| `widgets` | 否 | 小组件声明 |
| `alerts` | 否 | 默认告警规则（目前只解析并存储） |

时长写法同 Go 的 `time.ParseDuration`（`500ms`、`30s`、`5m`、`1h`），必须为正。顶层出现未知字段会报错，错误带行号。

多语言文本（`name`、`title`、`help`）可以写成单个字符串（两种语言共用），或 `{zh: 中文, en: English}`；只写一种时另一种回退到它。

### config_schema 字段类型

`config_schema` 是字段列表，每个字段都有 `key`（字母、数字、下划线，不以数字开头，同层唯一）和 `type`。通用属性：`title`、`help`、`required`、`default`、`min`、`max`、`pattern`、`visible_when`。

共 14 种类型：

| type | 取值形状 | 说明 |
|---|---|---|
| `string` | 字符串 | 可用 `pattern`；`min`/`max` 为字符数 |
| `text` | 字符串 | 多行文本，属性同 string |
| `number` | 数字 | `min`/`max` 为数值范围 |
| `boolean` | 布尔 | |
| `enum` | 字符串 | 必须有非空 `options`，选项写成字符串，或 `{value, title}` |
| `secret` | 字符串 | 密钥，不回显，随 `secrets` 传入 |
| `secret_url` | 字符串 | 整条地址就是凭据：公网只允许 https，禁止内嵌 `user:pass@`，允许查询参数，内网地址允许 http |
| `url` | 字符串 | 仅 http/https、必须有主机、禁止内嵌凭据；可选 `allow_query`、`allow_public_http`（公网主机也允许 http）、`follow_redirects`，三项默认都为 false |
| `proxy` | 字符串 | 代理 id，空表示直连 |
| `duration` | 字符串 | Go 时长写法；`min`/`max` 单位为秒 |
| `list` | 字符串数组 | `pattern` 对每个元素生效；`min`/`max` 为元素个数 |
| `kv` | 字符串到字符串的映射 | 声明 `secret_values: true` 时每个值都是密钥（如请求头） |
| `object_list` | 对象数组 | 子字段用 `fields` 声明，不能嵌套 `object_list`，也不能含带 `secret_values` 的 `kv` |
| `lookup` | 字符串，或由插件定义形状的对象 | 带查询候选的字段（如城市搜索）；查询入口由 hub 提供，目前仅内置插件实现 |

`pattern` 只能用于 `string`、`text`、`secret`、`list`；`min`/`max` 不能用于 `boolean`、`enum`、`url` 等类型；`options` 只用于 `enum`；`fields` 只用于 `object_list`；`secret_values` 只用于 `kv`；`allow_query` 等三项只用于 `url`。

`visible_when` 写成 `{另一字段 key: 值或值列表}`：所有条件同时成立才显示，值列表表示"等于其中任意一个"。只能引用同一层、排在前面的 `enum`、`boolean`、`string`、`number` 字段。不可见的字段不校验，也不会出现在 `config` 里。

### outputs

声明插件会产出的数据项：

```yaml
outputs:
  - {key: cpu,       type: gauge, title: {zh: CPU 使用率, en: CPU usage}}
  - {key: "disk[*]", type: quota, title: {zh: 磁盘, en: Disks}}
```

- `key` 是数据项键名；以 `[*]` 结尾表示动态集合，报告里的具体成员写成 `disk[/vol1]`、`container[nginx]`（方括号内是成员名，不能为空）。固定键不能含 `[`。`outputs` 里的 key 不能重复。
- `type` 是数据项类型，见第 5 节。
- 报告里出现的键必须是具体键名，不能是通配。

### widgets

```yaml
widgets:
  - id: cpu
    name: {zh: CPU, en: CPU}
    sizes:
      1x1: {template: gauge, bind: {value: {item: cpu}}}
      2x1:
        template: list
        bind:
          items:
            - {item: net_rx}
            - {item: net_tx}
```

- `id` 在同一插件内唯一；`sizes` 必填，键是 `NxM`（列 × 行），列 1 到 6、行 1 到 4（最小屏幕 800×480 的网格为 6×4）。
- `template` 必须是允许集合之一：`value`、`gauge`、`state`、`status-grid`、`list`、`table`、`chart`、`clock`、`weather`、`text`、`quota`、`quota-multi`（后两个在后续阶段实现）。
- `bind` 把模板的槽位绑定到数据项：值是 `{item, field}`，或这种引用的列表。`item` 必须是 `outputs` 里声明过的键（动态集合可写 `disk[*]` 引用全部成员，或 `disk[/vol1]` 引用某个成员）；`field` 省略时取该类型的默认字段，给出时必须属于该类型的字段集。槽位名由模板决定，manifest 不校验槽位名。

### alerts

```yaml
alerts:
  - name: {zh: 磁盘过高, en: Disk high}
    item: disk
    field: value
    op: ">"
    value: 90
    severity: warning
    for: 5m
```

`name`、`item`、`op` 必填；`op` 取 `<`、`<=`、`>`、`>=`、`==`、`!=`；`severity` 取 `info`、`warning`、`critical`；`item` 与 `field` 的规则同 bind 引用。

## 5. 报告格式

stdout 输出一个 JSON 对象：

```json
{
  "status": "ok",
  "summary": "/ 已用 17%，负载 3.28",
  "items": [
    {"key": "disk", "type": "gauge", "value": 17, "unit": "%", "min": 0, "max": 100}
  ],
  "events": [],
  "state": "任意字符串"
}
```

| 字段 | 说明 |
|---|---|
| `status` | 必填，`ok`、`warning`、`critical`、`unknown`。表达对被测对象的判断；采集本身失败请用非 0 退出码，不要用它表达 |
| `summary` | 一句话摘要，可选 |
| `items` | 数据项列表 |
| `events` | 事件列表，每项必须有 `id`、`type`、`at`（Unix 毫秒，不为 0）；目前只校验这三个通用字段，其余字段原样保存 |
| `state` | 插件私有状态，下次运行时在 stdin 的 `state` 里传回 |
| `collected_at`、`duration_ms` | 可选 |

顶层未知字段会被忽略。`stale` 由 hub 维护，插件自报的值会被清除。

### 数据项

每个数据项都有 `key`（具体键名，不重复）和 `type`，其余字段按类型使用。数值字段缺失与 0 是不同的含义，没有值就不要写。未知 `type` 的数据项会被丢弃（记日志，不算错误）；某个数据项自己出错时，可以带 `error` 字符串保留该项。

| type | 字段 | 默认字段 |
|---|---|---|
| `gauge` | `value`、`unit`、`min`、`max` | `value` |
| `number` | `value`、`unit` | `value` |
| `quota` | `used`、`total`、`remaining`、`remaining_pct`、`resets_at`、`expires_at`、`unit`、`label` | `remaining_pct` |
| `money` | `amount`、`currency`、`used`、`total` | `amount` |
| `state` | `state`、`text`（`state` 取 `ok`、`warning`、`critical`、`unknown`） | `state` |
| `text` | `text` | `text` |
| `table` | `columns`（字符串数组）、`rows`（二维数组） | 无，引用时必须写 `field` |

`resets_at`、`expires_at` 为 Unix 毫秒。数值历史只记录各类型的数值默认字段：`gauge.value`、`number.value`、`quota.remaining_pct`、`money.amount`，`quota` 另记 `used`。

### 缺失不当作零

采集失败时 hub 保留上次成功的值并标记过期，而不是显示 0。插件在拿不到数据时应以非 0 退出码结束，或把数据项的 `state` 报为 `unknown`，不要编造数值。

## 6. 开发工具

```text
pimon-hub plugin validate <目录>
pimon-hub plugin run <目录> [--config <文件>] [--proxy <URL>]
```

### validate

- 解析 `plugin.yaml`，报告全部问题并带行号，如 `第 7 行: kind: kind "sourcee" 不合法，应为 source 或 notifier`。
- 检查 `run` 入口存在且可执行、目录名等于 `id`、`runtime` 为 `exec`。
- 通过时会提示：hub 加载时还会检查属主与权限。
- 有任何问题退出码为 1。

### run

在本机用真实时钟运行插件一次：

1. 读取 `--config` 指定的 JSON 或 YAML 文件（按扩展名 `.json`、`.yaml`、`.yml` 判断，顶层必须是对象）；省略时按空配置处理。
2. 按 `config_schema` 应用默认值并校验，不合格则列出出错的字段并退出。
3. 拆出密钥，与 hub 运行时一样通过 stdin 的 `secrets` 传入；`--proxy` 可指定代理（`http`、`https`、`socks5`、`socks5h`）。
4. 按 manifest 的 `timeout` 执行一次，校验报告，打印状态、摘要、耗时、各数据项的键、类型与主值，并对照 `outputs` 提示未声明或未产出的数据项。
5. 失败（配置不合格、插件退出码非 0、超时、报告不合法）时打印原因并以退出码 1 结束。

密钥与代理凭据不会被打印。`run` 每次都是全新的一次运行，`state` 与 `last` 为空。

配置文件示例（`cfg.yaml`）：

```yaml
path: /etc/hosts
max_bytes: 100000
token: my-secret
```

## 7. JSON Schema

两份 JSON Schema 由 Go 结构生成，放在 `docs/plugin-schema/`，可用于编辑器里的补全和校验：

- `docs/plugin-schema/plugin.schema.json`：`plugin.yaml`（先把 YAML 转成 JSON 再校验）。
- `docs/plugin-schema/report.schema.json`：插件报告。

重新生成：

```bash
cd src && go test ./internal/hub/plugindev -run TestSchemaFiles -update-schema
```

测试会检查产物是否与当前代码一致，Go 结构改了而产物没更新时测试失败并给出上面的命令。`plugin.yaml` 的 Schema 由一组仅用于生成的描述结构产生，测试保证它的字段集合与解析器实际接受的一致；语义层面的校验（例如引用了未声明的数据项、`min_interval` 大于 `interval`）只有 `plugin validate` 能做。

## 8. 编写建议

- 只在插件自己出错时用非 0 退出码；被监控对象异常（例如磁盘满了、文件读不了）用 `status: warning|critical` 正常输出。
- 输出 JSON 要完整写到 stdout；调试信息写 stderr，它只会在失败时出现在错误信息里。
- 不依赖 hub 的环境变量：只有 `PATH`、`HOME`、`LANG`、`TZ` 可用，需要别的变量就用 `config` 传。
- Shell 插件没有 JSON 解析器时，可以像示例那样用 `sed` 提取简单字段，复杂配置建议改用 `jq` 或 Python。
- 用 `plugin validate` 和 `plugin run` 反复试，最后再放进 hub 的插件目录。
