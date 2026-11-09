# 排错

出了问题，先看日志。nps 和 npc 都会把重要的事件写成一行带固定事件名的日志（`event=…`），按事件名搜索，通常一两步就能定位。本页按现象组织：

- [npc 连不上服务端](/guide/server/troubleshooting.md?id=npc-连不上服务端)
- [登录不了网页管理](/guide/server/troubleshooting.md?id=登录不了网页管理)
- [客户端在线，但隧道不通](/guide/server/troubleshooting.md?id=客户端在线但隧道不通)
- [证书失败](/guide/server/troubleshooting.md?id=证书失败)
- [端口冲突](/guide/server/troubleshooting.md?id=端口冲突)
- [其他常见问题](/guide/server/troubleshooting.md?id=其他常见问题)

## 先看日志

**nps 的日志在哪**：直接在终端运行时在终端里；用 `nps install` 装成服务（Docker 也是）后写到文件，默认是安装目录下 `conf/nps.log`（Linux 上通常是 `/etc/nps/conf/nps.log`），具体看 `nps.conf` 的 `log_path`，详见[日志](/guide/server/logging.md)。**npc 的日志**默认写 `/var/log/npc.log`（Windows 在程序目录下的 `npc.log`），在终端里手动运行时直接显示在终端。

**按事件名搜索**，例如：

```bash
grep -E 'event=(auth_failed|listen_failed|relay_failed|cert_failed)' /etc/nps/conf/nps.log | tail -20
```

**日志不够详细时**，在 `nps.conf` 里把相关模块调到 `debug`，然后[重载配置](/guide/server/operations.md)（不用重启），问题复现后再改回来：

```ini
log_level=info,bridge=debug
```

npc 用 `-log_level=info,client=debug`。模块名见[日志](/guide/server/logging.md)。把日志发给别人之前先检查一遍，默认情况下密钥和口令已被隐藏，但域名、IP 地址仍然会出现。

## npc 连不上服务端

先在 npc 的日志里找 `connect_failed`，看 `reason`；再到 nps 的日志里找同一时间的 `auth_failed`。

| 看到什么 | 说明 | 怎么处理 |
| --- | --- | --- |
| npc：`connect_failed`，`reason=dial` | 网络层就没连上：地址或端口写错、防火墙或云服务器安全组没放行、域名解析失败、服务端没在运行 | 确认地址和端口。`mix_port` 要**同时放行 TCP 和 UDP**（`quic` 走 UDP）。用 `nc -vz 203.0.113.10 8024` 测 TCP 是否通；Docker 部署的 nps 要用 `--net=host`，或映射这个端口 |
| npc：`connect_failed`，`reason` 是 `send_type`、`signal_stream`、`no_tunnel` 或 `establish` | 连上了，但握手中途被断开 | 多半是连接方式和服务端不匹配：见下面的表；也可能被 nps 的全局黑白名单拒绝（被拒绝的连接会被直接断开），查看“全局参数”页的名单 |
| nps：`auth_failed`，`role=client`，`reason=bad_key` | vkey 不对 | 到网页“客户端”页，复制该客户端“唯一验证密钥”；或在“启动命令”页直接复制完整命令，例如 `npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"`。日志里为了安全不会显示这个 vkey |
| nps：`auth_failed`，`reason=disabled` | 客户端被停用了 | 在网页客户端页启用它 |
| nps：`auth_failed`，`reason=version` | npc 的协议版本不被接受 | `secure_mode=true` 时 0.33 以前的 npc 连不上，请升级 npc，或评估后设 `secure_mode=false`；0.35 的 npc 也不能连接旧版服务端。见[版本规则与兼容范围](/reference/versioning.md) |
| nps：`auth_failed`，`reason=timeout` 或 `malformed` | 对方没有按约定完成握手：连接方式不对、中间有设备改写了流量，或者根本不是 npc（端口扫描） | 检查连接方式；`secure_mode=true` 时两端系统时间相差超过 `auth_time_window`（默认 300 秒）也会被拒绝，请校准时间 |
| nps：`auth_failed`，`reason=forbidden` | 这个密钥不允许做这件事，例如访客密钥 `visitor_vkey` 只能访问点对点隧道 | 换成客户端自己的 vkey |
| nps：`limit_exceeded`，`kind=pipes` | 该客户端的连接通道数超过了 `client_max_pipes` | 调大或设为 `0`（不限制） |
| nps：`client_offline`，`reason=replaced` | 同一个客户端的新连接取代了旧的 | 偶尔出现是重连；反复出现说明同一个 npc 被重复启动了 |

