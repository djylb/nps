# Web API

网页管理界面用的接口，也可以直接拿来写脚本：增删改客户端、隧道、域名转发，查看状态，重新加载配置。网页上每个操作都对应这里的一个接口。

## 开启与认证

脚本调用需要先在 `nps.conf` 里设置 `auth_key`（见[服务端配置](/reference/server-config.md)），留空则脚本认证关闭。设置后每次请求在 **URL 查询参数**里带上两个值（放在表单或 JSON 里不会被认证）：

| 参数 | 含义 |
| --- | --- |
| `timestamp` | 当前 Unix 时间戳（秒） |
| `auth_key` | `md5(配置的auth_key + timestamp)`，32 位小写十六进制 |

时间戳和服务端时间相差不能超过 20 秒。通过认证的请求按管理员处理。

```bash
# 在 Linux/macOS 上
KEY=YOUR_AUTH_KEY
TS=$(date +%s)
SIGN=$(printf '%s%s' "$KEY" "$TS" | md5sum | cut -d' ' -f1)   # macOS 用 md5 -q
curl -X POST "http://203.0.113.10:8081/client/list?auth_key=$SIGN&timestamp=$TS" \
  --data 'offset=0&limit=10'
```

不带 `auth_key` 的请求要求已登录的会话（网页登录后的 Cookie）。

## 路径与格式

- 每个接口有两个等价的路径：原路径（如 `/client/list`）和 `/api/v1` 前缀路径（如 `/api/v1/client/list`）。配置了 `web_base_url` 时，所有路径前面再加这个前缀。
- 两种路径都接受表单（`application/x-www-form-urlencoded`）。`/api/v1` 前缀路径还接受 JSON 请求体（`Content-Type: application/json`），字段名与表单参数相同。
- `/api/v1` 路径出错时返回 JSON（401 `unauthorized`、403 `forbidden`）；原路径未登录时会跳转到登录页。
- 返回 JSON 的形式有几种：
  - 操作类：`{"status":1,"msg":"..."}`，`status` 为 `1` 成功、`0` 失败；新增类还带 `id`。
  - 查询单个：`{"code":1,"data":{...}}`，失败为 `{"code":0}`。
  - 列表：`{"rows":[...],"total":N}`。
- 非管理员（客户端用户，需 `allow_user_login`）只能操作自己名下的客户端及其隧道、域名转发；标注“仅管理员”的接口对他们返回 403。

## 获取服务端信息（无需认证）

| 接口 | 方法 | 返回 |
| --- | --- | --- |
| `/auth/gettime` | GET | `{"time": 当前Unix秒}` |
| `/auth/getauthkey` | GET | `{"status":1,"crypt_auth_key":"...","crypt_type":"aes cbc"}`：用 `auth_crypt_key`（必须正好 16 个字符）加密后的 `auth_key`；没配置 `auth_crypt_key` 时 `status` 为 `0` |
| `/auth/getcert` | GET | `{"status":1,"cert":"..."}`：登录时加密密码用的 RSA 公钥（PEM） |
| `/auth/bootstrap` | GET | 当前会话、可见的页面和接口清单、功能开关，网页前端启动时读取 |

`crypt_auth_key` 的解密方式：AES-128-CBC，密钥为 `auth_crypt_key`，偏移量（IV）与密钥相同，PKCS5 补码，密文为十六进制。

## 页面（仅网页前端使用）

这些 GET 路径渲染网页，脚本一般不用：

