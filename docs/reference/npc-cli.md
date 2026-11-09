# NPC 命令行参数

```bash
./npc -launch="npc://VKEY@host:8025?type=tls" -log=off -debug=false
./npc -server="xxx:123,yyy:456" -vkey="xxx,yyy" -type="tls,tcp" -log=off -debug=false
```

一条连接用 `-launch`，一个本地访问用 `-L`，都可重复，写法见[快速启动](/guide/client/launch.md)，网页的“启动命令生成器”可以生成。`-server`、`-vkey`、`-type`、npc.conf 继续可用，转换成同一启动配置；`-launch` 与 `-server`、`-vkey` 互斥。

## 命令

`install`、`uninstall`、`start`、`stop`、`restart`、`status`、`register`、`update`、`update-force`、`version`（同 `-version`）。

## 参数

| 参数 | 说明 | 默认 |
| --- | --- | --- |
| `-server`, `-s` | 服务器连接：多个连接（档案）之间用逗号分隔（旧写法），使用 URL 写法时改用分号；每个连接内用 `\|` 分隔多个承载候选，见下文“服务器地址” | 无 |
| `-pipes` | 每个连接的管道模式：`single`、`multi`（`multi` 需要 zmux，当前告警一次后按 `single` 运行） | `single` |
| `-vkey`, `-k` | 认证密钥，与 `-server` 的连接一一对应，分隔符与 `-server` 相同 | 无 |
| `-type`, `-t` | 旧写法（不带 `://`）候选的承载：`tcp`、`tls`、`kcp`、`quic`、`ws`、`wss`，分隔符与 `-server` 相同。`kcp` 告警后按 QUIC 连接，服务端的 KCP 端口同时接受 QUIC。URL 写法的候选由方案名决定承载，不需要 `-type`；全部是 URL 时给出 `-type` 会告警它不起作用 | `tcp` |
| `-config`, `-c` | 配置文件路径，逗号分隔 | 无 |
| `-launch` | 一条连接，可重复：`npc://` 启动链接、`@文件`、`http(s)://` 配置内容，或 base64（`npc launch pack` 生成）。解析失败报错。环境变量 `NPC_LAUNCH` 见下 | 无 |
| `-L`、`--local` | 一个本地点对点访问，可重复：`tcp://:2000?access=KEY&target=10.0.0.5:53&conn_mode=auto&via=NAME`；`@文件` 为每行一条 | 无 |
| `-proxy` | 经代理连接：http、https、socks5、socks5h、socks4、socks4a | 无 |
| `-local_ip` | 出站绑定的本地 IP，与 `-server` 对应，分隔符与 `-server` 相同 | 无 |
| `-local_ip_forward` | `local_ip` 同时作用于隧道转发出口（仅公网 IP 与域名目标） | `false` |
| `-local_type` | 旧写法（只支持单条，改用 `-L`）：点对点访问的本地监听 | `tcp_udp` |
| `-conn_mode` | 点对点访问的连接方式：`auto`、`relay`、`direct` | `auto` |
| `-local_port` | 旧写法：访问端本地端口 | `2000` |
| `-password` | 旧写法：点对点访问密钥；与 `-launch`、`-L` 同时给出报错 | 无 |
| `-target` | 旧写法：点对点访问的目标 | 无 |
| `-local_proxy` | 旧写法：点对点访问连到服务端本地 | `false` |
| `-p2p_timeout` | 打洞超时（秒） | `5` |
| `-disable_remote_update` | 忽略服务端下发的远程更新 | `false` |
| `-session_resume_timeout` | 核心协议：会话失去所有连接后等待恢复的秒数，`0` 为 30 秒，负数不恢复；服务端配置了 `session_resume_timeout` 时以服务端为准（下次会话生效） | `0` |
| `-session_usage_interval` | 核心协议：提供点对点隧道时，上报直连流量的间隔秒数，没有变化不报；`0` 为 60，负数不上报 | `0` |
| `-session_max_lanes` | 核心协议：每条 QUIC 管道接受的车道数，`0` 为 16，`1` 关闭车道 | `0` |
| `-relay_idle` | 核心协议：直连建立后中继保留的秒数，之后关闭；`0` 为 60 秒 | `0` |
| `-p2p_visitor_keep_server` | 核心协议：点对点访问者空闲时也保持与服务端的连接 | `false` |
| `-auto_reconnect` | 断线自动重连 | `true` |
| `-disconnect_timeout` | 连接超时（秒） | `30` |
| `-keepalive` | 保活周期（秒），`0` 用默认 | `0` |
| `-skip_verify` | 跳过服务器证书校验 | `false` |
| `-debug` | 没有给出 `-log` 时输出到控制台；安装的服务会带 `-debug=false`，按 `-log` 输出。不再改变日志级别，级别用 `-log_level` | `true` |
| `-log` | `stdout`、`file`、`both`、`off`；显式给出时总是生效 | `file`（OpenWrt 上为 `off`） |
| `-log_path` | 日志路径，空为默认，`off` 禁用 | 系统默认 |
| `-log_level` | `trace`、`debug`、`info`、`warn`、`error`、`off`；可按模块单独设置，如 `info,p2p=debug,acme=warn` | `info` |
| `-log_instance` | 每行末尾加 `instance=`，多个 npc 的日志汇总时区分来源 | 空 |
| `-log_secrets` | 开发调试用：日志里原样写出 vkey、口令等，生产环境不要开启 | `false` |
| `-log_format` | `auto`（终端上彩色，其他情况 logfmt）、`text`（logfmt）或 `json` | `auto` |
| `-log_compress` | 压缩日志 | `false` |
| `-log_max_days` | 保留天数，`0` 关闭 | `7` |
| `-log_max_files` | 文件数，`0` 关闭 | `10` |
| `-log_max_size` | 单文件 MB，`0` 关闭 | `5` |
| `-pprof` | PProf 地址 `ip:port` | 无 |
| `-dns_server` | 优先 DNS，不可达时回退到系统 DNS | `8.8.8.8` |
| `-ntp_server` | NTP 服务器 | 无 |
| `-ntp_interval` | NTP 最小查询间隔（分钟） | `5` |
| `-timezone` | 时区，如 `Asia/Shanghai` | 无 |
| `-time` | `register` 的有效小时数 | `2` |
| `-gen2fa` | 生成 TOTP 密钥 | `false` |
| `-get2fa` | 根据密钥输出当前验证码 | 无 |
| `-version`, `-v` | 显示版本 | |

