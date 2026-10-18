# 扩展功能

## ~~缓存支持~~ （已弃用）

~~对于Web站点来说，一些静态文件往往消耗更大的流量，且在内网穿透中，静态文件还需到客户端获取一次，这将导致更大的流量消耗。nps在域名解析代理中支持对静态文件进行缓存。~~

~~即假设一个站点有a.css，nps将只需从npc客户端读取一次该文件，然后把该文件的内容放在内存中，下一次将不再对npc客户端进行请求而直接返回内存中的对应内容。该功能默认是关闭的，如需开启请在
`nps.conf`中设置`http_cache=true`，并设置`http_cache_length`（缓存文件的个数，消耗内存，不宜过大，0表示不限制个数）~~

该功能将请求内容全部缓存在内存导致消耗过大，同时性能提升不明显，实际使用时缓存命中率不高，同时后端有文件修改也返回不及时，如有需要可使用前置Nginx来实现缓存，故废弃该功能。

## 数据压缩支持

由于是内网穿透，内网客户端与服务端之间的隧道存在大量的数据交换，为节省流量，加快传输速度，由此本程序支持Snappy形式的压缩。

- 所有模式均支持数据压缩
- 在Web管理或客户端配置文件中设置

## 加密传输

如果公司内网防火墙对外网访问进行了流量识别与屏蔽，例如禁止了ssh协议等，通过设置 配置文件，将服务端与客户端之间的通信内容加密传输，将会有效防止流量被拦截。

- nps现在默认每次启动时随机生成tls证书，用于加密传输

> 注意：当底层传输已经使用了安全协议（tls、quic、wss）时，建议关闭该选项

## 站点保护

域名代理模式所有客户端共用一个http服务端口，在知道域名后任何人都可访问，一些开发或者测试环境需要保密，所以可以设置用户名和密码，nps将通过
Http Basic Auth 来保护，访问时需要输入正确的用户名和密码。

- 在web管理或客户端配置文件中设置

## 嵌套转发支持

可在隧道的 **目标** 中配置其他隧道，实现链式转发。

- 转发到隧道：在目标填写 `tunnel://[隧道ID]`（如 `tunnel://11`）
  - 需开启 **“代理到服务器本地”**
  - **支持类型**：TCP 相关隧道（TCP 隧道、混合代理（HTTP/SOCKS5）、文件隧道等）

- 转发到桥接：在目标填写 `bridge://[桥接类型]`（如 `bridge://tcp`）
  - 需**管理员权限**且开启 **“代理到服务器本地”**
  - **支持类型**：tcp|tls|ws|wss|web|ipreg
    - `web`：Web 管理界面；`ipreg`：IP 登记页面（见“仅允许已登记的 IP”），例如在域名转发中把目标设为 `bridge://ipreg`，即可用自己的域名提供登记页面

此外 **文件访问** 隧道还支持下面方式嵌套：
- 转发到文件：在目标填写 `file://[唯一UUID]`（从管理页面文件访问列表获取）
  - **不要** 开启 “代理到服务器本地”
  - 发起嵌套的隧道客户端 **必须与该文件隧道所属客户端相同**

## 混合代理黑/白名单

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

支持域名解析模式的自定义404页面，修改/web/static/page/error.html中内容即可，暂不支持静态文件等内容

## 流量限制

支持客户端级流量限制，当该客户端入口流量与出口流量达到设定的总量后会拒绝服务
，域名代理会返回 404 页面，其他代理会拒绝连接,使用该功能需要在`nps.conf`中设置`allow_flow_limit`，默认是关闭的。

## 带宽限制

支持客户端级带宽限制，带宽计算方式为入口和出口总和，权重均衡,使用该功能需要在`nps.conf`中设置`allow_rate_limit`，默认是关闭的。

## 时间限制

支持客户端级到期日期限制，到期后会拒绝连接，使用该功能需要在`nps.conf`中设置`allow_time_limit`，默认是关闭的。
支持随便填写日期格式自动识别（支持时间戳、注意系统时区），留空关闭。示例：2025-01-01（指定东八时区：2025-01-01 00:00:00 +0800 CST）

## 负载均衡

本代理支持域名解析模式和tcp代理的负载均衡，在web域名添加或者编辑中内网目标分行填写多个目标即可实现轮训级别的负载均衡

## IP 黑白名单

可以按来源 IP 限制访问，分为三层：**全局**（全局参数页）、**客户端**（客户端编辑页）、**隧道或域名转发**（对应的编辑页，secret 和 p2p 隧道除外）。访问需要依次通过这三层，任何一层拒绝即拒绝，上级拒绝的访问下级无法放行。