| 路径 | 页面 |
| --- | --- |
| `/index/index` | 仪表盘 |
| `/index/tcp`、`/index/udp`、`/index/mix`、`/index/secret`、`/index/service` | TCP 隧道、UDP 隧道、混合代理、点对点访问、虚拟后端（只读）列表 |
| `/index/socks5`、`/index/http` | 只显示 SOCKS5、HTTP 代理的列表（左侧菜单不再单列，用混合代理页管理） |
| `/index/host`、`/index/hostlist` | 域名转发列表 |
| `/index/all` | 按客户端展示的隧道列表（参数 `client_id`） |
| `/index/add`、`/index/edit`、`/index/launch` | 新增、编辑隧道，隧道的启动命令（`edit`、`launch` 带参数 `id`） |
| `/index/addhost`、`/index/edithost` | 新增、编辑域名转发 |
| `/index/help` | 使用说明 |
| `/client/list`、`/client/add`、`/client/edit`、`/client/launch` | 客户端列表、新增、编辑、启动命令 |
| `/global/index`、`/global/banlist` | 全局参数、封禁管理（仅管理员） |
| `/login/index`、`/login/register` | 登录、注册（注册需 `allow_user_register`） |
| `/app` | 管理界面外壳 |

各页面的内容见 [管理页面说明](/reference/web-ui.md)。另有 `/auth/page/<控制器>/<页面>` 和 `/auth/render/<控制器>/<页面>`（也可用 `/api/` 开头）以 JSON 或 HTML 形式返回同一页面的数据。

## 仪表盘

- `GET /index/stats`（仅管理员）：仪表盘数据，`{"code":1,"data":{...}}`；非管理员返回 `{"code":0}`。

## 隧道

### 获取隧道列表

`POST /index/gettunnel`

| 参数 | 说明 |
| --- | --- |
| `client_id` | 只看某个客户端的隧道（整数，0 或不传为全部） |
| `type` | 隧道类型：`tcp`、`udp`、`mixProxy`、`httpProxy`、`socks5`、`secret`、`service` |
| `search` | 关键词 |
| `sort`、`order` | 排序字段，`asc` 或 `desc` |
| `offset`、`limit` | 分页 |

返回 `{"rows":[...],"total":N}`。

### 新增、修改隧道

新增 `POST /index/add`，修改 `POST /index/edit`（多一个必填的 `id`）。下面是表单里隧道可设置的全部参数。

| 参数 | 说明 |
| --- | --- |
| `client_id` | 所属客户端 ID |
| `type` | 隧道类型：`tcp`、`udp`、`mixProxy`、`httpProxy`、`socks5`、`secret`。虚拟后端由客户端上报，不能从这里新增 |
| `server_ip` | 服务端监听地址，留空为所有地址 |
| `port` | 服务端监听端口，`0` 或留空自动分配。新增 `tcp`、`udp` 隧道时可写端口范围，见下 |
| `target` | 目标地址，一行一个 `ip:端口`；多行即负载均衡（仅 TCP 隧道） |
| `target_type` | 默认目标使用的网络：`all`、`tcp`、`udp`；网页的新增、编辑页不显示这一项，接口仍接受 |
| `password` | 点对点访问的唯一标识密钥，访问端凭它连接（`secret` 类型用） |
| `enable_http` | 混合代理是否启用 HTTP 代理（`0`/`1`） |
| `enable_socks5` | 混合代理是否启用 SOCKS5 代理（`0`/`1`） |
| `dest_acl_mode` | 目标访问控制：`0` 关闭，`1` 白名单，`2` 黑名单；点对点访问隧道上是访问端的访问范围，关闭时不限制 |
| `dest_acl_rules` | 目标访问控制条目，多行，写法见[混合代理](/guide/tunnels/mixed-proxy.md) |
| `auth` | 代理的账号认证，多行，每行 `账号=密码`；留空不启用 |
| `proxy_protocol` | 向目标发送 Proxy Protocol：`0` 关闭，`1` V1，`2` V2 |
| `local_proxy` | 代理到服务器本地（`0`/`1`），受 `allow_local_proxy` 约束 |
| `remark` | 备注 |
| `compress` | 这条隧道的压缩算法：空为跟随客户端，`snappy`、`zstd`、`flate`，`off` 不压缩 |
| `max_conn` | 最大连接数，`0` 不限 |
| `upload_limit`、`download_limit` | 上行、下行限速，KB/s，`0` 不限 |
| `flow_limit` | 流量限制，MB，空为不限 |
| `time_limit` | 到期时间，空为不限，格式自由，见[限制与统计](/reference/features-limits.md) |
| `flow_reset` | 为 `1` 时清空这条隧道已统计的流量 |
| `health_check` | 启用健康检查（`0`/`1`），见[健康检查](/guide/tunnels/health-check.md) |
| `health_check_type` | 检查方式：`tcp`、`http`、`https` |
| `health_check_path` | `http`、`https` 检查的路径 |
| `health_check_interval`、`health_check_timeout` | 间隔、超时，秒 |
| `health_check_max_fail` | 连续失败多少次判定下线 |
| `iplistpriority` | 来源名单优先级：`0` 黑名单优先，`1` 白名单优先 |
| `blackiplist`、`whiteiplist` | 来源 IP 黑、白名单，多行，写法见[访问控制](/guide/server/access-control.md) |
| `registered_only` | 仅允许已登记的 IP（`0`/`1`） |
| `id` | 隧道 ID（修改时必填） |