## 服务器地址

`-server` 的值分两层：

- **连接（档案）**：每个是一条独立的连接，各自对应 `-vkey`、`-type`、`-local_ip` 列表中同一位置的值（较短的列表重复最后一个值）。分隔符：
  - `-server` 里没有 `://` 时是旧写法，用逗号分隔：`-server="a.com:8024,b.com:8024" -vkey="k1,k2"`；
  - 只要用了 URL 写法，就用分号分隔，逗号只用于参数里的列表：`-server="tls://a.com:8025|https://a.com/ws?http=h2,http/1.1;tcp://b.com:8024" -vkey="k1;k2"`。`-vkey`、`-type`、`-local_ip` 随之用分号。URL 写法里用逗号分隔连接会报错并提示改用分号。
  - 分号在 shell 里是命令分隔符，必须加引号。
- **承载候选**：一个连接内用 `|` 分隔，是到同一服务端的几种走法。单管道（`-pipes=single`）每次连接和重连按顺序尝试，一个失败就换下一个：上次成功的候选最先，其余按 `tier` 升序、同 tier 按书写顺序，`standby` 候选排在最后（只在其他候选都失败后才用）。每个失败的候选记一条 `connect_failed`，成功时 `connected` 记下实际用的承载。

候选有两种写法：

- **URL**：方案即承载：`tcp://`、`tls://`、`quic://`、`http://`、`https://`（后续版本的承载条目同样只有这五种），另外本版本还接受 `kcp://`（按 QUIC 连接）、`ws://`（= `http://` 只用 websocket）、`wss://`（= `https://` 只用 websocket）。URL 里写**拨号地址**；未写端口时用默认端口（tcp/kcp 8024，tls/quic 8025，http/ws 80，https/wss 443），IPv6 写成 `tcp://[::1]:8024`。路径是 http/https/ws/wss 的端点路径（默认 `/ws`），多级路径直接写，如 `/api/v2/ws`；其他方案不能带路径。路径或参数里的 `|` 写成 `%7C`，`?` 写成 `%3F`。查询参数（与后续版本的承载条目同名）：
  - `tier`：整数，越小越先尝试，默认 0；
  - `weight`：非负整数，多管道的权重，本版本只解析保存；
  - `standby`：布尔，备用候选，默认 false（只写 `standby` 也算 true）；
  - `count`：多管道下这个候选的管道数，默认 1；
  - `dirs`：`bidi`（默认）、`up`、`down`，可用逗号组合，如 `dirs=up,down`。会话只能在含 `bidi` 的候选上建立，所以每个连接至少要有一个；单管道下跳过不含 `bidi` 的候选（告警一次：方向需要 zmux）；
  - `kinds`：http/https 的 HTTP 种类，顺序即偏好，可选 `websocket`、`streaming`、`split`，默认全部；同一 URL 上按 `http` 的版本顺序、版本内按 `kinds` 的顺序依次尝试，不可用的自动换下一个（多管道下由内核做缓存并逐步升级到排在前面的种类）。`streaming`、`split` 经 CDN 或反向代理也可用，记录层用 Sealed。`exchange` 需要两端共享的准入密钥，本版本告警并忽略；`ws`/`wss` 固定为 `websocket`；
  - `host`：Host 头和默认 SNI，缺端口时沿用地址的端口；
  - `sni`：覆盖 TLS 服务器名（默认取 `host` 或地址的主机名，IP 地址时不发）；
  - `alpn`：外层 TLS 的 ALPN，逗号分隔，顺序即偏好，只用于 `tls`、`quic`（quic 默认 `nps`）；http/https/ws/wss 不接受，它们的 ALPN 由 `http` 决定。
  - `http`：HTTP 版本，`http/1.1`、`h2`、`h3` 的逗号列表，顺序即偏好，只用于 http/https/ws/wss；ws/wss 只能用 `http/1.1`、`h2`。默认 https/wss 为 `h2,http/1.1`，http/ws 为 `http/1.1`。http/https 的 `streaming`、`split` 用列表里的 `http/1.1`、`h2`（`h2` 经 TLS 协商，只用于 https）；`h3` 本版本不拨号，告警后跳过；ws/wss 走 `http/1.1`。
  - 以上参数名与后续版本的承载条目和加入链接相同，迁移时原样保留。

  参数名区分大小写；其他参数、重复的参数、未知方案、带用户名或 `#` 的 URL 都会报错。
