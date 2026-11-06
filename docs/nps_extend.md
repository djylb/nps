# 增强功能

---

## 1. 启用 HTTPS

### **1.1 NPS 直接提供 HTTPS**
NPS 可直接为域名提供 HTTPS 代理服务，类似于 Nginx 处理 HTTPS 证书。  

📌 **配置步骤：**
1. **修改 `nps.conf`**
   ```ini
   https_proxy_port=443  # 或者其他端口
   ```
2. **重启 `nps`**
   ```bash
   sudo nps restart
   ```
3. **在 Web 管理界面**
   - **添加或修改域名**
   - **上传 HTTPS 证书和密钥，或者直接填写证书文件所在路径（自动检测证书内容变化无需重启）**
   - **支持路径（绝对/相对）和文本内容方式**

📌 **未设置 HTTPS 证书时**
- **使用默认 HTTPS 证书**
- **若默认证书不存在，则仅转发 HTTPS 由后端服务器处理**

---

### **1.2 由后端服务器处理 HTTPS**
如果希望 **HTTPS 由内网服务器（如 Nginx）处理** ，在 Web 管理界面：
1. **"由后端处理 HTTPS (仅转发)" 选项设为 "是"**
2. **将目标类型 (HTTP/HTTPS) 设置为 HTTPS**

📌 **NPS 直接透传 HTTPS 流量，不解密**  
📌 **后端服务器必须正确配置 HTTPS 证书**

---

## 2. Nginx 反向代理 NPS

NPS 可与 **Nginx 配合**，用于**负载均衡、缓存优化、SSL 证书管理**。

📌 **步骤**
1. **修改 `nps.conf`**
   ```ini
   http_proxy_port=8010  # 避免与 Nginx 监听的 80 端口冲突
   ```
2. **在 Nginx 配置代理**
   ```nginx
   server {
       listen 80;
       server_name _;

       location / {
           proxy_pass http://127.0.0.1:8010;
           proxy_http_version 1.1;
           proxy_set_header Upgrade $http_upgrade;
           proxy_set_header Connection $http_connection;
           proxy_set_header Host $http_host;

           # 可信前置代理验证
           proxy_set_header X-NPS-Http-Only "password";
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

           proxy_redirect off;
           proxy_buffering off;
       }
   }
   ```

📌 **如果需要 HTTPS 反代**
- **在 Nginx 监听 443 并配置 SSL**
- **NPS 关闭 HTTPS（`https_proxy_port` 设为空）**
- **示例**
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
           proxy_set_header X-NPS-Http-Only "password";
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_redirect off;
           proxy_buffering off;
       }
   }
   ```

---

## 3. Caddy 反向代理 NPS

📌 **示例**
```Caddyfile
nps.example.com {
    reverse_proxy 127.0.0.1:8010 {
        header_up X-NPS-Http-Only "password"
    }
}
```

如果将web配置到Caddy代理,实现子路径访问nps,可以这样配置.

假设我们想通过 `http://caddy_ip:caddy_port/nps` 来访问后台, Caddyfile 这样配置:

```Caddyfile
caddy_ip:caddy_port/nps {
  ##server_ip 为 nps 服务器IP
  ##web_port 为 nps 后台端口
  proxy / http://server_ip:web_port/nps {
	transparent
  }
}
```

📌 **Web 端配置**
```ini
web_base_url=/nps
```

---

## 4. Web 管理面板使用 HTTPS

📌 **启用 HTTPS 访问 Web 管理界面**
- **在 `nps.conf` 配置**
   ```ini
   web_open_ssl=true
   web_cert_file=conf/server.pem
   web_key_file=conf/server.key
   ```
- **访问 `https://公网IP:web_port` 进行管理**

---

## 5. 关闭代理功能

📌 **完全关闭 HTTP / HTTPS 代理**
- 在 `nps.conf` 中：
   ```ini
   http_proxy_port=  # 关闭 HTTP 代理
   https_proxy_port= # 关闭 HTTPS 代理
   ```

---

## 6. 代理到本地服务器
NPS 支持 **代理到本地服务器**，相当于在 **NPS 服务器上启动了一个 `npc` 客户端**，并将流量回送到本机。

📌 **适用于**
- **NPS 服务器本身运行 Web 应用**
- **希望访问 `NPS` 服务器的 80 / 443 端口时，同时提供本地服务**
- **Web 界面上直接配置，无需额外客户端**

📌 **示例**
- **NPS 服务器本机运行 Web 服务，端口 `5000`**
- **NPS 监听 `80` 和 `443`，但想让某个域名直接访问 `5000`**
- **配置步骤**
  1. **启用 `allow_local_proxy=true`**
     ```ini
     allow_local_proxy=true
     ```
  2. **Web 管理界面：添加域名，并选择 "转发到本地"**
  3. **访问 `http://yourdomain.com`，流量将直接传递到 `5000`**

---

## 7. 其他增强功能

📌 **流量数据持久化**
```ini
flow_store_interval=10  # 统计周期（分钟）
```
- **默认不持久化**
- **不会记录使用公钥连接的客户端数据**

📌 **系统信息统计**
```ini
system_info_display=true
```
- **启用后可在 Web 面板查看服务器状态**

📌 **自定义客户端密钥**
- **Web 界面可自定义，每个客户端必须唯一**

📌 **禁用公钥访问**
```ini
public_vkey=
```

📌 **关闭 Web 管理**
```ini
web_port=
```

📌 **支持多用户管理**
```ini
allow_user_login=true
```
- **默认用户名：`user`**
- **默认密码：每个客户端的认证密钥**
- **可修改用户名和密码**

📌 **开启用户注册**
```ini
allow_user_register=true
```
- **注册按钮将在 Web 登录页面显示**

📌 **监听特定 IP**
```ini
allow_multi_ip=true
```
- **可在 `npc.conf` 里指定 `server_ip`**

---

✅ **如需更多帮助，请查看 [文档](https://github.com/djylb/nps) 或提交 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。**