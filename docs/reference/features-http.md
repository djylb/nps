# 站点与 HTTP

## 自动证书
开启后如果NPS监听80或443端口则自动申请SSL证书管理并续签

## 自动HTTPS (301)
开启后如果浏览器使用http请求会自动跳转为https访问

## 自动修复 CORS
当浏览器访问服务存在跨域问题时，开启该功能会将当前请求的 `Origin` 插入到服务器返回头部避免浏览器报 CORS 错误。但该功能最好由后端来实现。
注意该功能当后端返回 `Access-Control-Allow-Origin` 时不会强制覆盖

## TLS 直通（由后端解密）
根据首个 TLS 握手（ClientHello）的 **SNI** 分流，不在前端解密，**数据原样转给后端**，由后端完成 TLS 握手与证书校验。

注意：当后端开启 HTTP/2 且多个站点共享同一证书/IP，浏览器可能把不同域名复用到同一条连接，造成网站显示不正确。

## TLS 终止（解密转发）
根据 **SNI** 分流；在前端完成 TLS 握手与证书，随后把解密后的**明文数据**原样转发给选定后端（仅做解密传输，不做应用层处理）。

## Proxy Protocol
该功能用于 **TCP隧道** 、 **UDP隧道** 和 **域名转发** 向后端传递真实 IP 时使用，需要后端服务支持。

## Host 修改

由于内网站点需要的Host可能与公网域名不一致，域名代理支持host修改功能，即修改Request的Header中的host字段。

**使用方法：在Web管理中设置**

## 自定义重定向地址

支持对请求进行307重定向。

使用示例：
```
https://xxx.com${request_uri}
```

| 占位符                            | 含义                                     |
|--------------------------------|----------------------------------------|
| `${scheme}`                    | 请求协议，`http` 或 `https`                  |
| `${ssl}`                       | TLS 状态，`on`（HTTPS）或 `off`（HTTP）        |
| `${forwarded_ssl}`             | 同 `${ssl}`                             |
| `${host}`                      | 不带端口的主机名（等同 Nginx 的 `$host`）           |
| `${http_host}`                 | 原始 `Host:` 头值（等同 Nginx 的 `$http_host`） |
| `${server_port}`               | 服务监听端口号（等同 Nginx 的 `$server_port`）     |
| `${remote_addr}`               | 客户端真实地址（含端口）                           |
| `${remote_ip}`                 | 客户端真实 IP （IPv6不含方括号）                   |
| `${remote_port}`               | 客户端源端口                                 |
| `${proxy_add_x_forwarded_for}` | 完整的 `X-Forwarded-For` 链（追加了当前客户端 IP）   |
| `${request_uri}`               | 完整请求路径及查询字符串（含 `?` 及后续部分）              |
| `${uri}`                       | 请求路径，不含查询字符串                           |
| `${args}`                      | 查询字符串，不含前导 `?`                         |
| `${query_string}`              | 同 `${args}`                            |
| `${scheme_host}`               | 协议 + 主机（含非标端口），如 `https://example.com` |

## 自定义请求 Header

支持对请求Header进行新增或者修改，以配合服务的需要。

使用示例：
```
X-Original-URL: ${scheme_host}${request_uri}
X-Client-IP: ${remote_ip}
X-Client-Port: ${remote_port}
X-Forwarded-Proto: ${scheme}
X-Forwarded-Ssl: ${ssl}
```

| 占位符                            | 含义                                     |
|--------------------------------|----------------------------------------|
| `${scheme}`                    | 请求协议，`http` 或 `https`                  |
| `${ssl}`                       | TLS 状态，`on`（HTTPS）或 `off`（HTTP）        |
| `${forwarded_ssl}`             | 同 `${ssl}`                             |
| `${host}`                      | 不带端口的主机名（等同 Nginx 的 `$host`）           |
| `${http_host}`                 | 原始 `Host:` 头值（等同 Nginx 的 `$http_host`） |
| `${server_port}`               | 服务监听端口号（等同 Nginx 的 `$server_port`）     |
| `${remote_addr}`               | 客户端真实地址（含端口）                           |
| `${remote_ip}`                 | 客户端真实 IP （IPv6不含方括号）                   |
| `${remote_port}`               | 客户端源端口                                 |
| `${proxy_add_x_forwarded_for}` | 完整的 `X-Forwarded-For` 链（追加了当前客户端 IP）   |
| `${request_uri}`               | 完整请求路径及查询字符串（含 `?` 及后续部分）              |
| `${uri}`                       | 请求路径，不含查询字符串                           |
| `${args}`                      | 查询字符串，不含前导 `?`                         |
| `${query_string}`              | 同 `${args}`                            |
| `${scheme_host}`               | 协议 + 主机（含非标端口），如 `https://example.com` |
| `${http_upgrade}`              | 原始请求的 `Upgrade` 头                      |
| `${http_connection}`           | 原始请求的 `Connection` 头                   |
| `${http_range}`                | 原始请求的 `Range` 头                        |
| `${http_if_range}`             | 原始请求的 `If-Range` 头                     |
| `${unset}`                     | 删除该头                                   |

