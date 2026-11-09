# 站点与 HTTP

这些功能用在“域名转发”上（个别也用于 TCP、UDP 隧道）。字段在网页里的位置见[管理页面说明](/reference/web-ui.md)，操作步骤见[域名转发](/guide/tunnels/domain-forwarding.md)。

## 自动证书

开启后，nps 为这个域名自动申请并续签 HTTPS 证书。要求 nps 监听 80 或 443 端口，并且这两个端口能从公网访问。证书颁发机构、邮箱和缓存位置用 `ssl_ca`、`ssl_email`、`ssl_path` 设置；`force_auto_ssl=true` 时对所有域名强制申请，见[服务端配置](/reference/server-config.md)。证书和 HTTPS 的整体用法见[HTTPS 与反向代理](/guide/server/https-and-proxy.md)。

## 自动 HTTPS (301)

开启后，浏览器用 http 访问会被 301 跳转到 https。

## 自动修复 CORS

开启后，把当前请求的 `Origin` 写进响应头，避免浏览器报跨域错误。后端已经返回 `Access-Control-Allow-Origin` 时不会覆盖。这类问题最好由后端自己解决。

## TLS 直通（由后端解密）

按首个 TLS 握手里的域名（SNI）分流，nps 不解密，数据原样交给后端，由后端完成握手和证书校验。

## TLS 终止（解密转发）

按 SNI 分流，在 nps 完成 TLS 握手并用这里配置的证书，再把解密后的明文原样转给后端。只做解密传输，不做应用层处理。

## Proxy Protocol

向后端传递访问者真实 IP 的协议，后端要支持。TCP 隧道、UDP 隧道和域名转发都可以设置：关闭、V1（字符串格式）、V2（二进制格式）。

## 修改 Host

内网站点需要的 `Host` 可能和公网域名不同。在“请求域名信息修改”里填写，nps 会把发往后端的请求的 `Host` 改成这个值。

## 请求头与响应头修改

“请求头部信息修改”和“返回头部信息修改”里，一行写一条 `名称: 值`，值里可以使用变量，写法是 `${变量名}`：

```
X-Original-URL: ${scheme_host}${request_uri}
X-Client-IP: ${remote_ip}
```

名称写 `名称: ${unset}` 表示删除该头。变量名写错或不认识时，那一行会被忽略并在日志里提示。

下面的变量在请求头、响应头和重定向地址里都能用：

| 变量 | 含义 |
| --- | --- |
| `${scheme}` | `http` 或 `https` |
| `${ssl}`、`${forwarded_ssl}` | 请求走 TLS 为 `on`，否则 `off` |
| `${https}` | 走 TLS 为 `on`，否则为空 |
| `${host}` | 不带端口的小写主机名 |
| `${http_host}`、`${request_host}` | 原始 `Host` 头 |
| `${scheme_host}` | 协议加主机，如 `https://www.example.com` |
| `${request_method}` | 请求方法 |
| `${request_uri}` | 原始请求路径加查询串 |
| `${uri}`、`${request_path}` | 解码后的路径，不含查询串 |
| `${args}`、`${query_string}` | 查询串，不含 `?` |
| `${is_args}` | 有查询串时为 `?` |
| `${remote_addr}` | 访问者地址，带端口 |
| `${remote_ip}`、`${remote_port}` | 访问者 IP、端口 |
| `${peer_addr}`、`${peer_port}` | 直接连到 nps 的对端地址、端口 |
| `${server_addr}` | 请求进来的本地地址 |
| `${server_port}`、`${server_port_http}`、`${server_port_https}`、`${server_port_http3}` | 当前、HTTP、HTTPS、HTTP/3 监听端口 |
| `${forwarded_proto}`、`${forwarded_host}` | 访问者看到的协议、主机 |
| `${proxy_add_x_forwarded_for}` | 原有的 `X-Forwarded-For` 加上当前对端地址 |
| `${proxy_add_forwarded}` | 原有的 `Forwarded` 加上本跳 |
| `${http_头名}` | 请求头的值，`-` 写成 `_`，如 `${http_user_agent}`、`${http_upgrade}`、`${http_range}` |
| `${cookie_名}`、`${arg_名}` | 某个 Cookie、某个查询参数的值 |
| `${origin}`、`${user_agent}` | 请求头 `Origin`、`User-Agent` |
| `${date}`、`${timestamp}`、`${timestamp_ms}`、`${msec}`、`${time_iso8601}`、`${time_http}` | 当前时间的各种写法 |

只在响应头里有值的变量（请求头和重定向里为空）：

| 变量 | 含义 |
| --- | --- |
| `${status}` | 后端响应状态行，如 `200 OK` |
| `${status_code}` | 状态码，如 `200` |
| `${content_length}` | 响应体长度，未知为 `-1` |
| `${content_type}`、`${via}` | 响应头 `Content-Type`、`Via` 的值 |
| `${upstream_http_头名}` | 后端响应头的值 |

## 自定义重定向地址

“重定向地址”填写后，匹配的请求会得到 307 临时重定向。地址里可以使用上面的变量：

```
https://www.example.com${request_uri}
```

## URL 路由与 URL 重写

“URL 路由”让一个域名下不同路径转发到不同目标：填写路径前缀，只有以它开头的请求走这条规则，留空则不限。同一个域名可以建多条规则，路由更长的优先匹配。

“URL 重写”把请求路径里匹配到的 URL 路由前缀替换成填写的内容；nps 会自动加上 `X-Original-Path` 请求头，告诉后端浏览器请求的原始路径。例如：

| URL 路由 | URL 重写 | 请求 | 到达后端 |
| --- | --- | --- | --- |
| `/path/` | `/` | `/path/index.html` | `/index.html` |
| `/xml` | `/path/list.xml` | `/xml` | `/path/list.xml` |
| `/ws` | `/websocket` | `/ws` | `/websocket` |

## 泛域名

域名写成 `*.proxy.com`，`a.proxy.com`、`b.proxy.com` 等都会转发到同一目标。

## 站点保护

给域名转发设置“密码认证”（一行一个 `用户名=密码`），访问者要用 HTTP Basic 认证输入正确的账号密码才能访问。适合保护开发、测试环境。

## 负载均衡

域名转发和 TCP 隧道的“目标”分行填写多个地址即可。按加权轮询分配连接，同一个地址写几次，权重就是几。配合[健康检查](/guide/tunnels/health-check.md)可以自动摘除故障目标；nps 自己拨号连续失败的目标也会被暂时剔除，一段时间后重新尝试。

## 自定义错误页

域名转发返回的错误页可以替换：`error_page` 指定错误页文件（默认 `web/static/page/error.html`），`error_page_time_limit`、`error_page_flow_limit` 可分别指定到期和超流量时的页面。错误页是单个 HTML 文件，不能另带图片等静态文件。