- **不带方案**：旧写法 `addr:port[@host[:port]][/path]`，承载取该连接在 `-type` 中对应的值。与 URL 一一对应：`1.2.3.4:443@a.com:443/ws`（`-type=wss`）就是 `wss://1.2.3.4:443/ws?host=a.com:443`；quic 的旧写法把 ALPN 放在路径里（`a.com/h3`），照旧可用，URL 写法用 `?alpn=`。两种写法可以在一个连接里混用。

```bash
# 一个连接，三个候选：先 tls，失败换 wss，最后才用 b.com 的 tcp
./npc -server="tls://a.com:8025|wss://a.com:443/ws?tier=1|tcp://b.com:8024?standby=1" -vkey="xxx"
# 两个连接：第一个两种候选，第二个用旧写法和 -type 中的 kcp
./npc -server="tls://a.com|a.com:8024,c.com:8024" -vkey="k1,k2" -type="tcp,kcp"
```

多管道（`-pipes=multi`）的语义与后续版本一致，当前版本只解析、校验并保存这些参数，运行时仍是单管道：

- 只有 `tier` 不超过最优 tier + 1 的非 standby 候选参与，每个候选按 `count` 建管道；
- 会话先在一个含 `bidi` 的候选上建立，只有 `up` 或 `down` 的候选只承载那个方向；
- `standby` 候选也建好管道，但只在非 standby 管道全部失效时才承载流量（单管道下 standby 只是排在最后）。

