# 域名转发

## 这能解决什么

用域名访问内网的网站。多个域名可以共用服务器的 80、443 端口，nps 按访问的域名把请求交给不同的内网站点，作用类似反向代理（例如 Nginx）。它不是 DNS 服务器，域名的解析要在域名服务商那里自己设置。

示例：域名 `www.example.com` 要访问内网 `192.168.1.10:80` 上的网站。

## 操作步骤

1. 在域名服务商处，把 `www.example.com` 解析到 `203.0.113.10`。
2. 左侧菜单点“域名转发”，点“新增”。
3. 填写：
   - 客户端 ID：内网机器上的客户端；
   - 域名：`www.example.com`；
   - 目标 (IP:端口)：`192.168.1.10:80`。
4. 点“新增”。

服务器需要放行 HTTP、HTTPS 端口，默认配置是 `80`、`443`（`http_proxy_port`、`https_proxy_port`，见[服务端配置](/reference/server-config.md)）。要给更多域名，重复第 2 到 4 步，每个域名一条。

## 怎么验证

```bash
curl -I http://www.example.com
```

返回内网网站的响应头即可。域名还没生效时，可以用 `curl -I -H "Host: www.example.com" http://203.0.113.10` 先绕过解析验证。

## 常见选项

- **模式**（所有、HTTP、HTTPS）：指访问者用哪种方式访问 nps，不是后端的协议。选“所有”则两种都接收。
- **目标类型 (HTTP/HTTPS)**：后端本身只提供 HTTPS 时选 HTTPS，目标填后端的 HTTPS 端口。
- **多个目标**：目标按行填写多个，可以做负载均衡，并配合[健康检查](/guide/tunnels/health-check.md)。
- **URL 路由**：只转发匹配某个路径前缀的请求，同一个域名可以按路径分给不同的后端。
- **HTTPS 与证书**：“自动HTTPS (301)”、“自动证书”、“HTTPS 证书”“HTTPS 密钥”，以及“TLS 直通（由后端解密）”“TLS 终止（解密转发）”，用法见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。
- **路由与请求响应**：“URL 重写”“重定向地址”“请求域名信息修改”“请求头部信息修改”“返回头部信息修改”“自动修复 CORS”“兼容模式”，说明见[站点与 HTTP](/reference/features-http.md)。
- **账号认证**：“密码认证”每行 `用户名=密码`，访问网站时要先输入，留空不启用。
- **访问控制与限制**：IP 黑白名单、“最大连接数”（HTTP 按并发请求计数，HTTPS 透传和 TLS 终止按连接计数）、限速、流量、时间限制，与 [TCP 隧道](/guide/tunnels/tcp.md)的同名选项一致。
- **同一个域名由多台内网机器发布**：见[客户端启动链接与多条连接](/guide/client/launch.md)。

## 相关页面

- [HTTPS 与反向代理](/guide/server/https-and-proxy.md)
- [站点与 HTTP](/reference/features-http.md)
- [健康检查](/guide/tunnels/health-check.md)
- [虚拟后端](/guide/tunnels/virtual-backend.md)：把 npc 本机提供的网页或文件服务当作目标
