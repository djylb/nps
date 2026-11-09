# HTTPS 与反向代理

用域名访问内网网站（“域名转发”）时，HTTPS 有三种做法：让 nps 自己提供 HTTPS，让内网的后端自己处理 HTTPS，或者在 nps 前面放 Nginx、Caddy 之类的反向代理。选哪种取决于证书放在哪里。域名转发本身怎么建，见[域名转发](/guide/tunnels/domain-forwarding.md)。

## NPS 直接提供 HTTPS

1. 在 `nps.conf` 里确认 `https_proxy_port=443`（或其他端口，发布包自带的配置已开启，`0` 为关闭），改过需要重启 nps。
2. 在网页“域名转发”里添加或修改域名，填写“HTTPS 证书（pem格式）”和“HTTPS 密钥（key格式）”：可以是文本内容，也可以是文件路径（相对路径以 nps 的运行目录为基准，文件内容变化会自动检测，无需重启）。Docker 用户请用硬链接而不是软链接。

新增或编辑域名时，如果已有条目的证书覆盖该域名，页面会提示可复用的证书。管理员可看到所有客户端的证书，普通用户只看到自己客户端的。

证书处理优先级：

1. 域名自己的证书
2. 默认证书（`https_default_cert_file`、`https_default_key_file`）；只用于已配置且被其覆盖的域名
3. TLS 直通，由后端处理 HTTPS（见下一节）

过期证书不再使用。`ssl_cache_reload` 控制检测文件变更的周期。

### 自动证书

在域名上开启“自动证书”后，如果 nps 监听 80 或 443，会自动申请并续签（证书机构需要从公网访问你的 80 或 443 端口来验证域名）。相关配置：`ssl_email`、`ssl_ca`（`LetsEncrypt`、`ZeroSSL`、`GoogleTrust`）、`ssl_zerossl_api`、`ssl_path`。`force_auto_ssl=true` 强制所有域名自动申请，需自行确保 80、443 可达。申请成功或失败都会在日志里留下 `cert_issued`、`cert_failed` 事件，失败时怎么查见[排错](/guide/server/troubleshooting.md)。

## 由后端处理 HTTPS

证书放在内网的后端上时，在网页域名转发的添加或编辑页，把 **“TLS 直通（由后端解密）”** 设为“是”，**“目标类型 (HTTP/HTTPS)”** 选 HTTPS。nps 只按域名（SNI）分流，数据原样转给后端，不解密，所以后端必须自己配置证书。多个站点共用同一证书和 IP 时，建议后端关闭 HTTP/2。

页面上还有“TLS 终止（解密转发）”：nps 只处理 TLS 握手，把解密后的数据转给后端。

## Nginx 反向代理 NPS

修改 `nps.conf`，避免与 Nginx 冲突：

```ini
http_proxy_port=8010
https_proxy_port=0
x_nps_http_only=password
```

```nginx
server {
    listen 80;
    listen 443 ssl;
    server_name _;

    ssl_certificate /etc/ssl/fullchain.pem;
    ssl_certificate_key /etc/ssl/key.pem;

    location / {
        proxy_pass http://127.0.0.1:8010;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $http_connection;
        proxy_set_header Host $http_host;

        # 这里填 nps.conf 中 x_nps_http_only 的值
        proxy_set_header X-NPS-Http-Only "password";
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        proxy_redirect off;
        proxy_buffering off;
    }
}
```

`X-NPS-Http-Only` 让 nps 信任该代理、避免自动 301；`X-Real-IP` 和 `X-Forwarded-For` 传递真实 IP。前面有反代或端口映射时，用 `http_proxy_access_port`、`https_proxy_access_port` 设置页面生成链接使用的对外端口。

## Caddy 反向代理 NPS

```Caddyfile
nps.example.com {
    reverse_proxy 127.0.0.1:8010 {
        header_up X-NPS-Http-Only "password"
    }
}
```

把管理界面放在子路径（`/nps`）时，先设置 `web_base_url=/nps`：

```Caddyfile
example.com {
    handle /nps* {
        reverse_proxy 127.0.0.1:8081
    }
}
```

## Web 管理界面使用 HTTPS

```ini
web_open_ssl=true
web_cert_file=conf/server.pem
web_key_file=conf/server.key
```

然后访问 `https://公网IP:web_port`。

## 关闭代理功能

端口设为 `0` 或留空即不启动：

```ini
http_proxy_port=0
https_proxy_port=0
http3_proxy_port=0
```

## 获取真实 IP

`http_add_origin_header=true` 时，域名转发在请求头注入 `X-Forwarded-For` 和 `X-Real-IP`。后端支持时也可以开启 [Proxy Protocol](/reference/features-http.md)。

HTTP、HTTPS、HTTP/3 任一监听失败时（常见原因是端口被占用），nps 记录错误并停止这组监听中已启动的部分，其他功能继续运行。HTTP/3 的细节见[HTTP 功能](/reference/features-http.md)。
