# 快速启动 -launch 与 -L

一条连接用 `-launch`，一个本地访问用 `-L`，都可重复。网页上客户端和点对点隧道的“编辑”页可以生成完整命令。命令行没有 `-launch` 时读环境变量 `NPC_LAUNCH`（每行一个）。日志、`-skip_verify` 等进程级设置仍是普通 flag。

```bash
npc -launch="npc://vkey@1.2.3.4:8025?type=tls"
npc -launch=@/etc/npc/npc.jsonc
npc -launch="npc://vkey@1.2.3.4:8025?type=tls#home" -L="tcp://:2000?access=sshkey&target=10.0.0.5:22"
npc install -launch="npc://vkey@1.2.3.4:8025?type=tls" -L="tcp://:2000?access=sshkey"
```

## -launch 的值

按前缀判定：

| 形式 | 说明 |
| --- | --- |
| `npc://…` | 启动链接 |
| `@路径` | 本地配置文件 |
| `http(s)://…` | 远程配置内容，只能定义连接 |
| 其他 | base64（标准或 URL 安全，有无填充都可以，去掉所有空白）；解码后是 gzip 就解压，上限 1 MiB |

都不是时报错“不是启动链接、@文件或 base64，文件请写 @路径”。`npc launch pack <文件>` 生成 gzip+base64，**输出里含明文密钥**。

## 启动链接

```
npc://<vkey>@host:port[/path]?type=tls&alpn=…&sni=…&host=…&alt=tcp://addr:port&name=…#名字
```

链接只描述连接，不含本地访问。

- `vkey` 是任意字符串，按百分号编码；链接里没有客户端编号。
- `host:port[/path]` 是第一个拨号地址；IPv6 写成 `[::1]:8025`。`/path` 只用于 `ws`、`wss`。
- `#名字` 和 `name` 是连接名（日志和 `-L` 的 `via` 用），`#名字` 优先。
- 参数名区分大小写，不能重复（`alt` 除外）。不认识的参数告警后忽略；`access`、`password`、`target`、`local_type`、`local_port`、`local_ip`、`conn_mode`、`local_proxy` 属于 `-L`，写在链接里报错。文本最长 8 KiB。

