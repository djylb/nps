# FAQ

## 服务端

**服务端无法启动**：示例配置占用 8024（TCP 和 UDP）、8081、80、443 和 UDP 6000-6002，端口冲突时请修改。单个 HTTP/HTTPS 监听失败不会让 nps 退出，请看日志。

**修改配置无效**：Linux 安装后配置在 `/etc/nps/conf/nps.conf`，请修改该文件。

**IPv6**：nps 默认在 IPv4/IPv6 双栈上监听，无需额外配置。

**指定配置路径**：`-conf_path`，服务安装时也要带上：`./nps install -conf_path=/app/nps`，然后 `nps start`。

**客户端连接端口**：`mix_port` 不为 `0` 时监听，接受 `tls`、`wss`、`quic`。客户端 `npc -launch="npc://vkey@xxx:8024?type=tls"`。各协议单独端口 `bridge_tls_port` 等已弃用，旧配置仍然读取。

## 客户端

**无法连接服务端**：

1. 防火墙、云安全组是否开放端口。
2. vkey 是否匹配。
3. 版本是否在[兼容范围](/getting-started/versioning.md)内。

**Windows 7 无法启动，或前台能连接但服务离线**：使用 `old` 包，补齐 SP1、DLL 加载接口及 SHA-2 相关更新，并检查本地计算机根证书、系统时间和服务日志。`RUNNING` 只表示服务进程已启动。详见 [Windows 7](/getting-started/install-windows.md?id=windows7)。

**隧道端口连不上**：隧道端口由 nps 服务端开放，不是 npc。nps 用 Docker 且未使用 `--net=host` 时要映射这些端口。

**点对点访问无法直连**：两端都是随机端口的对称型 NAT 时通常无法直连，`conn_mode=auto` 下继续经 nps 中转，见[点对点访问](/guide/tunnels/secret.md)。

**一个进程连接多个服务器**：`npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls`；可以省略填写，如 `npc -server=xxx:123 -vkey=key1,key2,key3`。

## 反向代理与 HTTPS

**NPS 代理时如何保留真实 IP**：设置 `http_add_origin_header=true`，后端读取 `X-Forwarded-For` 或 `X-Real-IP`；后端支持时也可启用 Proxy Protocol。

**NPS 在 Nginx/Caddy 后面**：见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。前置代理负责证书，传 `X-NPS-Http-Only` 避免 301。

**域名转发的 HTTPS 逻辑**：访问 nps 的模式不等于后端模式。目标填后端 HTTP 端口即可；后端仅支持 HTTPS 时目标类型选 HTTPS，也可以启用仅转发。

**Auto CORS**：可自动插入 CORS 头，但建议在后端实现更细粒度的控制。

## 其他

**日志在哪里**：见 `nps.conf` 的 `log`、`log_path`。默认路径 Windows 为程序目录的 `nps.log`，Linux/macOS 为 `/var/log/nps.log`。

**到期时间**：设置 `allow_time_limit=true`，在 Web 上填写，如 `2025-01-01` 或 `2025-01-01 00:00:00 +0800 CST`。

**修改 `nps.conf` 不想重启**：发送 `SIGUSR1`，见[重载配置](/guide/server/operations.md)。