```bash
./npc -pipes=multi -server="tls://a.com:8025?tier=0&count=5|wss://b.com/ws?tier=0&dirs=up&count=3|tcp://c.com:8024?tier=1&dirs=down&count=5" -vkey="xxx"
```

### 引号

值里有 `|`、`&`、`?` 等字符时必须加引号，建议所有值都用**双引号**：

- Linux/macOS shell、PowerShell：双引号或单引号都可以；
- Windows cmd 只认双引号（`|`、`&` 在双引号外会被 cmd 解释）；
- `.bat`/`.cmd` 脚本中 `%` 要写成 `%%`（例如 URL 里的 `%2F`）。

PowerShell 会把不加引号的 `-server=1.2.3.4:8024` 在第一个 `.` 处拆成两个参数，npc 和 nps 会把它们重新拼起来，但仍建议加引号。

## 旧写法

旧版安装的服务会保留当时的参数，更新后照常启动：

| 参数 | 处理 |
| --- | --- |
| `-tls_enable`（含 `=true`） | 旧写法，等同 `-type=tls`，覆盖 `-type`；`=false` 无作用 |
| `-proto_version` | 忽略并告警：npc 只使用最新协议，服务端较旧时请先升级服务端 |
| `-stun_addr` | 忽略并告警：STUN 服务器改在服务端 `p2p_stun_servers` 设置 |
| `-p2p_type`、`-log_daily` | 忽略并告警，已无作用 |

其他不认识的参数忽略并告警。

环境变量：`NPC_SERVER_ADDR`、`NPC_SERVER_VKEY`、`NPC_CONFIG_PATH`、`NPC_LOCAL_IP`、`NPC_LAUNCH`。

`NPC_LAUNCH` 只在命令行没有 `-launch` 时读取，不与命令行合并。多组只按换行分隔（空行忽略），不按逗号，因为链接的查询里有 `alpn=h3,nps`、`http=h2,h3` 这样的逗号；每行可以是启动链接、URL 或 `@文件`。折行的 Base64 不能放在里面，用 `npc launch pack` 生成一行，或用 `@文件`。

## 安装为服务

```bash
./npc install -launch="npc://VKEY@host:8025?type=tls" -log=off
```

链接写入安装目录下仅属主可读的 `conf/npc-service-launch-N.conf`，`-L` 写入 `conf/npc-service-local.conf`，服务定义只带 `@文件`。`-server`/`-vkey` 的安装写到 `conf/npc-service.json`，服务定义同样是 `-launch=@文件`。

旧参数 `-local_type=p2p|p2ps|p2pt|secret`、`-target_type`、`-fallback_secret`、`-disable_p2p` 仍可使用，自动换算为 `-local_type` 和 `-conn_mode` 并告警，对照表见[点对点访问](/guide/tunnels/secret.md#旧版写法)。

## launch 子命令

```bash
npc launch pack npc.jsonc                # 输出 gzip+base64，可作 -launch 的值；含明文密钥
npc launch show -launch=… -L=… [--reveal]  # 规范化打印，默认遮盖 vkey、access 和代理密码；也接受旧参数并输出新写法
```
