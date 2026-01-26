# NPS 服务端配置文件

📌 **默认配置文件路径**
- **Linux/macOS**：`/etc/nps/conf/nps.conf`
- **Windows**：`C:\Program Files\nps\conf\nps.conf`

📌 **指定自定义配置路径**
```bash
# Linux 启动
./nps -conf_path=/app/nps

# Windows 启动
nps.exe -conf_path=D:\test\nps
```

📌 **注意**
- **参数留空即为默认值**
- **修改后重启 NPS 生效**
- **涉及安全的配置，如 `web_password`，正式部署时请修改默认值**

---

## 1. 基础配置
| 名称             | 说明             |
|----------------|----------------|
| `appname`      | 应用名称           |
| `runmode`      | 运行模式（dev/pro）  |
| `dns_server`   | DNS 服务器        |
| `timezone`     | 时区             |
| `ntp_server`   | NTP 服务器        |
| `ntp_interval` | NTP 最小查询间隔（分钟） |

---
## 2. Web 管理面板相关
| 名称                  | 说明                                     |
|---------------------|----------------------------------------|
| `web_port`          | Web 管理端口（默认 `8081`）                    |
| `web_ip`            | Web 管理界面监听地址（默认 `0.0.0.0`，监听所有 IP）     |
| `web_host`          | Web 界面域名（默认 `a.o.com`，端口复用时访问管理页面的地址）  |
| `web_username`      | Web 管理员账号（默认 `admin`）                  |
| `web_password`      | Web 管理员密码（默认 `123`，建议修改！）              |
| `web_open_ssl`      | 是否启用 Web 面板 HTTPS（默认 `false`，启用需配置证书）  |
| `web_cert_file`     | Web HTTPS 证书文件路径                       |
| `web_key_file`      | Web HTTPS 证书密钥文件路径                     |
| `web_base_url`      | Web 管理主路径（默认 `/`，适用于 Web 反向代理时调整路径）    |
| `open_captcha`      | 是否启用验证码                                |
| `pow_bits`          | PoW 验证位数（默认 `20`）                      |
| `totp_secret`       | 两步验证密钥 开启后 `web_password` 失效 使用动态验证码登录 |
| `allow_x_real_ip`   | 允许通过 X-Real-IP 头获取真实IP                 |
| `trusted_proxy_ips` | 受信任的代理服务器 IP 地址（多个用逗号分隔）               |

---

## 3. 代理端口相关
| 名称                          | 说明                                                                                      |
|-----------------------------|-----------------------------------------------------------------------------------------|
| `bridge_port`               | 客户端与服务端通信端口（默认 `8024`，**仅在端口复用时需要配置**）                                                  |
| `bridge_ip`                 | 监听地址（默认 `0.0.0.0`，监听所有 IP）                                                              |
| `bridge_type`               | 连接方式（`tcp`、`udp`、`both`，默认 `both`）（已弃用）                                                 |
| `http_proxy_ip`             | HTTP 代理监听地址（默认 `0.0.0.0`）                                                               |
| `http_proxy_port`           | HTTP 代理监听端口（默认 `80`，留空不启用）                                                              |
| `https_proxy_port`          | HTTPS 代理监听端口（默认 `443`，留空不启用）                                                            |
| `http3_proxy_port`          | HTTP/3 代理监听端口（默认 `https_proxy_port`，配置`0`关闭）                                            |
| `force_auto_ssl`            | 强制自动申请证书，需自行解决80、443端口问题（默认 `false`）                                                    |
| `ssl_path`                  | 自动申请证书保存路径（默认 `ssl`）                                                                    |
| `ssl_email`                 | 自动申请证书使用的邮箱                                                                             |
| `ssl_ca`                    | 自动申请证书使用的 CA（`LetsEncrypt`、`ZeroSSL`、`GoogleTrust`，默认 `LetsEncrypt`）                    |
| `ssl_zerossl_api`           | ZeroSSL 的 API 密钥                                                                        |
| `ssl_cache_max`             | 证书缓存最大个数（0 不限制）                                                                         |
| `ssl_cache_reload`          | 证书缓存重载间隔，检测文件是否变更的时间间隔（单位：s）                                                            |
| `ssl_cache_idle`            | 证书缓存闲置清理，从缓存移除该证书的时间间隔（单位：m）                                                            |
| `bridge_tcp_port`           | 客户端与服务端通信 TCP 端口（默认 `8024`，留空不启用）                                                       |
| `bridge_kcp_port`           | 客户端与服务端通信 KCP 端口（默认 `8024`，留空不启用）                                                       |
| `bridge_tls_port`           | 客户端与服务端通信 TLS 端口（默认 `8025`，留空不启用）                                                       |
| `bridge_quic_port`          | 客户端与服务端通信 QUIC 端口（默认 `8025`，留空不启用）                                                      |
| `bridge_ws_port`            | 客户端与服务端通信 WS 端口（默认 `8026`，留空不启用）                                                        |
| `bridge_wss_port`           | 客户端与服务端通信 WSS 端口（默认 `8027`，留空不启用）                                                       |
| `bridge_tcp_ip`             | 客户端与服务端通信 TCP 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                        |
| `bridge_kcp_ip`             | 客户端与服务端通信 KCP 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                        |
| `bridge_quic_ip`            | 客户端与服务端通信 QUIC 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                       |
| `bridge_tls_ip`             | 客户端与服务端通信 TLS 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                        |
| `bridge_ws_ip`              | 客户端与服务端通信 WS 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                         |
| `bridge_wss_ip`             | 客户端与服务端通信 WSS 监听IP（可选，只有和 `bridge_ip` 不一样时才需要配置）                                        |
| `bridge_path`               | 客户端与服务端通信 WS 的请求路径                                                                      |
| `bridge_real_ip_header`     | 客户端与服务端通信 WS 获取真实IP的请求头                                                                 |
| `bridge_trusted_ips`        | 客户端与服务端通信 WS 获取真实IP信任的代理IP列表                                                            |
| `bridge_host`               | 客户端与服务端通信域名 （端口复用时使用）                                                                   |
| `bridge_cert_file`          | 客户端与服务端通信 TLS 证书文件路径                                                                    |
| `bridge_key_file`           | 客户端与服务端通信 TLS 证书密钥文件路径                                                                  |
| `bridge_http3`              | 客户端与服务端通信允许通过 HTTP/3 端口连接                                                               |
| `bridge_select_mode`        | 相同`vkey`客户端连接选取模式（主备：`0`/`Primary`/`p`，轮询：`1`/`RoundRobin`/`rr`，随机：`2`/`Random`/`rand`） |
| `quic_alpn`                 | QUIC 握手时允许协商的 ALPN 列表，逗号分隔（默认 `nps`）                                                    |
| `quic_keep_alive_period`    | QUIC 空闲保活周期（单位：s，默认 `10`）                                                               |
| `quic_max_idle_timeout`     | QUIC 最大空闲超时时间（单位：秒，默认 `30`）                                                             |
| `quic_max_incoming_streams` | QUIC 最大并发接收流数量（默认 `100000`）                                                             |


