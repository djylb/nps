# npc 命令行参数

本页列出 npc 的全部子命令、参数和环境变量。第一次使用请看[连接与系统服务](/guide/client/connect.md)。启动链接和 `-L` 的格式见[启动链接与配置格式](/reference/npc-launch.md)。

## 参数的写法

```bash
npc [子命令] [参数]
```

- `-name=value`、`--name=value`、`-name value`、`--name value` 都可以。布尔参数写 `-name` 表示 `true`，要关闭写 `-name=false`。
- 参数名里的 `-`、`.` 和 `_` 等价，`-log-level` 就是 `-log_level`。
- 参数和子命令的先后顺序不限。
- 不认识的参数不会让 npc 启动失败：npc 忽略它（连同紧跟的值）并在日志里写一条警告。参数拼错时请留意这条警告。
- 值里有 `|`、`&`、`?`、`;` 时必须加引号，建议一律用双引号。Windows cmd 只认双引号；`.bat`、`.cmd` 脚本里 `%` 要写成 `%%`。
- PowerShell 会把不加引号的 `-launch=1.2.3.4` 这类值在第一个 `.` 处拆开，npc 会重新拼接，但仍建议加引号。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| `install` | 把 npc 安装为系统服务，并记下本次给出的参数。再次执行会先卸载旧服务再安装。参数如何保存见[连接与系统服务](/guide/client/connect.md) |
| `start` | 启动系统服务 |
| `stop` | 停止系统服务 |
| `restart` | 重启系统服务。修改配置文件后用它生效 |
| `uninstall` | 卸载系统服务，`conf` 目录里的文件保留 |
| `status` | 向服务端查询这个 vkey 的隧道和域名转发状态，打印后退出。只读 `-server`、`-vkey`、`-type`、`-proxy`、`-local_ip`，不读 `-launch` 和环境变量 |
| `register` | 向服务端登记本机当前 IP，有效期由 `-time` 决定，登记后退出。读取的参数同 `status`。登记的用途见[访问控制](/guide/server/access-control.md) |
| `update` | 更新到最新发布版本，已是最新时什么也不做。见[更新与维护](/guide/client/maintenance.md) |
| `update-force` | 无论当前版本如何，都安装最新发布版本 |
| `version` | 打印版本，同 `-version` |
| `launch pack <文件>` | 把文件内容压缩（gzip + base64）成一行，可作 `-launch` 的值。输出里含明文密钥，请当作密钥保管。文件只是引用另一个文件或网址时报错 |
| `launch show` | 打印当前参数相当于的 `-launch` 和 `-L` 写法后退出。默认遮盖 vkey、访问密钥和代理密码，加 `--reveal` 才显示。也接受已弃用的参数，可用来换算旧命令 |

不带子命令时，npc 在前台运行。`-h`、`--help` 打印全部参数。

## 连接

