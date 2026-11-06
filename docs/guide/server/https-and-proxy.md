# HTTPS 与反向代理

## NPS 直接提供 HTTPS

1. 在 `nps.conf` 设置 `https_proxy_port=443`（或其他端口），重启 nps。
2. 在 Web 管理界面添加或修改域名，填写证书和密钥：文本内容，或绝对、相对路径（相对路径基于 nps 配置目录，内容变化自动检测，无需重启）。Docker 用户请用硬链接而不是软链接。

新增或编辑域名时，如果已有条目的证书覆盖该域名，页面会提示可复用的证书。管理员可看到所有客户端的证书，普通用户只看到自己客户端的。

证书处理优先级：

1. 域名自己的证书
2. 默认证书（`https_default_cert_file`、`https_default_key_file`）；只用于已配置且被其覆盖的域名
3. 仅转发，由后端处理 HTTPS

过期证书不再使用。`ssl_cache_reload` 控制检测文件变更的周期。

### 自动证书

在域名上开启“自动证书”后，如果 nps 监听 80 或 443，会自动申请并续签。相关配置：`ssl_email`、`ssl_ca`（`LetsEncrypt`、`ZeroSSL`、`GoogleTrust`）、`ssl_zerossl_api`、`ssl_path`。`force_auto_ssl=true` 强制自动申请，需自行确保 80、443 可达。

## 由后端处理 HTTPS

在 Web 管理界面把 **“由后端处理 HTTPS (仅转发)”** 设为“是”，**目标类型**设为 HTTPS。nps 透传流量不解密，后端必须配置证书。

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

HTTP、HTTPS、HTTP/3 任一监听失败时，nps 记录错误并停止这组监听中已启动的部分，其他功能继续运行。