**全局名单相当于 nps 前面的防火墙**，对所有进入 nps 的连接生效：客户端连接（bridge，包括 TCP、KCP、TLS、QUIC、WS、WSS）、所有隧道和域名转发、Web 管理界面（包括登录页和 IP 登记页）。P2P 打洞使用的 UDP 探测端口（`p2p_port`）不受全局名单限制：它只用于探测 NAT、不转发流量，而探测包的来源地址可能与客户端连接不同（多出口、IPv4/IPv6、经反代连接等），限制它容易导致打洞失败。被拒绝的地址连管理界面也无法打开，修改前请确认不会把自己挡在外面，误操作时可手动编辑 `conf/global.json` 恢复。限制谁能访问哪个内网服务，应在客户端或隧道、域名转发中配置。

每一层都可以同时配置黑名单和白名单，并选择优先级：

| | 只在白名单 | 只在黑名单 | 同时在两个名单 | 都不在 |
|---|---|---|---|---|
| 黑名单优先（默认） | 放行 | 拒绝 | 拒绝 | 拒绝 |
| 白名单优先 | 放行 | 拒绝 | 放行 | 放行 |

只配置一个名单时：只有黑名单则名单内的地址拒绝、其余放行；只有白名单则名单内的地址放行、其余拒绝。两个名单都为空时不做限制。

每行一条规则，`#` 开头为注释：

| 写法 | 含义 |
|---|---|
| `1.2.3.4`、`2001:db8::1` | 单个 IP |
| `10.0.0.0/8` | 网段 |
| `192.168.*`、`10.*` | IPv4 前缀通配 |
| `geoip:cn` | Geo 数据中的国家或地区 |
| `geoip:!cn` | 除该国家或地区以外 |
| `geoip:private` | 内网和保留地址（不需要 Geo 数据） |
| `ext-ip:文件名:代码` | Geo 数据目录下其他文件中的分类（仅全局名单可用） |

不支持 `1.1.1.1-1.1.1.5` 这类区间写法，请改用网段；前缀拼错（如 `geoipp:cn`）也会被判为无效。客户端、隧道和域名转发的名单每份最多 1000 条，可通过 `nps.conf` 的 `acl_max_rules` 修改（`0` 为不限制）。规则在保存时校验，有误时会提示具体行号，保存后统一为规范写法（例如 `10.1.2.3/8` 存为 `10.0.0.0/8`）。

旧版本保存的 IP 黑名单无需转换，原样按新规则解析；其中的空行会被忽略，无法解析的条目（旧版按字符串精确匹配，本来就不会命中）会被跳过并记录日志。

### Geo 数据

`geoip:` 和 `geosite:` 规则使用 [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) 的 `geoip.dat` 和 `geosite.dat`，发布包和 Docker 镜像都已附带，默认位于 `conf` 目录，路径可通过 `geoip_path`、`geosite_path` 修改。

更新方式：

- 在 Web 管理界面的全局参数页点击“更新 Geo 数据”；
- 或执行 `nps update-geo`（运行中的 nps 会在一分钟内自动生效）；
- 执行 `nps update` 更新程序时，如果发布包中的 Geo 数据比本地新，也会一并更新。

缺少 Geo 数据时，使用 `geoip:` 的规则会被跳过并记录日志，其余规则照常生效；白名单中的规则全部无法使用时，该白名单会拒绝所有访问，不会因为缺少数据而放行。

## 端口白名单

为了防止服务端上的端口被滥用，可在nps.conf中配置allow_ports限制可开启的端口，忽略或者不填表示端口不受限制，格式：

```ini
allow_ports=9001-9009,10001,11000-12000
```

## 端口范围映射

当客户端以配置文件的方式启动时，可以将本地的端口进行范围映射，仅支持tcp和udp模式，例如：

```ini
[tcp]
mode=tcp
server_port=9001-9009,10001,11000-12000
target_port=8001-8009,10002,13000-14000
```

逗号分隔，可单个或者范围，注意上下端口的对应关系，无法一一对应将不能成功

## 端口范围映射到其他机器

```ini
[tcp]
mode=tcp
server_port=9001-9009,10001,11000-12000
target_port=8001-8009,10002,13000-14000
target_ip=10.1.50.2
```

填写target_ip后则表示映射的该地址机器的端口，忽略则便是映射本地127.0.0.1,仅范围映射时有效

## KCP协议支持

在网络质量非常好的情况下，例如专线，内网，可以开启略微降低延迟。如需使用可在nps.conf中修改`bridge_type`为kcp
，设置后本代理将开启udp端口（`bridge_port`）

注意：当服务端为kcp时，客户端连接时也需要使用相同配置，无配置文件模式加上参数type=kcp,配置文件模式在配置文件中设置tp=kcp

## 域名泛解析