| 参数 | 默认值 | 取值与说明 |
| --- | --- | --- |
| `-launch` | 无 | 一条连接，可重复。可以是 `npc://` 启动链接、`@文件`、`http(s)://` 网址或 base64，格式见[启动链接与配置格式](/reference/npc-launch.md)。不给时读环境变量 `NPC_LAUNCH`。不能和 `-server`、`-vkey` 同时使用 |
| `-L`、`--local` | 无 | 一个本地点对点访问，可重复，也可写 `@文件`（每行一条）。格式见[启动链接与配置格式](/reference/npc-launch.md) |
| `-config`、`-c` | 见说明 | INI 配置文件路径，多个用逗号分隔，格式见 [npc.conf 配置](/reference/npc-config.md)。没有 `-config`，也没有 `-launch`、`-server`、`-vkey` 时，读 `conf/npc.conf`（Linux、macOS 相对当前目录，Windows 相对 npc.exe 所在目录）。可以和 `-server`、`-vkey` 同时使用，两边都运行。和 `-launch` 同时给出时被忽略并报警告 |
| `-server`、`-s` | 无 | **已弃用（用于启动连接时）**，改用 `-launch`。服务端地址，详见下文[已弃用的写法](#已弃用的写法)。`status` 和 `register` 仍然用它。必须和 `-vkey` 成对给出，只给一个时 npc 报错退出。和 `-launch` 同时给出时报错 |
| `-vkey`、`-k` | 无 | **已弃用（用于启动连接时）**，改用 `-launch`。客户端密钥，在网页“客户端”列表的“唯一验证密钥”处查看。`status` 和 `register` 仍然用它 |
| `-type`、`-t` | `tcp` | **已弃用（用于启动连接时）**，改用链接的 `type=`。取值 `tcp`、`tls`、`kcp`、`quic`、`ws`、`wss`。只对不带 `://` 的地址起作用，所有地址都带 `://` 时报警告；`kcp` 报警告后按 `quic` 连接。`status` 和 `register` 仍然用它。和 `-launch` 同时给出时被忽略并报警告 |
| `-proxy` | 无 | **已弃用（用于启动连接时）**，改用链接的 `proxy=`。经代理连接服务端，支持 `http`、`https`、`socks5`、`socks5h`、`socks4`、`socks4a`，如 `socks5://user:pass@127.0.0.1:1080`；不写协议的 `host:port` 按 HTTP 代理处理。`status` 和 `register` 仍然用它。和 `-launch` 同时给出时被忽略并报警告 |
| `-local_ip` | 无 | **已弃用（用于启动连接时）**，改用链接的 `source_addr=`。连接服务端时使用的本机源地址。`status` 和 `register` 仍然用它。和 `-launch` 同时给出时被忽略并报警告 |
| `-local_ip_forward` | `false` | 让出站源地址同时用于隧道转发（只对公网 IP 和域名目标生效） |
| `-auto_reconnect` | `true` | 断线后自动重连。`false` 时任一连接结束后 npc 退出 |
| `-skip_verify` | `false` | 跳过服务端证书校验 |
| `-disable_remote_update` | `false` | 忽略网页发来的远程更新请求 |
| `-disconnect_timeout` | `30` | 保留项，无需设置 |
| `-keepalive` | `0` | 秒。连接保活周期，`0` 使用内置默认值 |
| `-p2p_timeout` | `5` | 保留项，无需设置 |

未配置 `-proxy` 或 `proxy=` 时，npc 读取环境变量 `ALL_PROXY`，并按 `NO_PROXY` 跳过匹配的地址。

## 连接的高级设置

这些参数是所有连接的默认值：启动链接或配置文件里写了同名设置时，以它们自己写的为准。一般不需要修改。

| 参数 | 默认值 | 取值与说明 |
| --- | --- | --- |
| `-pipes` | `single` | `single` 或 `multi`。一条连接里同时使用一条还是多条路径。写错时 npc 报错退出 |
| `-scheduler_up` | 空（即 `affinity`） | 多条路径时上行流量的分配方式：`affinity` 固定路径、`session_affinity` 每个会话固定一条路径、`spread_eta` 按预计到达时间分流、`spread_round_robin` 轮流使用各路径、`spread_rate_weighted` 按速率加权分流。写错时 npc 报错退出 |
| `-session_max_lanes` | `0` | 每条 QUIC 路径接受的并行通道数，`0` 表示 16，`1` 表示关闭 |
| `-session_resume_timeout` | `0` | 秒。连接全部断开后，等待恢复的时间。`0` 表示 30，负数表示不恢复。服务端也设置了时以服务端为准 |
| `-session_usage_interval` | `0` | 秒。上报本客户端提供的点对点直连流量的间隔。`0` 表示 60，负数表示不上报 |
| `-relay_idle` | `0` | 秒。点对点直连建立后，中转通道继续保留的时间。`0` 表示 60 |
| `-p2p_visitor_keep_server` | `false` | 点对点访问端空闲时也保持与服务端的连接 |
| `-new_protocol_pin` | 无 | 服务端密钥标识（十六进制）。填写后只连接持有这个密钥的服务端 |
| `-compress_max_window` | `0` | 字节。压缩连接允许的最大 zstd 窗口，`0` 表示 8 MiB |
| `-compress_max_ratio` | `0` | 压缩连接允许的最大膨胀倍数，`0` 表示 4096，负数表示不检查 |
| `-socks5_udp_idle_timeout` | `300` | 秒。作为出口时，SOCKS5 UDP 关联没有流量后保留的时间 |

## 点对点访问（单条，已弃用）

这组参数只能描述一个访问，已弃用，请改用 [`-L`](/reference/npc-launch.md)。它们和 `-launch` 或 `-L` 同时给出时 npc 报错退出。设置了 `-password` 时，`-config` 不再读取。

| 参数 | 默认值 | 取值与说明 | 替代写法 |
| --- | --- | --- | --- |
| `-password` | 无 | **已弃用。** 点对点隧道的访问密钥 | `-L` 的 `access=` |
| `-local_type` | `tcp_udp` | **已弃用。** `tcp`、`udp`、`tcp_udp`、`mixed`、`socks5`、`http`、`transparent` | `-L` 的协议名 |
| `-local_port` | `2000` | **已弃用。** 本地监听端口 | `-L` 的端口 |
| `-target` | 无 | **已弃用。** 要访问的目标 | `-L` 的 `target=` |
| `-conn_mode` | `auto` | **已弃用。** `auto`、`relay`、`direct` | `-L` 的 `conn_mode=` |
| `-local_proxy` | `false` | **已弃用。** 让服务端连接目标 | `-L` 的 `local_proxy=` |

## 日志

| 参数 | 默认值 | 取值与说明 |
| --- | --- | --- |
| `-log` | `file`（OpenWrt 上为 `off`） | `stdout`、`file`、`both`、`off`（`false` 同 `off`）。写了就按写的输出 |
| `-debug` | `true` | 没有写 `-log` 时，让 npc 输出到控制台。安装的服务固定带 `-debug=false`，按 `-log` 输出。它不影响日志级别 |
| `-log_level` | `info` | `trace`、`debug`、`info`、`warn`、`error`、`off`，可以按模块单独设置，如 `info,p2p=debug` |
| `-log_format` | `auto` | `auto`（终端上彩色，其他情况 logfmt）、`text`（logfmt）、`json` |
| `-log_path` | 空 | 日志文件路径。空、`on`、`true` 表示默认位置：Linux、macOS 为 `/var/log/npc.log`，Windows 为 npc.exe 所在目录下的 `npc.log`。`off`、`false`、`docker`、`/dev/null` 表示不写文件，此时 `file`、`both` 改为输出到控制台 |
| `-log_max_size` | `5`（OpenWrt 上为 `1`） | MB。单个日志文件的大小上限，`0` 表示不按大小切分 |
| `-log_max_days` | `7` | 旧日志保留天数，`0` 表示不限 |
| `-log_max_files` | `10`（OpenWrt 上为 `1`） | 保留的文件数，`0` 表示不限 |
| `-log_compress` | `false` | 压缩切分后的日志 |
| `-log_instance` | 空 | 每行日志写上 `instance=名称`，用于区分多个 npc |
| `-log_secrets` | `false` | 在日志里写出密钥和口令，仅供调试，生产环境不要打开 |

## 其他

| 参数 | 默认值 | 取值与说明 |
| --- | --- | --- |
| `-dns_server` | `8.8.8.8` | 解析域名用的 DNS 服务器，不可达时使用系统 DNS |
| `-ntp_server` | 无 | NTP 服务器，用于校准时间 |
| `-ntp_interval` | `5` | 分钟。两次 NTP 同步的间隔 |
| `-timezone` | 无 | 时区，如 `Asia/Shanghai` |
| `-pprof` | 无 | PProf 调试地址 `ip:port` |
| `-time` | `2` | 小时。`register` 登记的有效期 |
| `-reveal` | `false` | 只用于 `npc launch show`：显示 vkey、访问密钥和代理密码 |
| `-gen2fa` | `false` | 生成一个 TOTP 密钥后退出 |
| `-get2fa` | 无 | 根据 TOTP 密钥打印当前验证码后退出 |
| `-version`、`-v` | `false` | 打印版本后退出 |

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `NPC_LAUNCH` | 命令行没有 `-launch` 时读取，不和命令行合并。每行一个输入，只按换行分隔，空行忽略。详见[启动链接与配置格式](/reference/npc-launch.md)。`npc install` 也读取它 |
| `NPC_SERVER_ADDR` | **已弃用。** `-server` 的默认值 |
| `NPC_SERVER_VKEY` | **已弃用。** `-vkey` 的默认值 |
| `NPC_LOCAL_IP` | **已弃用。** `-local_ip` 的默认值 |
| `NPC_CONFIG_PATH` | `-config` 的默认值 |
| `ALL_PROXY`、`NO_PROXY` | 没有配置代理时使用，见上文 |

`NPC_SERVER_ADDR`、`NPC_SERVER_VKEY`、`NPC_LOCAL_IP`、`NPC_CONFIG_PATH` 只在启动连接时读取，`status`、`register`、`install` 不读取。变量的值不能包含 `=`。

## 已弃用的写法

### -server 的地址格式

`-server` 的值分两层：

- 一个 `-server` 可以写多条**连接**，每条连接各自对应 `-vkey`、`-type`、`-local_ip` 中同一位置的值，较短的列表重复最后一个值。没有 `://` 时用逗号分隔；只要出现 `://`，就改用分号分隔，这几个列表也随之用分号。
- 一条连接里可以写多个**路径**，用 `|` 分隔。每个路径可以是 URL（格式和参数见[启动链接与配置格式](/reference/npc-launch.md)，此处多接受 `kcp://`，报警告后按 `quic` 连接），也可以是不带协议的 `地址:端口[@主机[:端口]][/路径]`，此时协议取 `-type`。

替代写法：每条连接写一个 `-launch`。用 `npc launch show` 可以换算：

```bash
npc launch show -server="203.0.113.10:8024" -vkey="YOUR_VKEY" -type=tls --reveal
```

### 已移除的参数

服务升级后会保留当初安装时的参数，所以下面这些参数不会导致启动失败。除非另有说明，所有处理都会在日志里写一条警告。

| 参数 | 处理方式 | 替代写法 |
| --- | --- | --- |
| `-tls_enable`、`-tls_enable=true` | 被翻译为 `-type=tls`，覆盖 `-type` | 链接的 `type=tls` |
| `-tls_enable=false` | 被忽略 | 删除 |
| `-new_protocol` | 被忽略 | 删除 |
| `-proto_version` | 被忽略 | 删除；服务端版本较旧时先升级服务端 |
| `-stun_addr` | 被忽略 | 在服务端设置 `p2p_stun_servers`，见[服务端配置](/reference/server-config.md) |
| `-p2p_type` | 被忽略 | 删除，npc 自动选择 |
| `-log_color` | 被忽略 | `-log_format` |
| `-log_daily` | 被忽略 | `-log_max_size`、`-log_max_days` |
| `-local_type=p2p` | 被翻译：`-local_type` 取决于 `-target_type`，`-conn_mode=auto`（`-fallback_secret=false` 时为 `direct`） | `-L` |
| `-local_type=p2ps` | 被翻译为 `-local_type=mixed`，`-conn_mode` 同上 | `-L="mixed://…"` |
| `-local_type=p2pt` | 被翻译为 `-local_type=transparent`，`-conn_mode` 同上 | `-L="transparent://…"` |
| `-local_type=secret` | 被翻译为 `-conn_mode=relay`，`-local_type` 取决于 `-target_type` | `-L` 的 `conn_mode=relay` |
| `-target_type` | 被翻译：`tcp` 为 `-local_type=tcp`，`udp` 为 `-local_type=udp`，其他为 `tcp_udp` | `-L` 的协议名 |
| `-fallback_secret`、`-fallback_secret=true` | 被翻译：`-conn_mode=auto` | `-L` 的 `conn_mode=auto` |
| `-fallback_secret=false` | 被翻译：`-conn_mode=direct` | `-L` 的 `conn_mode=direct` |
| `-disable_p2p`、`-disable_p2p=true` | 被翻译为 `-conn_mode=relay`，优先于上面几项 | `-L` 的 `conn_mode=relay` |

同时写了现行的 `-local_type` 或 `-conn_mode` 时，以现行写法为准。
