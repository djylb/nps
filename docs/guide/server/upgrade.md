# 升级到 0.35

本页讲从 0.34 及更早版本升级到 0.35 的步骤，以及旧配置在 0.35 里怎么处理。只想更新 0.35 的小版本，直接看[运维](/guide/server/operations.md)里的 `nps update`。

先说结论：

- **旧配置文件不用改就能继续运行。** 旧的键、旧的命令行参数、已存储的隧道会被自动换算，并在日志里留下一条警告，告诉你该改成什么。改不改、什么时候改，由你决定。
- **升级顺序：先升级 nps（服务端），再升级 npc（客户端）。** 0.35 的 npc 不能连接旧版服务端；而旧版 npc 连接 0.35 的 nps，在下表的范围内可以继续使用，见[版本规则与兼容范围](/reference/versioning.md)。
- **升级前先备份。** 0.35 第一次启动时会把已存储的旧写法隧道换算成新写法并写回数据文件，写回之后旧版本未必还能读。

## 升级步骤

### 1. 升级前

1. 浏览仓库的 [CHANGELOG](https://github.com/djylb/nps/blob/master/CHANGELOG.md)，看有没有影响你的变化。下文“行为变化”一节列了最需要留意的几项。
2. 按[备份与恢复](/guide/server/backup.md)备份 `conf` 目录（配置、数据文件和实例主密钥）。
3. 对照[版本规则与兼容范围](/reference/versioning.md)，确认你现有的 npc 版本能连上 0.35；`npc -version` 可以看版本。

### 2. 升级 nps

选一种方式：

- **用更新命令**（0.34 及更早的 nps 也支持，会直接更新到 0.35）：

  ```bash
  sudo nps update   # Linux、macOS
  nps.exe update    # Windows，用管理员身份运行
  ```

  已有的 `conf` 目录不会被覆盖，没有的配置参考文件会补上。如果你的 nps 比 0.34.8 还旧、而且不是用 `nps install` 装到系统目录的，网页文件可能没有更新，管理页面会报错（隧道不受影响），这时用发布包里的 `web` 目录手动覆盖；0.26 版本线的 `update` 要求已经执行过 `nps install`，否则请直接手动替换。
- **手动替换**：到[发布页](https://github.com/djylb/nps/releases/latest)下载新版本，覆盖 `nps` 程序和 `web` 目录，步骤见[运维：手动替换程序](/guide/server/operations.md?id=手动替换程序)。
- **Docker 换镜像**：数据都在挂载的目录里，换镜像不丢配置。

  ```bash
  docker pull duan2001/nps
  docker rm -f nps
  docker run -d --restart=always --name nps --net=host -v <local_conf_dir>:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
  ```

  容器启动时，镜像自带的 Geo 数据比挂载目录里的新才会覆盖；已有的 `nps.conf` 不会被改动。官方镜像默认禁止网页更新，所以 Docker 部署只能这样升级。

升级后建议重新安装一次系统服务（`sudo nps install`，必要时带上原来的 `-conf_path`），这样 0.35 新增的 systemd 设置（等待网络就绪再启动）才会生效，不重装也不影响使用。

### 3. 检查日志里的警告

重启后看 nps 的日志（位置见[日志](/guide/server/logging.md)），搜 `legacy config setting`：每一条都是一个被自动换算或被忽略的旧写法，`detail` 里写着该怎么改。没有警告说明配置已经是当前写法。

登录网页确认：客户端在线、隧道和域名都在、点对点访问的隧道仍是“点对点访问”。

### 4. 升级 npc

- 版本在 0.34.9 及以上的 npc：管理员在“客户端”页对客户端点更新，服务端会下发更新指令，npc 更新后自动重连。
- 更旧的 npc 收到“不支持远程更新”的提示，需要在客户端机器上执行 `npc update`（Docker 的 npc 换镜像）。
- 先在一个客户端上试，确认连接和隧道正常，再升级其余的。

0.35 的 npc 对旧写法同样兼容：`npc.conf`、`-server`、`-vkey`、`-type` 照常使用，旧的参数和配置键换算后告警。想改成当前写法，见[客户端配置](/reference/npc-config.md)和[启动链接](/reference/npc-launch.md)。

## 行为变化

升级后这些行为和以前不同：

| 变化 | 说明 |
| --- | --- |
| 日志重写 | 默认日志级别从 `trace` 变为 `info`；一个事件一行（logfmt，可选 JSON）；`log_color` 无作用。见[日志](/guide/server/logging.md) |
| 重载配置 | `nps reload` 命令移除，改为发 `SIGUSR1` 或在网页“全局参数”页点“重新加载配置”，见[运维](/guide/server/operations.md) |
| 点对点访问 | 私密代理（secret）与 P2P 合并为“点对点访问”。网页和接口不再接受 `mode=p2p`；0.34 及更早的 npc 不能参与新的点对点访问（访问端会被拒绝，提供端只能被中转访问） |
| 命令行参数 | nps、npc 遇到不认识的参数时忽略并告警，不再拒绝启动 |
| 健康检查 | 改为在服务端按隧道、域名配置；旧 npc 自己配置并上报的检查被忽略 |
| 访客限制 | `ip_limit` 移除，改为在客户端、隧道、域名上单独开启“仅允许已登记的 IP”，见[访问控制](/guide/server/access-control.md) |
| 监听端口 | 新安装只开一个混合端口 `mix_port`；旧的 `bridge_*` 端口配置仍然读取，已弃用 |
| 服务安装 | `npc install` 不再把 vkey 等写进服务定义，改写到安装目录 `conf/` 下仅属主可读的文件（见[连接与系统服务](/guide/client/connect.md)）；已安装的服务需要重新 `npc install` 才改成这样 |
| 更新范围 | `update`、`update-force`、网页更新不跨大版本，大版本升级需手动进行 |

## nps.conf 旧键对照

| 旧写法 | 0.35 怎么处理 | 应该改成 |
| --- | --- | --- |
| `bridge_port`、`bridge_tcp_port`、`bridge_tls_port`、`bridge_ws_port`、`bridge_wss_port`、`bridge_quic_port`、`bridge_kcp_port`，以及它们的 `*_ip`、`bridge_type`、`tcp_enable` 等 `*_enable` | 继续读取，已弃用 | `mix_port`、`mix_ip`，见下文“迁移到混合端口” |
| `tls_bridge_port` | 当作 `bridge_tls_port` 读取并提示改名；两个都写时 `bridge_tls_port` 优先 | `mix_port` |
| `new_protocol` | 已移除，忽略并告警 | 删掉 |
| `log_color` | 已移除，忽略并告警 | 删掉；用 `log_format` 选格式 |
| `ip_limit` | 已移除，设置了会告警 | 在客户端、隧道或域名上开启“仅允许已登记的 IP” |
| `allow_secret_local` | 已移除，写了 `true` 会告警 | 点对点访问的 `local_proxy` 在隧道开启“代理到服务器本地”且 `allow_local_proxy=true` 时生效 |
| `allow_secret_link` | 已移除，写了会告警 | 删掉；点对点访问的访问端可以指定任意目标，要限制请在隧道的目标名单里配置 |
| `quic_alpn` 里含 `h3` | 自动从中去掉并告警；去空后改用 `nps` | 删掉 `h3`；旧 npc 要用同一个 ALPN 连接 |

`log_level` 不再是旧版的数字级别，写 `trace`、`debug`、`info`、`warn`、`error`、`off`，可按模块设置，见[日志](/guide/server/logging.md)。KCP 已弃用，请改用 `quic`。

### 迁移到混合端口

只写了 `bridge_port`（没有写各协议端口）的旧配置，0.35 照旧把这个端口当作完整的混合端口，所有旧连接方式都可以继续连，没有急着改的必要。想改成当前写法时，把 `bridge_*` 一组换成：

```ini
mix_port=8024
```

换之前请对照你现有 npc 的连接方式（`-type` 或 `npc.conf` 的 `conn_type`）：

| 现有 npc 的连接方式 | 在 `mix_port` 上 |
| --- | --- |
| `tls`、`quic` | 直接可用，0.34 及更早的 npc 也可以 |
| `wss` | 只有 0.35 的 npc 可以。0.34 及更早的 npc 不能通过混合端口使用 `ws`、`wss`，请改成 `tls`，或保留原来的 `bridge_ws_port`、`bridge_wss_port` |
| `tcp`、`ws`（明文） | 默认不接受。要么把 npc 改成 `tls`、`wss`，要么设 `mix_plain=true`（只在前面有自己的 TLS 反向代理，或可信内网中使用）；`ws` 同样只限 0.35 的 npc |
| `kcp` | 混合端口不再接 KCP，请把 npc 改成 `quic`；暂时不能改的，保留 `bridge_kcp_port` |

0.34 及更早的 npc 连混合端口，要求 `mix_legacy` 保持默认的 `true`；所有 npc 都升级之后可以关掉它。防火墙别忘了同时放行同一个端口号的 TCP 和 UDP。

## npc 旧写法对照

`npc.conf` 本身没有弃用，下面是其中被换算或忽略的旧写法：

| 旧写法 | 0.35 怎么处理 | 应该改成 |
| --- | --- | --- |
| `[common]` 里的 `new_protocol` | 忽略并告警 | 删掉 |
| `[common]` 里的 `tls_enable=true` | 当作 `conn_type=tls` 并告警，同旧版一样覆盖 `conn_type` | `conn_type=tls` |
| 任务里的 `[health_…]` 配置 | 忽略并告警，不再由 npc 检查 | 在网页上按隧道、域名配置健康检查，见[健康检查](/guide/tunnels/health-check.md) |
| `mode=p2p` 的任务、`[p2p…]` 节 | 换算成点对点访问并告警，密钥不变，访问端照常可用 | `[secret…]` 节，用 `local_type` 和 `conn_mode` |
| `local_type=secret/p2p/p2ps/p2pt`、`target_type`、`fallback_secret` | 换算成 `local_type` 加 `conn_mode` 并告警 | 见下面的换算表 |
| 没写 `local_type` 和 `conn_mode` 的 `[secret…]` 节 | 保持经 nps 中转，不会突然开始打洞 | 想尝试直连，加 `conn_mode=auto` |
| 把任务定义在客户端配置里（隧道、`file`、`p2p` 等） | 继续读取，已弃用；`file` 隧道自动变成虚拟后端加一条 TCP 隧道 | 在网页上建隧道，或用[虚拟后端](/guide/tunnels/virtual-backend.md) |

点对点访问旧写法的换算：

| 旧写法 | 现在 |
| --- | --- |
| `local_type=p2p`（或 `[p2p…]` 节） | `conn_mode=auto`；写了 `fallback_secret=false` 则 `conn_mode=direct` |
| `local_type=secret` | `conn_mode=relay` |
| `local_type=p2ps` | `local_type=mixed` |
| `local_type=p2pt` | `local_type=transparent` |
| `target_type=tcp` / `udp` | `local_type=tcp` / `udp`（其他取默认的 `tcp_udp`） |

npc 命令行的旧参数：

| 旧参数 | 0.35 怎么处理 |
| --- | --- |
| `-tls_enable` | 当作 `-type=tls` 并告警 |
| `-local_type=p2p/p2ps/p2pt/secret`、`-target_type`、`-fallback_secret`、`-disable_p2p` | 按上表换算成 `-local_type` 和 `-conn_mode` 并告警，`-disable_p2p` 得到 `relay` |
| `-new_protocol`、`-proto_version` | 忽略并告警。请先升级服务端，再升级 npc |
| `-stun_addr`、`-p2p_type`、`-log_daily`、`-log_color` | 已无作用，忽略并告警；STUN 服务器改在 nps.conf 的 `p2p_stun_servers` 设置 |
| `-server`、`-vkey`、`-type`、`-config` | 继续可用，已弃用，由 `-launch` 取代；只给了 `-server`、`-vkey` 之一会报错并指出缺哪个 |
| `-password` | 已弃用，改用 `-L` 的 `access=`；与 `-launch`、`-L` 同时给出时报错 |

## 已存储数据的换算

nps 启动时读取 `conf` 目录下的 `tasks.json` 等数据文件，下面这些旧写法的隧道会被换算，并立即写回文件：

| 旧隧道 | 现在 |
| --- | --- |
| `socks5`、`httpProxy` 模式 | 混合代理 |
| `p2p` 模式（密钥不变） | 点对点访问 |
| `file` 模式 | 一个虚拟后端，加上每个监听端口各一条指向它的 TCP 隧道；新隧道端口被占用时不创建，后端保留，可以手动补建 |

旧版保存的 IP 黑名单无需转换，原样按新规则解析：空行被忽略，无法解析的条目（旧版按字符串精确匹配，本来就不会命中）被跳过并记录日志。

数据文件读不出来时，原文件会另存为 `.bad` 备份，不会被直接覆盖。导入导出的旧文件也会做同样的换算，见[备份与恢复](/guide/server/backup.md)。

## 回退

升级前没有备份就不要回退。有备份时：

1. 停止 nps 和相关的 npc。
2. 把旧版本的 `nps` 程序和 `web` 目录放回原位（到[发布页](https://github.com/djylb/nps/releases)下载旧版本；Docker 部署改回旧镜像）。`nps update-force` 只会安装最新发布版，不能用来退回。
3. 用升级前的备份覆盖整个 `conf` 目录，包括 `nps.conf`、`tasks.json`、`hosts.json`、`clients.json`、`global.json` 和 `instance.key`。
4. 启动 nps，再启动 npc。

覆盖备份意味着升级后在网页上做的修改和累计的流量都会丢失。已经升级到 0.35 的 npc 不能连接旧版服务端，回退服务端时需要把这些 npc 一起退回。
