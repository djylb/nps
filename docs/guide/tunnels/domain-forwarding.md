# 域名转发

**适用范围：** Web 服务、小程序开发、微信公众号开发、产品演示。

域名转发是 HTTP/HTTPS 反向代理（类似 Nginx），不是 DNS 服务器。

**场景：** 有域名 `proxy.com`，内网站点 `127.0.0.1:81`、`127.0.0.1:82`，想用 `a.proxy.com`、`b.proxy.com` 分别访问。

1. 将 `*.proxy.com` 解析到 `1.1.1.1`。
2. 在客户端的域名管理中添加两条规则：`a.proxy.com` → `127.0.0.1:81`，`b.proxy.com` → `127.0.0.1:82`。
3. 通过 `http(s)://a.proxy.com`、`http(s)://b.proxy.com` 访问。

说明：

- **模式**指访问 nps 时使用 HTTP 还是 HTTPS，不是后端模式。目标填写后端 HTTP 端口，nps 自动转发。
- 后端只支持 HTTPS 时，把**目标类型**设为 HTTPS；也可以启用“由后端处理 HTTPS（仅转发）”。
- HTTPS 配置见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。
- 负载均衡、URL 路由、Header 修改、站点保护等见[站点与 HTTP](/reference/features-http.md)。
- 域名支持最大连接数（Web、API、npc.conf 的 `max_conn`），`0` 不限。HTTP 按并发请求计数，HTTPS 透传和 TLS 卸载按连接计数，与客户端总连接数限制同时生效。
- 同一个域名可由多个 npc 发布，见[多实例发布](/guide/client/multi-instance.md)。
