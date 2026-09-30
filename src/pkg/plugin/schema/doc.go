// Package schema 实现插件 config_schema：字段定义的解析与自检、实例配置校验、
// visible_when 求值、密钥字段识别与拆分。
//
// # 字段类型
//
// 共 14 种：string、text、number、boolean、enum、secret、secret_url、url、proxy、
// duration、list、kv、object_list、lookup。通用属性：required、default、min、max、
// pattern、visible_when、help。min/max 的含义随类型而定：字符串为字符数，number 为数值，
// duration 为秒，list/kv/object_list 为元素个数。
//
// # 各类型的取值形状（实例配置按 JSON 解码后的形状）
//
//   - string/text/secret/secret_url/enum/proxy：字符串（proxy 为代理 id，空表示直连）
//   - number：数字；boolean：布尔；duration：time.ParseDuration 可解析的字符串
//   - list：字符串数组，pattern 对每个元素生效
//   - kv：字符串到字符串的映射；声明 secret_values: true 时每个值都是密钥
//   - object_list：对象数组，子字段由 fields 声明，校验错误路径形如 targets[2].url
//   - lookup：字符串，或由插件定义形状的对象（如城市搜索返回的 {id, name, lat, lon}）
//
// lookup 字段只在此声明；查询入口 POST /api/plugins/{id}/lookup/{key} 由注册表与 API 层提供。
//
// # url 校验
//
// 仅接受 http/https，必须有主机，禁止内嵌凭据（user:pass@）。选项 allow_query 允许查询参数；
// allow_public_http 允许公网主机使用 http（内网、回环、链路本地、CGNAT、localhost、
// 单标签主机名以及 .local/.lan/.internal/.home.arpa 后缀视为非公网）；follow_redirects 不参与
// 值校验，由执行端读取 Field.FollowRedirects 决定是否跟随重定向。三项默认均为 false。
//
// # visible_when
//
// 语法为 visible_when: {<另一字段 key>: <值或值列表>}：所有条件同时成立才显示，
// 值列表表示「等于其中任意一个」。只能引用同一层、排在前面的字段；被引用字段取
// 「应用默认值之后」的有效值，被引用字段本身不可见时视为无值（条件不成立）。
// 不可见字段跳过校验，也不会出现在 Prepare 返回的配置里。
//
// # 密钥
//
// secret、secret_url 以及声明 secret_values 的 kv 的各个值是密钥。SecretPaths/Split/Merge
// 用具体路径（api_key、headers.X-Token、accounts[1].token）标识密钥；Split 把配置拆成
// 普通部分与密钥部分（kv 密钥在普通部分里以 nil 占位，保留键名）；Redact 把密钥回显为
// {"set": true}；KeepSecrets 实现「密钥字段留空、缺省或回显值表示保留原值」。
package schema