---

## 4. 认证与密钥
| 名称               | 说明                            |
|------------------|-------------------------------|
| `auth_key`       | Web API 认证密钥（建议填充复杂密钥）        |
| `auth_crypt_key` | 获取 `authKey` 的 AES 加密密钥（16 位） |
| `public_vkey`    | 客户端以配置文件模式启动时的密钥              |

---

## 5. 访问控制
| 名称                           | 说明                                             |
|------------------------------|------------------------------------------------|
| `ip_limit`                   | 是否限制 IP 访问（`true` 或 `false`）                   |
| `allow_ports`                | 允许客户端映射的端口范围（示例：`9001-9009,10001,11000-12000`） |
| `allow_user_login`           | 是否允许用户登录管理（`true` 或 `false`）                   |
| `allow_user_vkey_login`      | 是否允许用户使用 `vkey` 登录管理（`true` 或 `false`）         |
| `allow_user_register`        | 是否允许用户注册（`true` 或 `false`）                     |
| `allow_user_change_username` | 是否允许用户修改用户名（`true` 或 `false`）                  |

---

## 6. P2P 相关
| 名称         | 说明                       |
|------------|--------------------------|
| `p2p_ip`   | P2P 服务端 IP（指定P2P使用的公网IP） |
| `p2p_port` | P2P 端口（用于 P2P 通信）        |

---

## 7. 日志与流量控制
| 名称                    | 说明                                                              |
|-----------------------|-----------------------------------------------------------------|
| `log`                 | 日志模式（stdout、file、both、off）                                      |
| `log_level`           | 日志级别（`trace、debug、info、warn、error、fatal、panic、off`，默认为 `trace`） |
| `log_path`            | 日志路径（可选 `具体路径`、`off`、`docker`）                                  |
| `log_compress`        | 是否启用日志压缩（`true` 开启，`false` 关闭）                                  |
| `log_max_files`       | 允许存在的日志总文件个数（默认 `10`）                                           |
| `log_max_days`        | 允许保存日志的最大天数（默认 `7`）                                             |
| `log_max_size`        | 单个日志文件的最大大小（MB）（默认 `2MB`）                                       |
| `flow_store_interval` | 流量数据持久化间隔（分钟），留空表示不持久化                                          |

---

## 8. 反向代理与安全
| 名称                       | 说明                                 |
|--------------------------|------------------------------------|
| `http_add_origin_header` | 是否添加真实IP头（`true` 或 `false`）        |
| `x_nps_http_only`        | 前置代理传递 `X-NPS-Http-Only` 头验证，信任该代理 |

### **Nginx 代理示例**
```nginx
server {
    listen 443 ssl;
    server_name example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://127.0.0.1:80;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $http_connection;
        proxy_set_header Host $http_host;

        # 这里填 NPS 配置文件中填写的密码
        proxy_set_header X-NPS-Http-Only "password";
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        proxy_redirect off;
        proxy_buffering off;
    }
}
```

---

## 9. 其他高级配置
| 名称                           | 说明                                        |
|------------------------------|-------------------------------------------|
| `allow_flow_limit`           | 是否允许流量限制                                  |
| `allow_rate_limit`           | 是否允许带宽限制                                  |
| `allow_time_limit`           | 是否允许到期时间限制                                |
| `allow_tunnel_num_limit`     | 是否允许限制客户端最大隧道数                            |
| `allow_local_proxy`          | 是否允许 NPS 本地代理连接（相当于在nps服务器上启动一个npc）       |
| `allow_user_local`           | 是否允许用户使用 NPS 本地代理连接                       |
| `allow_secret_link`          | 是否允许私密代理客户端指定连接地址                         |
| `allow_secret_local`         | 是否允许私密代理客户端连接到服务器本地                       |
| `allow_connection_num_limit` | 是否限制客户端最大连接数                              |
| `allow_multi_ip`             | 是否允许配置隧道监听IP地址                            |
| `system_info_display`        | 是否显示系统负载监控信息                              |
| `disconnect_timeout`         | TCP 中断超时等待时间（单位 5s，默认值 60，即 300s = 5mins） |
| `http_cache`                 | 是否启用 HTTP 缓存（已弃用，不再支持该功能）                 |

---

✅ **如需更多帮助，请查看 [文档](https://github.com/djylb/nps) 或提交 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。**