**端口范围**：新增 `tcp`、`udp` 隧道时，`port` 可以写成 `8000-8100,9000`，每个端口新建一条独立隧道，备注为“备注-端口”（备注为空时就是端口）。此时 `target` 只能一行：`host:端口` 表示所有端口转发到它，`host:8000-8100,9000` 则与监听端口一一对应，数量不一致报错；多行负载均衡目标不能与范围同用。整批要么全部创建，要么一条都不创建，失败时消息里带出错的端口。一次最多展开的条数由 `web_tunnel_port_range_max` 限制（默认 1000，`0` 不限）。成功创建多条时返回里有 `count`，`id` 是第一条的 ID。其他类型不支持范围。

### 单条隧道操作

| 接口 | 参数 | 说明 |
| --- | --- | --- |
| `POST /index/getonetunnel` | `id` | 取详情 |
| `POST /index/start`、`POST /index/stop` | `id`；可选 `mode`（`http`、`socks5`） | 启动、停止隧道；带 `mode` 时是开、关该隧道上的 HTTP 或 SOCKS5 代理 |
| `POST /index/clear` | `id`、`mode` | `mode` 为 `flow`（清空流量统计）、`flow_limit`（取消流量限制）、`time_limit`（取消时间限制）、`http`、`socks5`（切换代理开关） |
| `POST /index/del` | `id` | 删除 |
| `POST /index/launchform` | `id` | 取这条隧道已保存的启动命令表单 |
| `POST /index/savelaunch` | `id`、`form` | 保存启动命令表单，`form` 为空则恢复默认 |

## 域名转发

### 获取列表

`POST /index/hostlist`，参数 `client_id`、`search`、`sort`、`order`、`offset`、`limit`，返回 `{"rows":[...],"total":N}`。

### 新增、修改域名转发

新增 `POST /index/addhost`，修改 `POST /index/edithost`（多一个必填的 `id`）。

