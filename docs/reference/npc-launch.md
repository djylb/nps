# 启动链接与配置格式

本页是 `-launch` 和 `-L` 接受的全部格式。使用示例见[启动链接与多条连接](/guide/client/launch.md)。

## -launch 的值

`-launch` 可以重复，每个值是下列形式之一，npc 按前缀判断：

| 形式 | 说明 |
| --- | --- |
| `npc://…` | [启动链接](#启动链接) |
| `@路径` | 本地文件，内容格式见[文件内容](#文件内容) |
| `http://…`、`https://…` | 从网址读取配置，见[从网址读取](#从网址读取) |
| 其他 | base64（标准或 URL 安全字符集，有无 `=` 填充都可以，空白被忽略）。解码后是 gzip 就解压。解码结果按[文件内容](#文件内容)读取，上限 1 MiB |

都不符合时，npc 报错退出。`npc launch pack <文件>` 可以生成 base64 形式的值，结果包含明文密钥。

## NPC_LAUNCH

命令行没有 `-launch` 时，npc 读取环境变量 `NPC_LAUNCH`，它不和命令行合并。

- 每行一个值，只按换行分隔（链接自身可能含逗号），空行忽略。
- 每行可以是上表的任意形式。折行的 base64 不能放进去，请用 `npc launch pack` 生成一行，或者用 `@文件`。
- `npc install` 也读取它，内容写入服务的配置文件。

## 启动链接

```text
npc://<vkey>@<地址>:<端口>[/路径]?type=<协议>&<参数>#<连接名>
```

- `vkey` 是客户端密钥，在网页“客户端”列表的“唯一验证密钥”处查看。含特殊字符时按百分号编码，`:` 写成 `%3A`。
- 地址和端口是第一条路径的拨号地址，IPv6 写成 `[::1]:8024`。
- `/路径` 只用于 `ws`、`wss`、`http`、`https`、`auto`。
- `#连接名` 和 `name=` 都是连接名，用于日志和 `-L` 的 `via`；同时写时 `#` 优先。
- 参数名区分大小写，除 `alt` 外不能重复，否则报错。
- 链接总长不超过 8 KiB。
- 不认识的参数报警告后忽略，属于 `-L` 的 `access`、`password`、`target`、`target_addr`、`local_type`、`local_port`、`local_ip`、`conn_mode`、`local_proxy` 写在链接里报错。

### 连接参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `type` | `auto` | 第一条路径的协议：`auto`、`tcp`、`tls`、`ws`、`wss`、`http`、`https`、`quic`。`auto` 必须写端口，见下文 |
| `alt` | 无 | 更多路径，可重复，写法见下文 |
| `name` | 无 | 连接名 |
| `pipes` | `single` | `single` 按顺序尝试各路径，`multi` 同时使用多条路径 |
| `scheduler_up` | `affinity` | 上行分配方式，取值见 [npc 命令行参数](/reference/npc-cli.md) |
| `session_max_lanes` | `0` | 同 `-session_max_lanes` |
| `session_resume_timeout` | `0` | 同 `-session_resume_timeout` |
| `session_usage_interval` | `0` | 同 `-session_usage_interval` |
| `relay_idle` | `0` | 同 `-relay_idle` |
| `p2p_visitor_keep_server` | `false` | 同 `-p2p_visitor_keep_server` |
| `proxy` | 无 | 连接服务端使用的代理，格式同 `-proxy`。密码含 `&`、`%` 时按百分号编码 |
| `source_addr` | 无 | 出站连接使用的本机源地址 |
| `pin` | 无 | 服务端密钥标识，32 位十六进制 |

链接里没写的设置，使用同名命令行参数的值。

### 路径参数

下面的参数可以直接写在链接上（作用于第一条路径），也可以写在 `alt` 或配置文件的 `paths` 里。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `tier` | `0` | 整数，越小越先尝试 |
| `standby` | `false` | 备用路径，其他路径都不可用时才用。只写 `standby` 也算 `true` |
| `weight` | `0` | 非负整数。`pipes=multi` 时这条路径分得的份额 |
| `count` | `1` | 不小于 1 的整数。`pipes=multi` 时这条路径的管道数 |
| `dirs` | `bidi` | `bidi` 双向、`up` 仅客户端到服务端、`down` 仅服务端到客户端，可用逗号组合。至少一条路径要含 `bidi`，`http`、`https` 只能是 `bidi` |
| `host` | 地址本身 | HTTP Host 头，也是默认的 TLS 服务器名 |
| `sni` | `host` 的主机名 | TLS 服务器名，地址是 IP 时默认不发送 |
| `alpn` | `quic` 为 `h3` | 只用于 `tls`、`quic`。TLS ALPN，逗号分隔，顺序即偏好 |
| `http` | 见说明 | 只用于 `ws`、`wss`、`http`、`https`。HTTP 版本：`http/1.1`、`h2`、`h3`，逗号分隔，顺序即偏好。`https`、`wss` 默认 `h2,http/1.1`，`http`、`ws` 默认 `http/1.1`。`h2`、`h3` 只用于 `https`；`ws`、`wss` 不接受 `h3`，实际走 `http/1.1` |
| `kinds` | 全部 | 只用于 `http`、`https`（`ws`、`wss` 固定 WebSocket）。HTTP 承载方式：`websocket`、`streaming`、`split`、`exchange`、`http3`，逗号分隔，顺序即偏好。`http3` 需要 `http` 含 `h3`。不认识的取值报警告后忽略 |
| `subprotocol` | 无 | 只用于 `ws`、`wss`、`http`、`https`。WebSocket 子协议。连接服务端默认的 WebSocket 路径时需要 `subprotocol=binary`，网页生成的命令已经带上；服务端 `new_protocol_ws_paths` 中的路径不需要 |
| `admit_key` | 无 | 只用于 `http`、`https`。64 位十六进制的准入密钥，`exchange` 需要它，没有时跳过 `exchange` 并报警告 |

### alt 的写法

```text
alt=<协议>://<地址>:<端口>[@主机[:端口]][/路径][?k=v;k=v]
```

- 协议的取值同 `type`。
- 路径参数之间用 `;` 分隔，因为 `&` 已用于分隔链接的参数。
- `@主机` 是 `host=` 的简写，两者不能同时写。
- 示例：`alt=wss://203.0.113.10:8024/ws?tier=1;subprotocol=binary`。

### 多条路径的尝试顺序

`pipes=single` 时，每次连接和断线重连按以下顺序尝试，一条失败就换下一条：上次成功的路径最先，其余按 `tier` 从小到大，相同时按书写顺序，`standby` 排在最后。

`pipes=multi` 时，非 `standby` 路径中 `tier` 不超过最小值加 1 的路径同时建立管道。`standby` 路径也建立管道，但只在其他管道全部失效时才承载流量。

### type=auto

不写 `type` 时就是 `auto`，它在一个混合端口上依次尝试 `tls`、`quic`、`wss`、`https`，全部是加密协议，不会尝试明文。没有写路径时跳过 `wss` 和 `https`。

`auto` 必须写端口。`alpn` 只给 `tls`、`quic`，`http`、`kinds`、`admit_key` 只给 `https`，`subprotocol` 只给 `wss`、`https`，其他路径参数给所有尝试。

## 本地访问 -L

`-L`（完整写法 `--local`）在本机开一个监听端口，把流量经点对点隧道送到另一端。可以重复。

```text
-L="<类型>://[用户:密码@][监听地址]:<端口>?access=<访问密钥>&target=<目标>&conn_mode=auto&local_proxy=false&via=<连接名>"
```

| 部分 | 默认值 | 说明 |
| --- | --- | --- |
| 类型 | 必填 | `tcp`、`udp`、`tcp_udp`（同时监听 TCP 和 UDP）、`mixed`（HTTP 和 SOCKS5 代理）、`socks5`、`http`（HTTP 代理）、`transparent`（透明代理，支持 Linux、macOS、FreeBSD，防火墙规则需要自己配置） |
| 监听地址 | 全部地址 | 留空表示全部地址，如 `:2000`；IPv6 写成 `[::1]:2000` |
| `用户:密码@` | 无 | 一组代理账号的简写，按百分号解码。只用于 `mixed`、`socks5`、`http`，可以和 `auth` 同时写 |
| `access` | 必填 | 点对点隧道的访问密钥 |
| `target` | 隧道的默认目标 | 要访问的目标。`mixed`、`socks5`、`http`、`transparent` 的目标由每条连接决定，写了报错 |
| `conn_mode` | `auto` | `auto` 自动（打洞成功就直连，否则经服务端中转）、`relay` 仅经服务端中转、`direct` 仅直连 |
| `local_proxy` | `false` | `true` 时由服务端连接目标。隧道开启“代理到服务器本地”才生效，否则仍由提供端连接 |
| `via` | 无 | 走哪条连接，取连接名。只有一条连接时可以省略，多条时必填 |
| `auth` | 无 | 代理账号 `用户:密码`，按第一个 `:` 分隔，可重复。只用于 `mixed`、`socks5`、`http` |

- 参数名只接受上表这些，写错报错。除 `auth` 外不能重复。
- `mixed`、`socks5`、`http` 没有账号时无需认证，能连到监听地址的人都能使用。监听局域网地址时请加账号。
- `-L=@文件` 从文件读取，每行一条，空行和 `#` 开头的行跳过。
- `-L` 和 `-launch` 的先后顺序不影响结果。

## 文件内容

`-launch=@路径` 指向的文件、解码后的 base64，都按下面的规则识别：

| 内容 | 说明 |
| --- | --- |
| 以 `{` 开头的 JSON，或以 `//`、`/*` 开头的 JSONC | [配置文件](#配置文件)。JSONC 可以写注释和尾逗号 |
| 一行启动链接、网址、`@路径` 或 base64 | 按 `-launch` 的值处理，最多嵌套 4 层 |
| 第一个有效行是 `[段名]` | **已弃用的载荷形式。** INI，语法同 [npc.conf](/reference/npc-config.md) |
| 其他 JSON（含 `[` 开头的数组）或 YAML | **已弃用的载荷形式。** 见下文 |

文件上限 1 MiB。

### 配置文件

顶层的 `profiles` 是对象，键是连接名，也就是 `-L` 的 `via` 取值：

```jsonc
{
  "profiles": {
    "home": {
      "vkey": "YOUR_VKEY",
      "source_addr": "192.168.1.10",
      "paths": [
        {"url": "tls://203.0.113.10:8024", "alpn": "h3"},
        {"url": "wss://www.example.com:8024/ws", "tier": 1, "subprotocol": "binary"}
      ],
      "session": {"max_pipes": 2, "scheduler_up": "spread_eta"},
      "access": [
        {"access": "YOUR_ACCESS_KEY", "target": "192.168.1.10:22", "local_type": "tcp", "local_port": 2000, "local_ip": "127.0.0.1"},
        {"access": "YOUR_ACCESS_KEY", "local_type": "mixed", "local_port": 1080, "auth": [{"user": "demo", "pass": "demo-password"}]}
      ]
    },
    "office": {"vkey": "YOUR_VKEY", "paths": [{"url": "quic://www.example.com:8024"}]}
  }
}
```

只有一条连接时可以省略 `profiles`，把字段放在顶层，连接名写 `name`（可省略）。有 `profiles` 时，顶层不能有其他键，`profiles` 里也不能写 `name`。不认识的键一律报错。

连接的字段：

| 字段 | 说明 |
| --- | --- |
| `vkey` | 必填。客户端密钥 |
| `paths` | 必填，至少一条。路径列表 |
| `source_addr` | 出站连接的本机源地址 |
| `session` | 连接设置，见下表 |
| `access` | 本地访问列表 |

`paths` 的字段是 `url`（必填，必须带协议，如 `tls://host:port`）、`alpn`、`sni`、`host`、`http`、`subprotocol`、`tier`、`weight`、`standby`、`dirs`、`count`、`kinds`，含义同[路径参数](#路径参数)。`admit_key` 没有单独的字段，写在 `url` 的查询参数里。

`session` 的字段：

| 字段 | 对应 |
| --- | --- |
| `max_pipes` | `1` 为单路径（`pipes=single`），大于 1 为多路径（`pipes=multi`），`0` 表示不设置，负数报错 |
| `scheduler_up` | `scheduler_up` |
| `max_lanes` | `session_max_lanes` |
| `resume_timeout` | `session_resume_timeout` |
| `usage_interval` | `session_usage_interval` |

`access` 的字段：`access`（必填）、`target`、`local_type`（默认 `tcp_udp`）、`local_port`、`local_ip`、`conn_mode`（默认 `auto`）、`local_proxy`、`auth`（`[{"user": "…", "pass": "…"}]`），含义同[本地访问 -L](#本地访问--l)。同一用户名写两次且密码不同时报错。

`relay_idle`、`p2p_visitor_keep_server`、`pin`、`proxy` 不能写在配置文件里，需要时用启动链接或命令行参数。

### 已弃用的载荷形式

早期版本写出的文件仍能读取，建议换成[配置文件](#配置文件)。

- **INI**：语法同 [npc.conf](/reference/npc-config.md)，可以写隧道和域名段。点对点访问的旧写法（`mode=p2p`、`[p2p…]`、`target_type`、`fallback_secret`、旧的 `local_type`）在这里直接报错，不做换算。
- **JSON、YAML**：一个对象、对象数组，或 `{"profiles": [...]}` 数组。对象的键是 `name`、`server`、`vkey`、`type`、`proxy`、`local_ip`、`pipes`、`common`（INI 的 `[common]` 键）、`hosts`、`tasks`、`locals`。只写前几个时，`server` 等列表按 `-server` 的规则展开成多条连接。`hosts`、`tasks` 的元素是 npc.conf 的键，`remark` 作为名称。`locals` 只接受 `local_type`、`conn_mode`、`local_port`、`local_ip`、`password`、`target_addr`、`local_proxy`。

用 `npc launch show -launch=@文件 --reveal` 可以看到它们对应的现行写法，隧道和域名段除外。

## 从网址读取

`-launch=https://…` 时，npc 从网址读取内容，格式同[文件内容](#文件内容)。

- 内容能决定 npc 连接哪个服务端，只使用可信的来源。`http://` 会报警告，`https://` 不允许跳转到 `http://`。
- 超时 15 秒，上限 1 MiB，最多跟随 5 次跳转。
- 只能定义连接。出现 `access`、隧道、域名段，引用其他网址或文件，使用 `{{` 模板，或设置 `multi_account`、`cert_file`、`key_file`、`local_path`、`strip_pre`、`pprof_addr`、`dns_server`，都按读取失败处理。

读取失败时的行为：

| 情况 | 行为 |
| --- | --- |
| 已经读到过内容 | 继续使用内存中最后一份有效内容，至少隔 30 秒再请求 |
| 还没有读到过内容 | 其他连接照常运行，每 30 秒重试。写了 `via` 或等待这条连接的 `-L`，在内容到达后才绑定 |
| 返回 `410 Gone` | 视为撤销，停止使用已有内容，5 分钟后再请求 |

## 多条连接的合并

- 每条连接独立运行，一条断开后等 5 秒重新读取来源并重启它，不影响其他连接。`-auto_reconnect=false` 或配置里 `auto_reconnection=false` 时，npc 退出。
- 第一次加载时，本地输入有错 npc 直接退出；之后重新加载出错只记日志，30 秒后重试。
- vkey 相同、路径集合相同的条目是同一条连接，共用一个会话。
  - 路径的写法必须一致，否则报错。
  - 连接设置写一处即可，多处写了且不一致时报错。
  - 同一条连接写多次时，`-L` 必须写 `via`。
- 连接名不能被不同的连接使用。没有名字的连接会自动命名为 `connection-序号`。
- 所有本地访问的监听不能冲突：端口相同、地址重叠、协议有交集时报错。
- 同一条连接不能既有来自其他来源的隧道、域名段，又有本地访问，请分开写成两条连接。

## 连接的角色

- 只有本地访问的连接只作访问端，不提供隧道，空闲时可以断开。
- 没有本地访问的连接是普通客户端。
- 同一条连接既有带 `-L` 的写法，又有不带的写法时，它既是普通客户端，也提供本地访问，空闲时不断开。