连接方式（`type=`）要和服务端的监听对得上：

- 0.35 的 nps 发布包默认只开混合端口 `mix_port=8024`，它接受 `tls`、`wss` 和 `quic`。`type=tcp`、`type=ws` 这类明文连接默认**会被忽略**（`mix_plain=false`），npc 表现为连接超时或握手失败；改用 `type=tls`，或在 nps 前面有自己的 TLS 反向代理时设 `mix_plain=true`。
- 0.34 及更早的 npc 通过混合端口连接时，`ws`、`wss` 不可用，请用 `tls` 或 `quic`，详见[升级到 0.35](/guide/server/upgrade.md)。
- 如果连接要经过 CDN 或反向代理，确认它放行了 WebSocket。

Windows 7 上 npc 无法启动，或前台能连接、服务却显示离线，见 [Windows 7](/getting-started/install-windows.md?id=windows7)。

## 登录不了网页管理

打不开页面：确认 `web_port` 不是 `0`（`0` 关闭网页管理），地址是 `http://203.0.113.10:8081`（注意是 http 还是 https，取决于 `web_open_ssl`），防火墙放行了这个端口。nps 日志里的 `listening`（`route=web`）说明它在监听，`listen_failed` 说明端口被占用，见下文“端口冲突”。

能打开但登录失败：看 nps 日志的 `login_failed`，`reason` 说明原因：

| `reason` | 说明 | 怎么处理 |
| --- | --- | --- |
| `bad_password` | 账号或密码错 | 发布包自带的是 `admin` / `123`。忘记密码时，直接改 `nps.conf` 里的 `web_username`、`web_password`，然后[重载](/guide/server/operations.md)即可，不用重启。设置了 `totp_secret` 的，登录还要在密码后面接上 6 位动态码（开启验证码时也可接在验证码后面） |
| `bad_captcha`、`bad_pow`、`bad_nonce`、`expired` | 验证码错、工作量证明没算完、请求过期 | 刷新登录页重试；`expired` 时再检查一下浏览器所在电脑的系统时间 |
| `banned` | 来源 IP 或用户名被暂时封禁（连续失败达到 `login_max_fail_times`，默认 10 次） | 等待一段时间（`login_ip_ban_time` 默认 180 秒，`login_user_ban_time` 默认 3600 秒），或用另一个 IP 登录后到“封禁管理”页解除 |
| `acl` | 来源被 `login_acl_mode`、`login_acl_rules` 拒绝，页面返回 403 | 检查[登录来源名单](/guide/server/access-control.md)，把自己的 IP 加进白名单，或改 `login_acl_mode=0`，重载即可 |
| `malformed`、`session_failed`、`error` | 请求格式不对，或保存登录状态失败 | 清除浏览器该站点的 Cookie 后重试；`session_failed` 同时检查 `instance.key` 能否读写，见[运维](/guide/server/operations.md?id=实例主密钥) |

管理界面放在反向代理后面时：

- 打开页面就是 502 或登录后立即掉线：检查代理的响应头缓冲、`web_base_url` 与代理路径是否一致，见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。
- 所有人的来源都变成代理的 IP：需要设置 `allow_x_real_ip=true`，并把代理的地址写进 `trusted_proxy_ips`，否则登录封禁和名单按代理的地址计算。

## 客户端在线但隧道不通

按顺序排查，每一步都能在日志或网页上确认：