| 参数 | 说明 |
| --- | --- |
| `client_id` | 所属客户端 ID |
| `host` | 域名，如 `www.example.com`，可用泛域名 `*.example.com` |
| `scheme` | 接受的协议：`all`、`http`、`https` |
| `target` | 内网目标，一行一个 `ip:端口`，多行即负载均衡 |
| `target_is_https` | 目标是 HTTPS（`0`/`1`） |
| `location` | URL 路由，空为不限 |
| `path_rewrite` | URL 重写 |
| `redirect_url` | 307 重定向地址，空为关闭 |
| `hostchange` | 改写发往目标的 `Host` |
| `header` | 请求头修改，一行一个 `名称: 值` |
| `resp_header` | 响应头修改，写法同 `header` |
| `auth` | 站点保护的账号认证，多行，每行 `账号=密码` |
| `https_just_proxy` | TLS 直通（`0`/`1`） |
| `tls_offload` | TLS 终止（`0`/`1`） |
| `auto_ssl` | 自动证书（`0`/`1`） |
| `cert_file`、`key_file` | HTTPS 证书和私钥，PEM 文本或文件路径 |
| `auto_https` | 自动跳转 HTTPS（`0`/`1`） |
| `auto_cors` | 自动修复 CORS（`0`/`1`） |
| `compat_mode` | 兼容模式（`0`/`1`） |
| `proxy_protocol`、`local_proxy`、`remark`、`compress`、`max_conn`、`upload_limit`、`download_limit`、`flow_limit`、`time_limit`、`flow_reset` | 含义同隧道 |
| `health_check`、`health_check_type`、`health_check_path`、`health_check_interval`、`health_check_timeout`、`health_check_max_fail` | 健康检查，含义同隧道 |
| `iplistpriority`、`blackiplist`、`whiteiplist`、`registered_only` | 来源名单，含义同隧道 |
| `id` | 域名转发 ID（修改时必填） |

各选项的用法见[域名转发](/guide/tunnels/domain-forwarding.md)和[站点与 HTTP](/reference/features-http.md)。

### 单条域名转发操作

| 接口 | 参数 | 说明 |
| --- | --- | --- |
| `POST /index/gethost` | `id` | 取详情 |
| `POST /index/starthost`、`POST /index/stophost` | `id` | 启动、停止 |
| `POST /index/clearhost` | `id`、`mode` | `mode` 为 `flow`、`flow_limit`、`time_limit`（同隧道），或 `auto_ssl`、`https_just_proxy`、`tls_offload`、`auto_https`、`auto_cors`、`compat_mode`、`target_is_https`（切换对应开关） |
| `POST /index/delhost` | `id` | 删除 |
| `POST /index/certsuggestion` | `host`、`exclude_id`（排除的条目）、`client_id`（非管理员必填） | 有可复用的证书时返回 `code: 1` 和来源条目、证书内容 |

## 客户端

### 获取列表

`POST /client/list`，参数 `search`、`sort`、`order`、`offset`、`limit`。除 `rows`、`total` 外还返回 `ip`、`addr`、`bridgeType`、`bridgePort`（网页生成启动命令用的服务端连接信息），管理员还有 `traffic`（服务端总转发流量与速率）。

### 新增、修改客户端

新增 `POST /client/add`，修改 `POST /client/edit`（多一个必填的 `id`）。

| 参数 | 说明 |
| --- | --- |
| `remark` | 备注 |
| `vkey` | 唯一验证密钥，新增时不填自动生成 |
| `u`、`p` | Basic 认证用户名、密码（用于该客户端的 SOCKS5、HTTP 代理和域名转发） |
| `config_conn_allow` | 允许客户端通过配置文件方式连接（`0`/`1`） |
| `compress` | 压缩：`0` 关闭，`1` 开启（算法默认 snappy），或直接写算法 `snappy`、`zstd`、`flate` |
| `compress_alg` | `compress=1` 时的算法；`off` 等同关闭。隧道和域名转发的 `compress` 可单独覆盖 |
| `crypt` | 加密（`0`/`1`）；新增时不传按开启，编辑时不传保持原值 |
| `shaping` | 对该客户端启用流量整形（`0`/`1`），服务端配置了整形来源才生效 |
| `scheduler_down` | nps 向该客户端发送数据的调度：`affinity`（默认）、`session_affinity`、`spread_eta`、`spread_round_robin`、`spread_rate_weighted` |
| `scheduler_up` | 客户端向 nps 发送数据的调度，取值同上；空为不下发，npc 用本地的 `-scheduler_up` |
| `flow_limit` | 流量限制，MB，空为不限 |
| `time_limit` | 到期时间，空为不限 |
| `rate_limit` | 带宽限制，KB/s，空为不限 |
| `max_conn` | 最大连接数，空为不限 |
| `max_tunnel` | 最大隧道数，空为不限 |
| `flow_reset` | 修改时：为 `1` 清空流量统计 |
| `web_username`、`web_password`、`web_totp_secret` | 该客户端的网页登录账号、密码、TOTP 密钥 |
| `iplistpriority`、`blackiplist`、`whiteiplist`、`registered_only` | 来源名单，含义同隧道 |
| `register_key` | IP 登记口令，留空则只能用 vkey 登记 |
| `register_max_hours` | 登记页面登记的最长有效时间（小时），`0` 为 2 小时 |
| `id` | 客户端 ID（修改时必填） |

