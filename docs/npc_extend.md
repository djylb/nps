# 增强功能

## 1. NAT 类型检测

NAT 类型由 NPS 服务端的 NAT 观测端在每次 P2P 打洞时测量（多端口观测并校验回程地址），访问端与提供端都会在日志中输出本端的测量结果，例如：

```
P2P local port 2000: punched public path ...; local NAT udp4 mapping=eim filtering=pdf ports=preserving
```

打洞失败时日志给出失败原因与下一次尝试的时间。两端都是随机端口分配的对称型 NAT 时通常无法直连，开启 `fallback_secret` 后会自动走私密代理中转，并在后台以低速率继续尝试直连。

---

## 2. 状态检查

检查 NPC 客户端的运行状态：
```bash
./npc status -config=/path/to/npc.conf
```
📌 **可选参数**

| 参数        | 说明            |
|-----------|---------------|
| `-config` | 指定 NPC 配置文件路径 |

---

## 3. 重载配置文件

重新加载 NPC 客户端配置，而无需重启进程：
```bash
./npc restart -config=/path/to/npc.conf
```
📌 **可选参数**

| 参数        | 说明            |
|-----------|---------------|
| `-config` | 指定 NPC 配置文件路径 |

---

## 4. 通过代理连接 NPS

如果 NPC 运行的机器无法直接访问外网，可以通过 **Socks5 / Socks4 / HTTP(S) 代理** 连接 NPS 服务器。

### **4.1 配置文件方式**
在 `npc.conf` 文件中添加：
```ini
[common]
proxy_url=socks5://111:222@127.0.0.1:8024
```

### **4.2 命令行方式**
```bash
./npc -server=xxx:123 -vkey=xxx -proxy=socks5://111:222@127.0.0.1:8024
```

📌 **支持代理协议**

| 代理类型       | 示例格式                                 |
|------------|--------------------------------------|
| **Socks5** | `socks5://username:password@ip:port`（`socks5h://` 同义，均由代理解析域名） |
| **Socks4** | `socks4://userid@ip:port`（本地解析域名）、`socks4a://userid@ip:port`（代理解析域名） |
| **HTTP**   | `http://username:password@ip:port`；省略协议的 `ip:port` 视为 HTTP 代理 |
| **HTTPS**  | `https://username:password@host:port`（与代理之间使用 TLS，并校验代理证书） |

未配置 `proxy_url` 时，NPC 读取环境变量 `ALL_PROXY`（或 `all_proxy`），并按 `NO_PROXY` 跳过匹配的服务器地址。

---

## 5. 其他命令行参数
📌 **所有参数可与启动命令组合使用** ：

```bash
./npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off -debug=false
```

| 参数                    | 说明                                                                    | 默认值       |
|-----------------------|-----------------------------------------------------------------------|-----------|
| `-server`             | 指定 NPS 服务器地址（`addr:port[@host_or_sni[:port]][/ws_path_or_quic_alpn]`） | 无         |
| `-vkey`               | 客户端认证密钥                                                               | 无         |
| `-type`               | 服务器连接方式（`tcp` / `tls` / `kcp` / `quic` / `ws` / `wss`）                | `tcp`     |
| `-config`             | 指定配置文件路径                                                              | 无         |
| `-proxy`              | 通过代理连接 NPS（支持 Socks5 / HTTP）                                          | 无         |
| `-local_ip`           | 指定客户端出站绑定的本地 IP（可逗号分隔，和 `-server` 一一对应）                               | 无         |
| `-local_ip_forward`   | 是否让 `local_ip` 同时作用于隧道转发出口（仅对公网 IP 与域名生效，私网 IP 忽略）                    | `false`   |
| `-debug`              | 是否启用调试模式                                                              | `true`    |
| `-log`                | 日志输出模式（`stdout` / `file` / `both` / `off`）                            | `file`    |
| `-log_path`           | NPC 日志路径（为空使用默认路径，`off` 禁用日志）                                         | `npc.log` |
| `-log_level`          | 日志级别（trace、debug、info、warn、error、fatal、panic、off）                     | `trace`   |
| `-log_compress`       | 是否启用日志压缩                                                              | `false`   |
| `-log_max_days`       | 日志最大保留天数（0 关闭）                                                        | `7`       |
| `-log_max_files`      | 最大日志文件数（0 关闭）                                                         | `10`      |
| `-log_max_size`       | 单个日志文件最大大小（MB）                                                        | `5`       |
| `-log_color`          | 控制台输出启用 ANSI 彩色                                                       | `true`    |
| `-auto_reconnect`     | 断线后自动重连                                                               | `true`    |
| `-disconnect_timeout` | 连接超时秒数                                                                | `30`      |
| `-keepalive`          | 保活（KeepAlive）周期（秒）                                                    | 默认        |
| `-pprof`              | 启用 PProf 调试（格式 `ip:port`）                                             | 无         |
| `-local_type`         | P2P 目标类型                                                              | `p2p`     |
| `-local_port`         | P2P 本地端口                                                              | `2000`    |
| `-password`           | P2P 认证密码                                                              | 无         |
| `-target`             | P2P 目标（如目标客户端 ID/标识）                                                  | 无         |
| `-target_type`        | P2P 目标连接类型（`all` / `tcp` / `udp`）                                     | `all`     |
| `-p2p_timeout`        | P2P 超时时间（秒）                                                           | `5`       |
| `-disable_p2p`        | 禁用 P2P 连接                                                             | `false`   |
| `-disable_remote_update` | 忽略服务端下发的远程更新指令                                              | `false`   |
| `-fallback_secret`    | 当 P2P 不可用时回退到 Secret 直连模式                                             | `true`    |
| `-dns_server`         | 优先使用的 DNS 服务器，不可达时回退到系统 DNS                                 | `8.8.8.8` |
| `-ntp_server`         | 配置 NTP 服务器                                                            | 无         |
| `-ntp_interval`       | NTP 最小查询间隔（分钟）                                                        | `5`       |
| `-timezone`           | 配置时区（Asia/Shanghai）                                                   | 无         |
| `-time`               | 客户端注册时间（小时）                                                           | `2`       |
| `-gen2fa`             | 生成 TOTP 双因素认证密钥                                                       | `false`   |
| `-get2fa`             | 根据提供的密钥输出一次性 TOTP 验证码                                                 | 无         |
| `-version`            | 显示当前版本                                                                | 无         |

