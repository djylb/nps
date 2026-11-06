# 混合代理

同一端口提供 HTTP 和 SOCKS5 代理，可分别开关。

**适用范围：** 在外网环境下像使用 VPN 一样访问内网设备或资源。

**场景：** 把 `1.1.1.1:8003` 作为代理。

1. 在客户端的隧道管理中添加混合代理，监听端口 `8003`，保存。
2. 在本机配置代理（例如用 Proxifier 全局代理），地址 `1.1.1.1`，端口 `8003`。

**注意：**

- 经过 SOCKS5 时，收到数据包前 Socket 已处于 Accept 状态，表现为扫描端口全部 Open、建立连接后短时间关闭。若想与内网表现一致，建议远程连接一台设备。
- 为保证内网安全，建议配置用户名密码认证。仅配置用户名时也开启认证。来源名单在握手和认证前检查。

## 目标黑白名单

混合代理（HTTP/SOCKS5）支持按目标地址配置黑名单或白名单，对 HTTP 请求、HTTP CONNECT、SOCKS5 CONNECT 和 SOCKS5 UDP 的每个数据报都生效，被拒绝的 UDP 数据报直接丢弃。每行一条规则：

| 写法 | 含义 |
|---|---|
| `full:example.com` | 精确匹配该域名 |
| `*.example.com` | 只匹配子域名 |
| `*example.com`、`domain:example.com` | 匹配该域名及其子域名 |
| `keyword:google`、`google`（不带前缀） | 域名包含该文本 |
| `regexp:^ads\.` | 正则匹配域名 |
| `geosite:cn`、`geosite:google@ads` | Geo 数据中的域名分类 |
| `1.2.3.4`、`10.0.0.0/8`、`geoip:cn` | IP、网段、Geo 数据中的 IP 分类 |

保存时会校验规则并统一成规范写法，例如 `*example.com` 存成 `domain:example.com`、不带前缀的 `example.com` 存成 `keyword:example.com`。旧版本中不带前缀的域名表示精确匹配，现在按关键字匹配，需要精确匹配时请改用 `full:`。

启用目标名单后 SOCKS5 UDP 仍然可用，按数据报检查。