### 单个客户端操作

| 接口 | 参数 | 说明 |
| --- | --- | --- |
| `POST /client/getclient` | `id` | 取详情 |
| `POST /client/pingclient` | `id` | 延迟检测，返回 `{"code":1,"rtt":毫秒}` |
| `POST /client/changestatus` | `id`、`status`（`0`/`1`） | 启用或停用客户端 |
| `POST /client/clear` | `id`、`mode` | 仅管理员。`mode` 为 `flow`、`flow_limit`、`time_limit`、`rate_limit`、`conn_limit`、`tunnel_limit`；`id` 为 `0` 对所有客户端生效 |
| `POST /client/kick` | `id` | 仅管理员。踢下线，配置不变，客户端可重连 |
| `POST /client/update` | `id` | 仅管理员。请求在线客户端自我更新 |
| `POST /client/del` | `id` | 删除 |
| `POST /client/launchform` | `id` | 取已保存的启动命令表单 |
| `POST /client/savelaunch` | `id`、`form` | 保存启动命令表单，空则恢复默认 |
| `GET /client/qr` | `text`，或 `account` + `secret` | 返回 PNG 二维码；后者由服务端生成 TOTP 的 otpauth 链接 |

## 登录与注册

| 接口 | 说明 |
| --- | --- |
| `POST /login/verify` | 登录。参数 `username`；`password` 是用 `/auth/getcert` 的公钥加密的 JSON（`n` 随机串、`t` 毫秒时间戳、`p` 明文密码）；`captcha_id`、`captcha`（开启验证码时必填，开启 TOTP 时 6 位动态码接在明文密码后面，也可接在验证码后面）；`powx`、`bits`（强制工作量证明或触发风控时必填） |
| `POST /login/register` | 注册（需 `allow_user_register`）。参数 `username`、`password`（加密方式同登录）、`captcha_id`、`captcha` |
| `GET /login/out` 或 `POST /api/v1/login/logout` | 登出 |
| `GET /captcha/new` | 取新验证码，`/captcha/<id>` 取图片 |

登录来源受 `login_acl_mode` 限制，连续失败会被封禁，见[访问控制](/guide/server/access-control.md)。脚本调用一般用上面的 `auth_key`，不需要登录。

## IP 登记（无需登录）

- 页面 `GET /ip/register`，可带 `key` 预填口令。登记服务也可以单独监听（`ip_register_port`），或用域名转发的目标 `bridge://ipreg` 挂到自己的域名上；这两种方式不带 `web_base_url` 前缀，根路径就是登记页面。
- 登记 `POST /ip/register`：`key`（客户端的登记口令或 vkey；`public_vkey` 对所有客户端生效）、`hours`（有效小时数，留空或超过上限按上限）、`captcha_id`、`captcha`（开启验证码时必填）。返回 `status`、`msg`、`ip`、`until`。输错口令计入登录失败次数。

用法见[访问控制](/guide/server/access-control.md)。

## 全局管理（仅管理员）