下面只补充**文档中缺失**的参数说明（基于 `./npc -h`），其余已存在的不重复列出：

---

## 6. 快速启动（`--launch`）

`--launch` 用一段内容启动 npc，不需要配置文件。可以重复传入，每个输入独立运行；也可以用环境变量 `NPC_LAUNCH` 传一个。设置后 `-server`、`-vkey`、`-config` 等连接参数被忽略，日志、`-skip_verify`、`-proto_version` 等进程级设置仍只由命令行决定。

| 形式 | 示例 |
|------|------|
| npc URL | `npc://vkey@1.2.3.4:8024?type=tls` |
| 多服务器 | `npc://?server=a:8024,b:8024&vkey=k1,k2&type=tls,tcp` |
| 本地 P2P/Secret | `npc://vkey@1.2.3.4:8024?password=pw&target=127.0.0.1:22&local_port=2000` |
| 远程内容 | `https://example.com/npc.yaml` |
| 本地文件 | `@/etc/npc/launch.json` |
| Base64 | 以下任一内容的 Base64（标准或 URL 安全） |
| JSON / YAML / INI | 见下 |

npc URL 还支持 `conn_type`、`proxy`、`local_ip`、`local_type`、`target_type`、`fallback_secret`、`local_proxy`、`name` 参数，未知参数会报错。

JSON 与 YAML 结构相同，顶层是一个配置、配置列表，或 `{"profiles": [...]}`：

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

各节的键名与[配置文件](example.md)完全相同；INI 内容就是配置文件本身，并按配置文件模式运行。`remark` 不能以 `secret`、`p2p` 开头。

**运行与回退：** 同一输入的多个配置一起运行，任一配置断开时整组重启（5 秒后），并重新读取来源。远程来源获取或解析失败时继续使用内存中最后一份有效内容，并至少隔 30 秒再请求；首次启动没有可用内容时每 30 秒重试。来源返回 `410 Gone` 表示已撤销：停止运行，5 分钟后重试。最后一份有效内容不写入磁盘，npc 重启后需要来源可用。非远程内容无效时直接退出。

**安全：** 来源可以决定 npc 连接哪个服务器、开放哪些隧道，能让对方访问本机可达的服务，只使用你信任的来源，并尽量使用 https（http 会告警，https 不允许跳转到 http）。远程内容限制 1 MiB，不执行命令，也不能使用模板、引用其他 URL 或本地文件，不能设置 `multi_account`、`cert_file`、`key_file`、`local_path`、`strip_pre`、`pprof_addr` 或 `mode=file`。需要这些功能时使用本地配置文件。

---

## 7. 群晖支持

📌 **推荐使用 Docker 部署**
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off
```
~~曾提供 `.spk` 群晖套件，但已不再维护，建议使用 Docker 方式运行。~~ 
✅[Telegram](https://t.me/npsdev) 内有第三方提供的群晖套件。

---

✅ **如需更多帮助，请查看 [文档](https://github.com/djylb/nps) 或提交 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。**