| 参数 | 说明 |
| --- | --- |
| `type` | `tcp`（默认）、`tls`、`ws`、`wss`、`quic` |
| `host`、`sni` | Host 头与默认 SNI、TLS 服务器名 |
| `alpn` | `tls`、`quic` 的 ALPN，逗号列表 |
| `http` | `ws`、`wss` 的 HTTP 版本，逗号列表 |
| `subprotocol` | `ws`、`wss` 的 WebSocket 子协议 |
| `tier`、`weight`、`standby`、`count`、`dirs`、`kinds` | 第一个地址的多承载参数，见 [NPC 命令行参数](/reference/npc-cli.md#服务器地址) |
| `alt` | 更多路径，可重复：`scheme://host:port[/path][?k=v;k=v]`，参数同上，用 `;` 分隔 |
| `pin` | 服务端密钥标识（32 位十六进制） |
| `pipes`、`scheduler_up`、`session_max_lanes`、`session_resume_timeout`、`session_usage_interval`、`relay_idle`、`p2p_visitor_keep_server` | 会话设置，含义同 [npc.conf](/reference/npc-config.md) |
| `proxy` | 连接服务端使用的代理 |
| `source_addr` | 出站连接的本地源地址（npc.conf 的 `local_ip`） |

一条链接是一个服务端实例，可以有多条路径。`pipes=multi` 让路径同时各建管道，缺省时按顺序尝试。

```bash
npc -launch="npc://VKEY@nps.example.com:8025?type=tls&alpn=h2&count=2&pipes=multi&alt=wss://nps.example.com:443/ws?tier=1;weight=2;http=h2&alt=tcp://nps.example.com:8024?standby=1&name=office"
```

## 本地访问 -L

```
-L="<类型>://[user:pass@][host]:port?access=<密钥>&target=<目标>&conn_mode=auto&local_proxy=false&via=<连接名>"
```

| 部分 | 说明 |
| --- | --- |
| 类型 | `tcp`、`udp`、`tcp_udp`、`mixed`（HTTP 和 SOCKS5 代理，UDP 走 SOCKS5）、`transparent`（透明代理，仅 Linux，npc 不改防火墙） |
| `host:port` | 本地监听，`host` 留空为全部地址，IPv6 写成 `[::1]:2000` |
| `access` | 隧道密钥，必填 |
| `target` | 要访问的目标；不写时用隧道的默认目标，隧道也没有时连接失败。`mixed`、`transparent` 的目标取自每条连接，写了报错 |
| `conn_mode` | `auto`（默认）、`relay`、`direct` |
| `via` | 走哪条连接（`#名字`）。只有一条连接时可省略，多条时必填，不存在就报错 |
| `auth` | 只用于 `mixed`，可重复，`user:pass`，按第一个 `:` 分隔，百分号解码；`user:pass@` 是一组简写，可以和 `auth` 同时存在；不写就无认证 |

`mixed` 没有账号时任何能连到监听地址的人都能使用，监听局域网地址请加账号。`-L=@文件` 读取每行一条（`#` 开头和空行跳过）。

`-launch` 和 `-L` 分别收集后再关联，先后顺序不影响结果。

## 角色

是否有 `-L` 关联决定角色：有 `-L` 的 `-launch` 只作访问者（不托管隧道，可按策略空闲断开）；没有 `-L` 的是普通客户端。配置文件同理：有 `access` 段是访问者。同一 vkey 和服务器既有带 `-L` 的又有不带的，共用一条会话，按普通客户端托管，同时提供访问，不空闲断开。

## 配置文件

JSON 或 JSONC（可写注释和尾逗号）。顶层 `profiles` 是以连接名为键的对象，键就是 `#名字`，也是 `-L` 的 `via` 的值：

```jsonc
{
  "profiles": {
    "home": {
      "vkey": "VKEY",
      "source_addr": "192.168.1.5",
      "paths": [
        {"url": "tls://a.example.com:8025", "alpn": "h2", "count": 2},
        {"url": "wss://a.example.com:443/ws", "tier": 1, "weight": 2, "standby": false}
      ],
      "session": {"max_pipes": 2, "scheduler_up": "spread_eta", "max_lanes": 0, "resume_timeout": 0, "usage_interval": 0},
      "access": [
        {"access": "sshkey", "target": "10.0.0.5:22", "local_type": "tcp", "local_port": 2000, "local_ip": "127.0.0.1", "conn_mode": "auto"},
        {"access": "lan", "local_type": "mixed", "local_port": 1080, "auth": [{"user": "a", "pass": "b"}]}
      ]
    },
    "office": {"vkey": "VKEY2", "paths": [{"url": "tcp://b.example.com:8024"}]}
  }
}
```

单实例可以省略 `profiles`，把字段放顶层，连接名写 `name`（可省）。`paths` 的 `url` 必须带 scheme，字段还有 `sni`、`host`、`http`、`subprotocol`、`tier`、`weight`、`standby`、`dirs`、`count`、`kinds`。`session.max_pipes` 为 1 单管道，大于 1 多管道。`source_addr` 是出站连接的本地源地址，同启动链接的 `source_addr`。`profiles` 里不能再写 `name`。

`-L` 可以挂到配置文件里的连接上：`-L="tcp://:3000?access=p&via=office"`，`via` 就是 `profiles` 的键；该连接自己的 `access` 照常保留，只有本地端口冲突才报错。

多个 `-launch` 的内容合并：同一 vkey 和服务器的条目共用会话，会话设置写一处即可，写了不一致报错并指明来源；本地端口冲突报错并指明来源。

## 运行与回退

- 每条连接独立运行，一条断开后 5 秒重新读取来源并重启它，不影响其他连接。
- 远程来源失败时继续用内存中最后一份有效内容，至少隔 30 秒再请求；首次启动没有内容时每 30 秒重试，属于它的 `-L` 等它可用后再绑定。
- 来源返回 `410 Gone` 表示已撤销，5 分钟后重试。
- 本地输入无效时直接退出。

## 安全

远程内容可以决定 npc 连接哪个服务器，只使用你信任的来源，尽量用 https（http 会告警，https 不允许跳转到 http）。限制 1 MiB，超时 15 秒，最多跟随 5 次跳转。远程内容只能定义连接，不接受 `access`、隧道、域名解析等条目，不能引用其他 URL 或文件，不能使用模板，也不能设置 `dns_server`、`pprof_addr` 等本机相关的键。

`npc install` 把内容写到安装目录下仅属主可读的文件，服务定义只有 `@文件`。`npc launch show -launch=… -L=…` 规范化打印，默认遮盖 vkey、access 和代理密码，`--reveal` 才显示；也接受旧参数并输出新写法。

## 旧写法

`-server`、`-vkey`、`-type`、`-password`、`-local_type`、`-local_port`、`-target`、`-conn_mode`、`-local_proxy`、`npc.conf` 和早期版本写入的 JSON、YAML、INI 文件（`@文件`）继续可用，集中转换成同一启动配置。`-password` 这组只支持单条，和 `-launch`、`-L` 同时给出报错。

此功能到 1.0 不会原样保留，见[版本规则](../../getting-started/versioning.md)。