| 接口 | 说明 |
| --- | --- |
| `POST /global/save` | 保存全局参数：`globalIpListPriority`（`0` 黑名单优先，`1` 白名单优先）、`globalBlackIpList`、`globalWhiteIpList`（多行）、`globalRateLimit`（全局带宽，KB/s）、`globalMaxConn`（全局最大转发连接数）。名单有误时 `status` 为 `0`，`msg` 给出名单和行号 |
| `POST /global/updategeo` | 更新 Geo 数据，参数 `name`（`geoip` 或 `geosite`） |
| `POST /global/reload` | 重新加载 `nps.conf`，效果同 `SIGUSR1`，见[运维](/guide/server/operations.md)。返回 `status`（`1` 新配置已生效）、`msg`、`report` |
| `GET /global/export` | 导出配置为 JSON 文件，含明文密钥，见[备份与恢复](/guide/server/backup.md) |
| `POST /global/import` | 导入配置，表单字段 `data` 为文件内容 |
| `POST /global/banlist` | 登录封禁列表 |
| `POST /global/unban` | 解除一条封禁，参数 `key` |
| `POST /global/unbanall` | 清除所有封禁记录 |
| `POST /global/banclean` | 立即清理过期的封禁 |
| `POST /index/updateserver` | 更新 nps 自身 |

`/global/reload` 的 `report` 含：`path`（配置文件）、`error`（读取失败的原因，此时什么都没变）、`applied`（已生效的设置）、`restart`（需要重启才生效的设置）、`failed`（失败项，有失败时整次重载撤销）、`rollback_failed`（撤销时未能还原的项，通常为空）。

## 实时状态

`POST /live/status`：只取运行中会变的数据，供页面定时刷新。

- `resource`：`client`、`tunnel` 或 `host`。
- `items`：JSON 数组，如 `[{"id":1,"fields":["Speed","NowConn"]}]`，最多 500 项，每项最多 16 个字段。可用字段：`Speed`、`UploadRate`、`DownloadRate`、`Flow.InletFlow`、`Flow.ExportFlow`、`TotalFlow`、`ActualFlow`、`FlowRemain`、`NowConn`；隧道还有 `Flow.DirectIn`、`Flow.DirectOut`、`RunStatus`；客户端还有 `IsConnect`、`ConnectionHistory`、`LastOnlineTime`；隧道和域名转发还有 `Client.IsConnect`；隧道和客户端有 `Status`，域名转发有 `IsClose`。
- `global`：只有 `resource=client` 且是管理员时可用，逗号分隔的 `In`、`Out`、`InBps`、`OutBps`。

按资源归属和读取权限逐项过滤，不返回配置、密钥或名单。事件推送见 [Webhook](/guide/server/webhook.md)。

## 日志（仅管理员）

内存中保留最近 `log_memory` 条日志（默认 1000），格式见[日志](/guide/server/logging.md)。这些接口只有 `/api/v1` 路径。

- `GET /api/v1/logs/entries`，参数均可选：
  - `after`：只返回序号大于它的条目；
  - `level`：最低级别（`trace`、`debug`、`info`、`warn`、`error`）；
  - `module`、`event`：逗号分隔的模块名、事件名；
  - `text`：消息或字段中包含的文字，不区分大小写；
  - `limit`：最多返回多少条，默认和上限都是 1000。

  返回 `{"enabled":true,"entries":[{"seq":..,"time":..,"level":"WARN","module":..,"event":..,"msg":..,"attrs":[{"key":..,"value":..}]}],"seq":..,"size":..}`。把返回的 `seq` 作为下次的 `after` 反复查询即可持续跟随；`log_memory=0` 时 `enabled` 为 `false`。
- `GET /api/v1/logs/levels`：返回 `{"level":"info,p2p=debug"}`，即当前 `log_level`。
- `POST /api/v1/logs/override`：参数 `module`（留空表示所有模块）、`level`、`ttl`（秒）。在 `ttl` 秒内让该模块输出到指定级别，到期自动恢复，不修改 `nps.conf`；`ttl=0` 立即结束。
