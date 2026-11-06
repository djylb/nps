# FAQ（常见问题解答）

---

## 1. 服务端相关问题

### **服务端无法启动**
```
服务端默认配置启用了 8024、8080、80、443 端口，端口冲突可能导致无法启动，请修改配置文件中的端口设置。
```

### **服务端配置文件修改无效**
```
Linux 安装后，配置文件位于 `/etc/nps`，请修改该路径下的 `nps.conf`。
```

### **关于 IPv6 支持**
```
NPS 默认支持 IPv6，无需额外配置，已在 IPv4/IPv6 双栈协议上监听。
```

---

## 2. 客户端相关问题

### **客户端无法连接服务端**
```
请检查：
1. 服务器防火墙 / 云提供商的安全组是否开放相关端口。
2. 客户端 vkey 是否与服务器端配置匹配。
3. 客户端和服务端的 NPS 版本是否兼容。
```

### **Windows 7 程序无法启动，或前台能连接但服务离线**

请使用 `old` 包，补齐 Windows 7 SP1、DLL 加载接口及 SHA-2 相关更新，并检查本地计算机根证书、系统时间和服务日志。`RUNNING` 只表示服务进程已启动，不能据此判断是否连接成功。补丁编号、证书更新和验证步骤见 [Windows 7 系统更新与证书](install.md#windows7)。

### **客户端隧道端口连不上**
```
如果使用 Docker 部署，但未指定 `--net=host` 网络模式：
- 隧道端口应由 `NPS` 服务端暴露，而非 `NPC` 客户端。
- `NPS` 作为代理服务器，`NPC` 仅作为隧道客户端。
```

### **P2P穿透失败 [P2P服务](/example?id=P2P服务)**
```
双方NAT类型都是Symmetric Nat一定不成功，建议先查看NAT类型。请按照文档操作(标题上有超链接)
```

### **客户端命令行方式启动多个隧道**
```
支持使用逗号拼接多个隧道：
客户端支持同时连接多个服务器，示例：  
`npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls`  
这里 `xxx:123` 使用 tcp, `yyy:456` 和 `zzz:789` 使用tls

支持省略填写，示例：
`npc -server=xxx:123 -vkey=key1,key2,key3`
```

---

## 3. 反向代理相关问题

### **NPS 作为反向代理，如何保留真实 IP？**
```
当 NPS 直接代理 HTTP/HTTPS 请求时，可以使用 `X-Forwarded-For` 或 `X-Real-IP` 头获取真实客户端 IP：
- 确保 `nps.conf` 里 `http_add_origin_header=true`
- 目标服务器（后端 Web 服务器）可使用：
  - `X-Forwarded-For` 头来获取原始 IP
  - `X-Real-IP` 头获取第一个代理的 IP
如果后端支持Proxy Protocol的话可以启用Proxy Protocol来传递IP信息，需要后端服务支持该协议。
```

### **NPS 作为后端，被 Nginx/Caddy 代理时如何配置？**
```
如果 Nginx/Caddy 作为前端代理 NPS，建议它们提供 SSL 证书，并使用 `X-NPS-Http-Only` 头：
- 避免 NPS 自动 301 重定向
- 传递真实客户端 IP
```
#### **Nginx 配置示例**
```nginx
server {
    listen 443 ssl;
    server_name _;

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
> 📌 **说明**：
> - `X-NPS-Http-Only` 避免 NPS 处理 301 重定向
> - `X-Real-IP` & `X-Forwarded-For` 传递真实客户端 IP
> - NPS 处理 HTTP 代理流量，不提供 SSL 证书

---

## 4. 域名转发与 HTTPS 相关问题

### **如何配置 HTTPS 证书和密钥？**
```
NPS 支持 HTTPS 证书和密钥，可以使用文件路径或直接填入证书内容：
- **路径支持**：绝对路径或相对路径（相对路径基于 NPS 二进制文件所在目录）。
- **Docker 用户**：请使用硬链接，而非软链接。
```

### **Auto CORS 自动跨域**
```
NPS 可自动插入 CORS 头部，允许跨域访问，但建议在后端实现更细粒度的控制。
```

### **域名转发 HTTPS 处理逻辑**
```
1. 访问 NPS 时使用的模式 ≠ 后端服务器模式。
2. 目标应填写 后端 HTTP 端口，NPS 会自动转发。
3. 如后端仅支持 HTTPS：
   - 选择 `HTTPS` 作为后端类型。
   - 可启用 `由后端处理 HTTPS（仅转发）`，避免 NPS 需要证书。
```
**HTTPS 处理优先级：**
```
1. 用户自定义证书
2. 默认证书
3. 由后端处理 HTTPS（仅转发）
```
---

## 5. 日志与调试

### **如何查看日志？**
```
日志路径可在 `nps.conf` 里配置：
1. Windows 默认日志文件：当前运行目录下的 `nps.log`
2. Linux/macOS 默认日志路径：`/var/log/nps.log`
```

### **NPS 日志配置（nps.conf）**
```ini
# 日志模式:stdout|file|both|off
log=stdout
# 日志级别:trace|debug|info|warn|error|fatal|panic|off
log_level=trace
# 日志输出路径
log_path=conf/nps.log
# 是否启用日志压缩 (true|false)
log_compress=false
# 允许保存的日志文件总数
log_max_files=10
# 允许保存日志的最大天数
log_max_days=7
# 单个日志文件的最大大小（MB），超过此大小将自动轮换
log_max_size=2
```

---

## 6. 其他功能相关

### **到期时间限制**
```
NPS 支持客户端到期时间，在 `nps.conf` 里添加：
allow_time_limit=true
可在 Web 管理界面手动设置到期时间。

示例：
2025-01-01
或
2025-01-01 00:00:00 +0800 CST
```

### **TLS 端口设置**
```
`nps.conf` 新增 `bridge_tls_port=8025`，当 `bridge_tls_port` 不为 `0` 时，NPS 会监听 8025 端口。

客户端可选择连接 TLS 端口或非 TLS 端口：
- `npc.exe -server=xxx:8024 -vkey=xxx`
- `npc.exe -server=xxx:8025 -vkey=xxx -type=tls`
```

### **NPS 读取指定配置文件**
```
新增 `-conf_path` 参数，允许 NPS 读取指定配置路径及 Web 资源文件。

Windows 示例：
- 直接启动：`nps.exe -conf_path=D:\test\nps`
- 安装服务：`nps.exe install -conf_path=D:\test\nps`
- 启动服务：`nps.exe start`

Linux 示例：
- 直接启动：`./nps -conf_path=/app/nps`
- 安装服务：`./nps install -conf_path=/app/nps`
- 启动服务：`nps start -conf_path=/app/nps`
```

---

> **如遇问题，请先检查日志，确保防火墙、端口开放，或至 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。** ✅