1. **客户端真的在线吗**：网页“客户端”页里该客户端应显示在线，nps 日志里有 `client_online`。
2. **隧道已启用并在监听吗**：隧道应是“已启用”，日志里有 `tunnel_started`；如果是 `listen_failed`，是隧道端口被占用，见下文“端口冲突”。
3. **访问的是 nps 服务器的端口**：隧道端口开在 nps 所在的机器上，不是 npc 所在的机器。防火墙、云安全组要放行这个端口（TCP 隧道放行 TCP，UDP 隧道放行 UDP）；Docker 部署的 nps 不用 `--net=host` 时，要映射这些端口。
4. **目标地址对吗**：目标是**从 npc 所在的机器**访问的地址。目标写 `127.0.0.1:80` 指的是 npc 那台机器自己的 80 端口。
5. **看 `relay_failed`**：nps 日志里的这个事件带 `route`（哪条隧道）、`target`（目标）和 `reason`：

| `reason` | 说明 |
| --- | --- |
| `refused` | npc 连不上目标：内网服务没启动、端口写错，或服务只监听了别的网卡 |
| `timeout` | 目标不可达或太慢：检查 npc 机器到目标的网络、防火墙 |
| `dns` | 目标里的域名解析失败 |
| `client_offline` | 客户端当时不在线 |
| `no_target` | 隧道没有可用的目标 |

6. **看是不是被策略拒绝了**：`tunnel_denied` 的 `reason` 为 `acl`（命中黑白名单）、`dest_acl`（目标不在允许范围）、`not_registered`（要求先登记 IP，见[访问控制](/guide/server/access-control.md)）、`http_disabled` 或 `socks5_disabled`（混合代理没有开启对应协议）、`auth`（需要认证而没有通过）、`client_offline`。
7. **看是不是触发了限制**：`limit_exceeded` 的 `kind` 为 `flow`（流量用完）、`conn`（连接数上限）、`rate`（限速）、`expire`（已过期）；`quota_reached` 表示额度刚用尽。到网页对应的客户端、隧道或域名条目、以及“全局参数”页检查限制。
8. **看健康检查**：`target_down` 表示健康检查把某个目标标成下线，流量不再分给它；`target_up` 表示恢复。见[健康检查](/guide/tunnels/health-check.md)。

**域名转发**打不开时再多查几项：域名的 DNS 要解析到 nps 服务器；HTTP 端口 `http_proxy_port`（发布包默认 80）、HTTPS 端口 `https_proxy_port`（默认 443）没被别的程序占用；域名条目的“域名”要和浏览器里访问的完全一致。把 `http_access` 的日志级别调到 `debug` 可以看到每个请求的域名、状态码和目标，或者开 `access_log` 写成单独的文件。

**点对点访问**连不上时看 `p2p_failed`。两端都是对称型 NAT 时通常无法直连，`conn_mode=auto` 会继续经 nps 中转（`via=relay`）。两个 npc 都需要 0.35 及以上，见[点对点访问](/guide/tunnels/secret.md)。

## 证书失败

先看 nps 日志里的 `cert_failed`（带 `domain`、`reason`、`remaining_s`）和 `cert_loaded`（带 `until`，是当前使用的证书的到期时间）。证书剩余不足 7 天仍然失败时，`cert_failed` 的级别是 `error`。

| `reason` | 说明 | 怎么处理 |
| --- | --- | --- |
| `challenge_failed` | 证书机构验证域名失败 | 确认域名已解析到这台 nps 服务器，并且 80 或 443 端口能从公网访问（不能被防火墙、云安全组、前置代理挡住，也不能被别的程序占用） |
| `dns_failed` | 解析域名失败 | 检查域名的 DNS 记录，以及服务器自己的 DNS 是否可用 |
| `rate_limited` | 触发证书机构的申请频率限制 | 等待一段时间再试，不要反复重启 |
| `account_failed` | 注册申请账号失败 | 检查 `ssl_email` 是否是真实邮箱；使用 `ZeroSSL` 时还要设置 `ssl_zerossl_api` |
| `issue_failed` | 签发过程失败 | 看同一行的 `err`；确认 `ssl_ca` 是 `LetsEncrypt`、`ZeroSSL`、`GoogleTrust` 之一 |
| `load_failed` | 已有的证书文件读不出来 | 检查文件路径、权限、格式（PEM），以及证书和私钥是否成对；Docker 部署的文件请用硬链接，不要用软链接 |