支持域名泛解析，例如将host设置为*.proxy.com，a.proxy.com、b.proxy.com等都将解析到同一目标，在web管理中或客户端配置文件中将host设置为此格式即可。

## URL路由

本代理支持根据URL将同一域名转发到不同的内网服务器，可在web中或客户端配置文件中设置，此参数也可忽略，例如在客户端配置文件中

```ini
[web1]
host=a.proxy.com
target_addr=127.0.0.1:7001
location=/test
[web2]
host=a.proxy.com
target_addr=127.0.0.1:7002
location=/static
```

对于`a.proxy.com/test`将转发到`127.0.0.1:7001/test`，对于`a.proxy.com/static/bg.jpg`将转发到`127.0.0.1:7002/static/bg.jpg`

## URL 重写
填写后自动替换请求路径里 **URL 路由** 的前缀为填写的内容，适用于前后端访问不同路径的情况

NPS 会自动添加 `X-Original-Path` 请求头用于识别浏览器请求的实际地址

例如：
- 当**URL 路由**配置为`/path/`，当**URL 重写**配置为`/`。请求`xx.com/path/index.html`将返回`127.0.0.1:80/index.html`
- 当**URL 路由**配置为`/xml`，当**URL 重写**配置为`/path/list.xml`。请求`xx.com/xml`将下载`127.0.0.1:80/path/list.xml`
- 当**URL 路由**配置为`/ws`，当**URL 重写**配置为`/websocket`。请求`xx.com/ws`将转发到`127.0.0.1:80/websocket`

## 仅允许已登记的 IP

如果把 SSH 这类风险较高的服务暴露在公网上，可以在**客户端、隧道或域名转发**的编辑页开启“仅允许已登记的 IP”。开启后，访问者除了要通过黑白名单，还必须先登记自己的 IP；只影响开启了的客户端或条目，不影响其他服务。

**登记方式：**

**方式一：** 在需要访问的机器上运行客户端：

```
./npc register -server=ip:port -vkey=CLIENT_VKEY -time=2
```

`time` 为有效小时数。用客户端的 vkey 登记时只对该客户端有效，用公钥（`public_vkey`）登记时对所有客户端有效。

**方式二：** 打开 IP 登记页面，输入客户端的**登记口令**或 vkey，即可为当前 IP 登记该客户端；输入公钥（`public_vkey`）则相当于管理员登录，对所有客户端有效。

- 登记页面的地址：
  - Web 管理界面的 `/ip/register`（配置了 `web_base_url` 时带上该前缀，例如 `/nps/ip/register`），登录页也有入口；
  - 在 `nps.conf` 中配置 `ip_register_port`（和 `ip_register_ip`）后，会在该端口单独提供登记页面，根路径即为登记页面，不暴露管理界面；同时配置 `ip_register_cert_file` 和 `ip_register_key_file` 时使用 HTTPS，否则为 HTTP；
  - 通过嵌套转发 `bridge://ipreg`，例如在域名转发中把目标设为 `bridge://ipreg`。
- 登记口令在客户端编辑页设置（可一键生成），与 vkey 分开，泄露后重置即可，不影响客户端连接；编辑页还会显示带口令的登记链接，可直接发给访问者。
- 页面上可以选择有效期，超过上限时按上限登记。上限在客户端编辑页设置，未设置时以及使用公钥登记时，取 `nps.conf` 中的 `ip_register_max_hours`（默认 2 小时）。
- 登记页面与登录共用失败记录和封禁：输错口令计入失败次数，失败后需间隔一段时间才能再试，达到上限的 IP 登录和登记都会被暂时封禁；开启验证码时登记页面同样需要验证码。

**方式三：** 登录 Web 管理界面后自动为登录的 IP 登记两小时：管理员对所有客户端有效，普通用户只对自己的客户端有效。

登记只保存在内存中，nps 重启后需要重新登记。仪表盘会显示当前已登记的 IP 数量。

**注意：** 公网 IP 可能变化，同一网络下的多人也可能共用同一个公网 IP。

旧版本的 `ip_limit` 配置已移除（它会让所有隧道都要求登记），请改用上述按客户端或条目开启的方式。

## 客户端最大连接数

为防止恶意大量长连接，影响服务端程序的稳定性，可以在web或客户端配置文件中为每个客户端设置最大连接数。该功能针对`socks5`、
`http正向代理`、`域名代理`、`tcp代理`、`udp代理`、`私密代理`生效,使用该功能需要在`nps.conf`中设置
`allow_connection_num_limit=true`，默认是关闭的。

## 客户端最大隧道数限制

nps支持对客户端的隧道数量进行限制，该功能默认是关闭的，如需开启，请在`nps.conf`中设置`allow_tunnel_num_limit=true`。

