# 快速启动 -launch

`-launch` 用一段内容启动 npc，不需要配置文件。可以重复传入，每个输入独立运行；命令行没有 `-launch` 时读环境变量 `NPC_LAUNCH`。

`-launch` 和 `-server`、`-vkey` 是两种互斥的写法：同时给出会报错。`-type`、`-config`、`-proxy`、`-local_ip` 在 `-launch` 下被忽略并告警（启动链接用自己的参数）。日志、`-skip_verify` 等进程级设置仍只由命令行决定。`-launch` 与 `--launch` 写法相同。

```bash
npc -launch="npc://vkey@1.2.3.4:8025?type=tls"
npc -launch=https://example.com/npc.yaml -launch=@/etc/npc/office.json
npc install -launch="npc://vkey@1.2.3.4:8025?type=tls"
```

链接里的双引号在 Windows cmd、PowerShell 和 Linux shell 里都能用；网页上客户端详情的启动命令就是这种形式。

## 输入形式

| 形式 | 示例 |
| --- | --- |
| 启动链接 | `npc://vkey@1.2.3.4:8025?type=tls` |
| 链接的 Base64 | `npc://<base64>`：一个服务端实例，见下 |
| 远程内容 | `https://example.com/npc.yaml` |
| 本地文件 | `@/etc/npc/launch.json` |
| Base64 | 以下任一内容的 Base64（标准或 URL 安全，有无填充都可以；解码前去掉所有空白，可以折行） |
| JSON / YAML / INI | 见下 |

## 启动链接

```
npc://<vkey>@host:port[/path]?type=tls&alpn=…&sni=…&host=…&alt=tcp://addr:port&name=…#标签
```

- `vkey` 是任意字符串，按百分号编码（`:` `@` `/` `;` 等写成 `%3A` `%40` `%2F` `%3B`）；链接里没有客户端编号。
- `host:port[/path]` 是第一个拨号地址；IPv6 写成 `[::1]:8025`。`/path` 只用于 `ws`、`wss`。
- `#标签` 和 `name` 是档案名（日志里用），标签优先。
- 参数名区分大小写，不能重复（`alt` 除外）。**不认识的参数告警后忽略**，所以较新版本写的链接也能连上；链接文本最长 8 KiB。链接是秘密（含 vkey），日志里不写它。

