# 使用

📌 **提示**
- **使用 Web 管理模式时，`nps` 服务器必须在项目根目录运行，否则无法正确加载配置文件。**
- **安装后 `nps` 可执行文件位置可能会发生变化，建议使用 `whereis nps` 查找具体目录。**

---

## 1. Web 管理界面

📌 **访问 Web 界面**
- 在浏览器中输入 `http://公网IP:8081`（ **默认端口 `8081`** ）
- **默认管理员账号/密码**
  - 用户名：`admin`
  - 密码：`123`（请**修改默认密码**以确保安全，新增TOTP支持）

📌 **Web 界面功能**
- **客户端管理**（添加、删除、编辑隧道）
- **域名转发**（管理 HTTP/HTTPS 代理）
- **流量统计**
- **用户管理**
- **系统配置**
- **日志查看**
- **在线文档**

---

## 2. 服务端配置文件重载

📌 **适用于**
- **修改部分 `nps.conf` 配置后，无需重启即可生效**
- **支持的参数**
  - `allow_user_login`
  - `auth_crypt_key`
  - `auth_key`
  - `web_username`
  - `web_password`
  - **未来将支持更多参数**

### **Linux/macOS**
```bash
sudo nps reload
```

### **Windows**
```powershell
nps.exe reload
```

---

## 3. 服务端停止或重启

### **Linux/macOS**
```bash
sudo nps stop    # Stop NPS
sudo nps restart # Restart NPS
```

### **Windows**
```powershell
nps.exe stop    # Stop NPS
nps.exe restart # Restart NPS
```

---

## 4. 服务端更新
📌 **适用于**
- **升级至最新版本**
- **修复已知 Bug**
- **获取最新功能**

### **步骤**
**执行更新**（已在运行的 `nps` 服务会自动重启，无需先停止）
```bash
sudo nps update # Linux/macOS
nps.exe update  # Windows
```

已是最新版本时不会替换文件，也不会重启服务。发布包中的 Geo 数据比本地新时会一并更新。

**单独更新 Geo 数据**（黑白名单中 `geoip:`、`geosite:` 规则使用的数据，运行中的 nps 会在一分钟内自动生效）
```bash
sudo nps update-geo # Linux/macOS
nps.exe update-geo  # Windows
```

**在网页上更新**：管理员登录后，在仪表盘"服务端版本"卡片右上角点击更新按钮，服务端会更新到最新版本并自动重启，页面在新版本启动后自动刷新。

- 重启期间所有连接会短暂中断，客户端会自动重连
- Linux/macOS 等系统下无论以服务还是命令行方式运行都支持；Windows 下仅支持通过 `nps install` 安装的系统服务，其他情况不显示更新按钮
- 启动参数加上 `-disable_remote_update` 可禁止网页更新；官方 Docker 镜像默认已禁止

📌 **如果更新失败**
- **手动下载最新版本**：[🔗 GitHub Releases](https://github.com/djylb/nps/releases/latest)
- **覆盖原有 `nps` 二进制文件和 `web` 目录**
- **安装 `nps` 后可执行文件路径可能会改变，使用以下命令查找**
  ```bash
  whereis nps
  ```

---

## 5. 手动覆盖 NPS 可执行文件

📌 **适用于**
- **手动下载 `nps` 二进制文件**
- **`nps update` 更新失败时**

### **Linux/macOS**
```bash
sudo systemctl stop nps   # Stop nps
whereis nps               # Find nps install path
sudo cp nps /usr/bin/nps  # Replace old binary
sudo chmod +x /usr/bin/nps # Ensure executable permission
sudo systemctl start nps  # Start nps
```

### **Windows**
```powershell
Stop-Service nps   # Stop nps
Copy-Item -Path "PATH_TO_NEW_NPS_EXE" -Destination "PATH_TO_OLD_NPS_EXE_DIR" -Force
Start-Service nps  # Start nps
```

📌 **如遇 `权限不足`，请以管理员身份运行 `PowerShell` 终端执行。**

---

✅ **如需更多帮助，请查看 [文档](https://github.com/djylb/nps) 或提交 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。**