## 自定义响应 Header

支持对 HTTP 响应头进行新增或修改，以配合服务的需要。

使用示例：
```
Access-Control-Allow-Origin: ${origin}
Access-Control-Allow-Credentials: true
```

| 占位符                    | 含义                                        |
|------------------------|-------------------------------------------|
| `${scheme}`            | 请求协议，值为 `http` 或 `https`                  |
| `${ssl}`               | 是否启用 SSL，值为 `on`（HTTPS）或 `off`（HTTP）      |
| `${server_port}`       | 当前代理监听的端口                                 |
| `${server_port_http}`  | HTTP 监听端口                                 |
| `${server_port_https}` | HTTPS 监听端口                                |
| `${server_port_http3}` | HTTP/3 监听端口                               |
| `${host}`              | 不含端口的原始主机名（类似 Nginx 的 `$host`）            |
| `${http_host}`         | 原始 Host 头部内容（类似 Nginx 的 `$http_host`）     |
| `${remote_addr}`       | 客户端 IP 和端口（例如 `192.168.1.10:52345`）       |
| `${remote_ip}`         | 客户端 IP 地址（不含端口）                           |
| `${remote_port}`       | 客户端源端口                                    |
| `${request_method}`    | 请求方法，例如 `GET`、`POST`                      |
| `${request_host}`      | 请求的 Host                                  |
| `${request_uri}`       | 完整的请求 URI，包含查询字符串，如 `/foo/bar?name=value` |
| `${request_path}`      | 请求路径（不含查询字符串），如 `/foo/bar`                |
| `${uri}`               | 与 `${request_path}` 相同                    |
| `${query_string}`      | 查询字符串（不含 `?`），与 `${args}` 相同              |
| `${args}`              | 同 `${query_string}`                       |
| `${origin}`            | 请求头 `Origin` 的值                           |
| `${user_agent}`        | 请求头 `User-Agent` 的值                       |
| `${http_referer}`      | 请求头 `Referer` 的值                          |
| `${scheme_host}`       | 协议和主机拼接，如 `https://example.com`           |
| `${status}`            | 后端响应状态行，例如 `200 OK`                       |
| `${status_code}`       | 后端响应状态码，例如 `200`                          |
| `${content_length}`    | 响应体长度（字节数）。若未知为 `-1`                      |
| `${content_type}`      | 响应头 `Content-Type` 的值                     |
| `${via}`               | 响应头 `Via` 的值                              |
| `${date}`              | 当前 UTC 时间，格式符合 HTTP Date（RFC 1123）        |
| `${timestamp}`         | 当前时间戳（秒）                                  |
| `${timestamp_ms}`      | 当前时间戳（毫秒）                                 |
| `${unset}`             | 删除该头                                      |

## 404页面配置

支持域名解析模式的自定义404页面，修改 `web/static/page/error.html` 中内容即可（路径可用 `error_page` 修改），暂不支持静态文件等内容

## 域名泛解析

支持域名泛解析，例如将host设置为*.proxy.com，a.proxy.com、b.proxy.com等都将解析到同一目标，在web管理中或客户端配置文件中将host设置为此格式即可。

## URL 重写
填写后自动替换请求路径里 **URL 路由** 的前缀为填写的内容，适用于前后端访问不同路径的情况

NPS 会自动添加 `X-Original-Path` 请求头用于识别浏览器请求的实际地址

例如：
- 当**URL 路由**配置为`/path/`，当**URL 重写**配置为`/`。请求`xx.com/path/index.html`将返回`127.0.0.1:80/index.html`
- 当**URL 路由**配置为`/xml`，当**URL 重写**配置为`/path/list.xml`。请求`xx.com/xml`将下载`127.0.0.1:80/path/list.xml`
- 当**URL 路由**配置为`/ws`，当**URL 重写**配置为`/websocket`。请求`xx.com/ws`将转发到`127.0.0.1:80/websocket`

## 站点保护

域名代理模式所有客户端共用一个http服务端口，在知道域名后任何人都可访问，一些开发或者测试环境需要保密，所以可以设置用户名和密码，nps将通过
Http Basic Auth 来保护，访问时需要输入正确的用户名和密码。

- 在web管理或客户端配置文件中设置

## 负载均衡

域名转发和 TCP 隧道支持负载均衡，在内网目标中分行填写多个目标即可轮询。配合[健康检查](/guide/tunnels/health-check.md)自动摘除故障目标。

带 PROXY 头的域名后端连接不复用，避免后续访问者地址错误。

## URL 路由

见[配置文件模式](/guide/client/config-file.md)，也可以在 Web 上设置。