连接参数，与 `-server` 的候选参数同名同义（见 [NPC 命令行参数](/reference/npc-cli.md#服务器地址)）：

| 参数 | 说明 |
| --- | --- |
| `type` | `tcp`（默认）、`tls`、`ws`、`wss`、`quic`；`kcp` 不进链接 |
| `host`、`sni` | Host 头与默认 SNI、TLS 服务器名 |
| `alpn` | `tls`、`quic` 的 ALPN，逗号列表 |
| `http` | `ws`、`wss` 的 HTTP 版本，逗号列表 |
| `subprotocol` | `ws`、`wss` 的 WebSocket 子协议，默认不发送 |
| `tier`、`weight`、`standby`、`count`、`dirs`、`kinds` | 第一个地址的多承载参数，含义见 [NPC 命令行参数](/reference/npc-cli.md#服务器地址) |
| `alt` | 更多候选路径，可重复。写法 `scheme://host:port[/path][?k=v;k=v]`，`scheme` 是 `tcp`、`tls`、`ws`、`wss`、`quic`；alt 自己的参数（`tier`、`weight`、`standby`、`count`、`dirs`、`alpn`、`sni`、`http`、`subprotocol` 等，与第一个地址同名同义）写在 `?` 后，用 `;` 分隔，不必再编码 `&`；百分号编码后的 `&` 也接受。`host:port@host2` 的 `@host2` 是 `host` 参数 |
| `pin` | 服务端密钥标识（32 位十六进制），等同 `-new_protocol_pin`；只接受该标识的服务端 |

连接设置，键名与 [npc.conf](/reference/npc-config.md) 的 `[common]` 相同：`pipes`、`scheduler_up`、`session_max_lanes`、`relay_idle`、`p2p_visitor_keep_server`、`session_resume_timeout`、`proxy`（或 `proxy_url`）、`local_ip`。

带下面任一参数的链接是点对点访问者（本地定义的监听，服务端定义的监听不用这些参数）：`access`（或别名 `password`，必需）、`target`（`target_addr`）、`local_type`、`local_port`、`conn_mode`、`local_proxy`。命令行的 `-access`（别名 `-password`）、`-local_type`、`-local_port`、`-conn_mode`、`-target`、`-local_proxy` 会加到每个 `npc://` 链接上；链接里已有同一参数时报错。

```bash
npc -launch="npc://vkey@1.2.3.4:8025?type=tls" -access="sshkey" -local_type=tcp -local_port=2000 -conn_mode=auto
```

**一条链接 = 一个服务端实例**（一个 vkey、一条会话），可以含多条候选路径：第一个地址的参数写在 `type`、`tier`、`weight`、`standby`、`count`、`dirs`、`kinds` 里，更多路径用 `alt`。`pipes=multi` 让这些路径同时各建管道，缺省（`single`）时按顺序尝试，`alt` 是后备，语义同 `-pipes`。多个服务端实例用多个 `-launch`，或一次 `NPC_LAUNCH`（每行一个）。

完整示例：一个实例，主路径 TLS 建 2 条管道，WSS 作第二路径，TCP 为备用候选：

```bash
npc -launch="npc://VKEY@nps.example.com:8025?type=tls&alpn=h2&count=2&pipes=multi&alt=wss://nps.example.com:443/ws?tier=1;weight=2;http=h2&alt=tcp://nps.example.com:8024?standby=1&name=office"
```

同一个实例的多个连接共用一组列表时，才需要改用 `-server`。

### npc://Base64

`npc://` 后面整段不含 `@`、`?`、`#`、`/`、`:` 时，按 Base64（URL 安全，无填充，也接受 `+`）解码，结果再按同样的规则解析（启动链接、JSON、YAML、INI）。它代表**一个**服务端实例（可以含多条候选路径，如上面的 `alt`）：解出多个实例时报错，多个实例请用多个 `-launch`、纯 Base64 或 `@文件`。内容中不能有空白或折行，否则报错。含 `@` 的是普通启动链接。

纯 Base64（不带 `npc://`）可以含折行，也可以表示多组；放在 `NPC_LAUNCH` 里时每行是一组，所以折行的 Base64 不能放在环境变量里，请用 `base64 -w0` 生成一行，或用 `@文件`。

### 环境变量 NPC_LAUNCH

命令行没有 `-launch` 时才读取；给了 `-launch` 就不生效，两者不合并。多组按换行分隔，空行忽略，每行可以是链接、URL 或 `@文件`。逗号不是分隔符：链接的查询里有 `alpn=h3,nps`、`http=h2,h3` 这样的逗号。

## 安装为服务

`npc install -launch=…` 把输入写进安装目录下仅属主可读的 `conf/npc-service-launch-N.conf`（`@文件` 保持引用该文件的绝对路径），服务定义只有 `-launch=@文件`，vkey 不出现在服务定义里。命令行的 `-access` 等访问者参数同样并入写入的链接。

## JSON 与 YAML

顶层是一个配置、配置列表，或 `{"profiles": [...]}`：

```yaml
profiles:
  - name: direct            # 只有连接字段：用已有客户端的 vkey 直接连接
    server: a:8024,b:8024
    vkey: k1,k2
    type: tls
  - name: office            # 含 common 或下列列表：与配置文件模式相同
    common: {server_addr: a:8024, vkey: k3, conn_type: tls}
    hosts:
      - {remark: web, host: a.example.com, target_addr: [127.0.0.1:80], headers: {X-From: npc}}
    tasks:
      - {remark: ssh, mode: tcp, server_port: 2222, target_addr: 127.0.0.1:22}
    locals: []
```

- 连接字段：`server`、`vkey`、`type`、`proxy`、`local_ip`、`pipes`。
- `common`、`hosts`、`tasks`、`locals` 各项的键名与 [npc.conf](/reference/npc-config.md) 相同。
- `headers`、`response_headers` 对应 `header_*`、`response_*`。
- `remark` 不能以 `secret`、`p2p` 开头。

INI 内容就是配置文件本身，并按配置文件模式运行。

## 运行与回退

- 同一输入的多个配置一起运行，任一配置断开时整组 5 秒后重启，并重新读取来源。
- 远程来源获取或解析失败时继续使用内存中最后一份有效内容，并至少隔 30 秒再请求；首次启动没有可用内容时每 30 秒重试。
- 来源返回 `410 Gone` 表示已撤销：停止运行，5 分钟后重试。
- 最后一份有效内容不写入磁盘，npc 重启后需要来源可用。
- 非远程内容无效时直接退出。

## 安全

来源可以决定 npc 连接哪个服务器、开放哪些隧道，能让对方访问本机可达的服务。**只使用你信任的来源**，并尽量使用 https（http 会告警，https 不允许跳转到 http）。

远程内容限制 1 MiB，请求超时 15 秒，最多跟随 5 次跳转。它不执行命令，也不能：

- 使用模板，引用其他 URL 或本地文件；
- 设置 `dns_server`、`multi_account`、`cert_file`、`key_file`、`local_path`、`strip_pre`、`pprof_addr` 或 `mode=file`。

`npc://<base64>` 解出的内容沿用外层的来源信任：本地输入按本地处理，远程内容里的按远程处理。需要这些功能时使用本地配置文件。

此功能到 1.0 不会原样保留，见[版本规则](../../getting-started/versioning.md)。

点对点访问的本地服务器只接受新写法：`local_type`（`tcp_udp`、`tcp`、`udp`、`mixed`、`transparent`）和 `conn_mode`（`auto`、`relay`、`direct`）；`target_type`、`fallback_secret` 和旧的 `local_type` 取值会报错。
