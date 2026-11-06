# NPC 命令行参数

```bash
./npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off -debug=false
```

## 命令

`install`、`uninstall`、`start`、`stop`、`restart`、`status`、`register`、`update`、`update-force`。

## 参数

| 参数 | 说明 | 默认 |
| --- | --- | --- |
| `-server`, `-s` | 服务器地址 `addr:port[@host_or_sni[:port]][/ws_path_or_quic_alpn]`，逗号分隔多个 | 无 |
| `-vkey`, `-k` | 认证密钥，逗号分隔 | 无 |
| `-type`, `-t` | `tcp`、`tls`、`kcp`、`quic`、`ws`、`wss`，逗号分隔 | `tcp` |
| `-config`, `-c` | 配置文件路径，逗号分隔 | 无 |
| `--launch` | [快速启动](/guide/client/launch.md)内容，可重复 | 无 |
| `-proxy` | 经代理连接：http、https、socks5、socks5h、socks4、socks4a | 无 |
| `-local_ip` | 出站绑定的本地 IP，逗号分隔，与 `-server` 对应 | 无 |
| `-local_ip_forward` | `local_ip` 同时作用于隧道转发出口（仅公网 IP 与域名目标） | `false` |
| `-local_type` | 访问模式：`p2p`、`p2ps`、`p2pt`、`secret` | `p2p` |
| `-local_port` | 访问端本地端口 | `2000` |
| `-password` | secret/P2P 隧道的唯一密钥 | 无 |
| `-target` | secret/P2P 访问的目标，如 `10.2.50.2:22` | 无 |
| `-target_type` | `all`、`tcp`、`udp` | `all` |
| `-local_proxy` | Secret 连到服务端本地 | `false` |
| `-fallback_secret` | P2P 不可用时回退到私密代理 | `true` |
| `-disable_p2p` | 禁用 P2P | `false` |
| `-p2p_timeout` | P2P 超时（秒） | `5` |
| `-disable_remote_update` | 忽略服务端下发的远程更新 | `false` |
| `-auto_reconnect` | 断线自动重连 | `true` |
| `-disconnect_timeout` | 连接超时（秒） | `30` |
| `-keepalive` | 保活周期（秒），`0` 用默认 | `0` |
| `-proto_version` | 协议版本 `0-8`，见[兼容范围](/getting-started/versioning.md) | `8` |
| `-skip_verify` | 跳过服务器证书校验 | `false` |
| `-tls_enable` | 已弃用，等同 `-type=tls` | `false` |
| `-debug` | 调试模式：日志输出到控制台，级别 `trace` | `true` |
| `-log` | `stdout`、`file`、`both`、`off`；`-debug` 开启时 `file` 改为 `stdout` | `file` |
| `-log_path` | 日志路径，空为默认，`off` 禁用 | 系统默认 |
| `-log_level` | `trace`、`debug`、`info`、`warn`、`error`、`fatal`、`panic`、`off` | `trace` |
| `-log_compress` | 压缩日志 | `false` |
| `-log_max_days` | 保留天数，`0` 关闭 | `7` |
| `-log_max_files` | 文件数，`0` 关闭 | `10` |
| `-log_max_size` | 单文件 MB，`0` 关闭 | `5` |
| `-log_color` | 控制台彩色 | `true` |
| `-pprof` | PProf 地址 `ip:port` | 无 |
| `-dns_server` | 优先 DNS，不可达时回退到系统 DNS | `8.8.8.8` |
| `-ntp_server` | NTP 服务器 | 无 |
| `-ntp_interval` | NTP 最小查询间隔（分钟） | `5` |
| `-timezone` | 时区，如 `Asia/Shanghai` | 无 |
| `-time` | `register` 的有效小时数 | `2` |
| `-gen2fa` | 生成 TOTP 密钥 | `false` |
| `-get2fa` | 根据密钥输出当前验证码 | 无 |
| `-version`, `-v` | 显示版本 | |

环境变量：`NPC_SERVER_ADDR`、`NPC_SERVER_VKEY`、`NPC_CONFIG_PATH`、`NPC_LOCAL_IP`、`NPC_LAUNCH`。