## 端口复用

在一些严格的网络环境中，对端口的个数等限制较大，nps支持强大端口复用功能。将`bridge_port`、 `http_proxy_port`、
`https_proxy_port` 、`web_port`都设置为同一端口，也能正常使用。

- 使用时将需要复用的端口设置为与`bridge_port`一致即可，将自动识别。
- 如需将web管理的端口也复用，需要配置`web_host`也就是一个二级域名以便区分

## 多路复用

nps主要通信默认基于多路复用，无需开启。

多路复用基于TCP滑动窗口原理设计，动态计算延迟以及带宽来算出应该往网络管道中打入的流量。
由于主要通信大多采用TCP协议，并无法探测其实时丢包情况，对于产生丢包重传的情况，采用较大的宽容度，
5分钟的等待时间，超时将会关闭当前隧道连接并重新建立，这将会抛弃当前所有的连接。
在Linux上，可以通过调节内核参数来适应不同应用场景。

对于需求大带宽又有一定的丢包的场景，可以保持默认参数不变，尽可能少抛弃连接
高并发下可根据[Linux系统限制](## Linux系统限制) 调整

对于延迟敏感而又有一定丢包的场景，可以适当调整TCP重传次数
`tcp_syn_retries`, `tcp_retries1`, `tcp_retries2`
高并发同上
nps会在系统主动关闭连接的时候拿到报错，进而重新建立隧道连接

## 环境变量渲染

npc支持环境变量渲染以适应在某些特殊场景下的要求。

**在无配置文件启动模式下：**
设置环境变量

```
export NPC_SERVER_ADDR=1.1.1.1:8024
export NPC_SERVER_VKEY=xxxxx
```

直接执行./npc即可运行

**在配置文件启动模式下：**

```ini
[common]
server_addr={{.NPC_SERVER_ADDR}}
conn_type=tcp
vkey={{.NPC_SERVER_VKEY}}
auto_reconnection=true
[web]
host={{.NPC_WEB_HOST}}
target_addr={{.NPC_WEB_TARGET}}
```

在配置文件中填入相应的环境变量名称，npc将自动进行渲染配置文件替换环境变量

## 健康检查

当客户端以配置文件模式启动时，支持多节点的健康检查。配置示例如下

```ini
[health_check_test1]
health_check_timeout=1
health_check_max_failed=3
health_check_interval=1
health_http_url=/
health_check_type=http
health_check_target=127.0.0.1:8083,127.0.0.1:8082

[health_check_test2]
health_check_timeout=1
health_check_max_failed=3
health_check_interval=1
health_check_type=tcp
health_check_target=127.0.0.1:8083,127.0.0.1:8082
```

**health关键词必须在开头存在**

第一种是http模式，也就是以get的方式请求目标+url，返回状态码为200表示成功

第二种是tcp模式，也就是以tcp的方式与目标建立连接，能成功建立连接表示成功

如果失败次数超过`health_check_max_failed`，nps则会移除该npc下的所有该目标，如果失败后目标重新上线，nps将自动将目标重新加入。

| 项                       | 含义                |
|-------------------------|-------------------|
| health_check_timeout    | 健康检查超时时间          |
| health_check_max_failed | 健康检查允许失败次数        |
| health_check_interval   | 健康检查间隔            |
| health_check_type       | 健康检查类型            |
| health_check_target     | 健康检查目标，多个以逗号（,）分隔 |
| health_check_type       | 健康检查类型            |
| health_http_url         | 健康检查url，仅http模式适用 |

## 日志输出

日志输出级别

(trace|debug|info|warn|error|fatal|panic|off)

**对于npc：**

```
-log_level=info -log_path=npc.log
```

默认为全输出

**对于nps：**

在`nps.conf`中设置相关配置即可

## pprof性能分析与调试

可在服务端与客户端配置中开启pprof端口，用于性能分析与调试，注释或留空相应参数为关闭。

默认为关闭状态

## 自定义客户端超时检测断开时间

客户端与服务端间会间隔5s相互发送延迟测量包，这个时间间隔不可修改。
可修改延迟测量包丢包的次数，默认为60也就是5分钟都收不到一个延迟测量回包，则会断开客户端连接。
值得注意的是需要客户端的socket关闭，才会进行重连，也就是当客户端无法收到服务端的fin包时，只有客户端自行关闭socket才行。
也就是假如服务端设置为较低值，而客户端设置较高值，而此时服务端断开连接而客户端无法收到服务端的fin包，客户端也会继续等着直到触发客户端的超时设置。

在`nps.conf`或`npc.conf`中设置`disconnect_timeout`即可，客户端还可附带`-disconnect_timeout=60`参数启动