其他情况：

- 浏览器提示证书不匹配：域名条目没配证书时，会用默认证书（`https_default_cert_file`），但默认证书只用于被它覆盖的域名；对其他域名需要给条目配自己的证书，或开启“自动证书”。
- 过期的证书不再使用，自己更换证书文件后，等 `ssl_cache_reload` 设置的周期内会自动加载，不用重启。
- 前面有反向代理时，由代理处理证书，nps 这边不要再做 HTTPS 跳转，见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。

## 端口冲突

nps 启动或新增隧道时端口被占用，日志里是 `listen_failed`（级别 `error`），`reason=in_use` 表示被占用，`reason=permission` 表示没有权限，`route` 和 `listen` 说明是哪个服务的哪个地址。

发布包自带配置占用的端口见[快速开始](/getting-started/quick-start.md)第 1 步。

找出是谁占用了端口：

```bash
sudo ss -lntup | grep ':8024'      # Linux
sudo lsof -nP -i :8024             # macOS、Linux
```

```powershell
netstat -ano | findstr :8024       # Windows，最后一列是进程号
```

处理办法：

- 占用方是不需要的程序：停掉它。
- 必须共存（例如 80、443 要留给 Nginx）：改 nps 的端口，见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)，端口设为 `0` 就是不启动该服务。
- 没有权限：1024 以下的端口需要 root（或授予程序绑定低位端口的能力）；用 `nps install` 装成系统服务时一般已经满足。
- 单个服务监听失败不会让整个 nps 退出：HTTP、HTTPS、HTTP/3 任一监听失败时，只停止这一组监听，其他功能继续；配置文件发布的隧道遇到端口冲突，只让那一条隧道失败，不会断开客户端。看到 `listen_failed` 后确认 nps 的其他功能是否正常。
- 配置了 `allow_ports` 的，隧道端口必须在允许范围内。

## 其他常见问题

**修改了配置没有效果**：确认改的是正在使用的那个文件（Linux 安装后是 `/etc/nps/conf/nps.conf`，nps 启动时的 `started` 事件的 `file` 字段会写出来）；确认这项设置属于“重载就生效”还是“需要重启”，见[运维](/guide/server/operations.md?id=各项设置重载后的效果)。重载的结果会写成 `config_applied` 事件，`restart` 字段列出了还需要重启的键；写错了会是 `config_failed`，此时原配置保持不变。

**nps 启动不了**：在终端里直接运行 `nps`，错误会打印出来。常见原因是配置文件语法错误（输出 `load config file error`）、端口冲突，或者 `instance.key` 已损坏——这个文件读不出来、格式或校验和错误时 nps 会拒绝启动，也不会自动覆盖；删除它会生成新的，但所有网页登录状态会失效。

**重启后流量统计少了一点**：流量按 `flow_store_interval` 设置的周期保存，异常退出时周期内新增的流量会丢失。

**连接数很多时出问题**：Linux 默认的连接数限制偏低，可以按机器性能调大 `tcp_max_syn_backlog`、`somaxconn` 等内核参数。使用 QUIC 时日志里出现 UDP 缓冲区警告，可以增大系统的 UDP 缓冲区：

```bash
echo -e "\nnet.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

**获取访问者的真实 IP**：域名转发时设置 `http_add_origin_header=true`，后端读取 `X-Forwarded-For` 或 `X-Real-IP`；后端支持时也可以开启 Proxy Protocol，见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。

**Docker 里 `docker logs` 没有日志**：容器里的 nps 是以服务方式运行的，`log=stdout` 会改写到文件。把 `log` 设为 `both`，或设 `log_path=off`，见[日志](/guide/server/logging.md)。

**需要提交问题**：到 [GitHub Issues](https://github.com/djylb/nps/issues) 说明版本（`nps -version`、`npc -version`）、系统、相关日志和你已做过的排查。
