# 启动 NPS 服务端

## 安装并启动

```bash
# Linux / macOS
sudo ./nps install
sudo nps start
nps stop
nps restart
nps uninstall

# 自定义配置路径
./nps -conf_path="/app/nps"
./nps install -conf_path="/app/nps"
```

```powershell
# Windows，以管理员身份运行
.\nps.exe install
.\nps.exe start
.\nps.exe -conf_path="D:\test\nps"
.\nps.exe install -conf_path="D:\test\nps"
```

`-conf_path`（`-c`）可以是目录，也可以是 `.conf`、`.ini`、`.yaml`、`.yml`、`.json` 配置文件。不带命令时 nps 在前台运行；未知命令会打印用法并以非零状态退出。

| 命令 | 作用 |
| --- | --- |
| `install` / `uninstall` | 安装、卸载系统服务 |
| `start` / `stop` / `restart` | 控制系统服务 |
| `update` / `update-force` | 更新到最新发布版本；后者跳过版本比较 |
| `update-geo [geoip\|geosite]` | 更新 Geo 数据 |
| `version` | 显示版本，同 `-version` |
| `service` | 由服务管理器调用 |

其他参数：`-version`、`-gen2fa`（生成 TOTP 密钥）、`-get2fa=<密钥>`（输出当前动态码）、`-disable_remote_update`（禁止从网页更新）。

<a id="dirs"></a>

## 目录

| 平台 | 配置目录 | 程序 |
| --- | --- | --- |
| Linux / macOS | `/etc/nps` | `/usr/bin/nps` |
| Windows | `C:\Program Files\nps` | 当前解压目录 |

配置文件在 `conf/nps.conf`。配置目录不存在时，使用程序所在目录。

> Windows 请勿删除或移动解压目录下的 `nps.exe`，服务依赖它运行。

## 访问 Web 管理

打开 `http://<服务器IP>:8081`，默认账号 `admin`、密码 `123`。**正式使用请修改默认密码。** 然后创建客户端。

## 启动失败时

先停止服务，再前台运行查看输出：

```bash
nps stop
./nps
```

日志位置由 `nps.conf` 的 `log`、`log_path` 决定。常见原因是端口冲突，见 [FAQ](/reference/faq.md)。

## 手动注册系统服务（多开）

`install` 会自动注册服务（Linux 上服务名为 `Nps`）。只有在同一台机器运行多个实例时才需要手动注册。

### Linux（systemd）

`/etc/systemd/system/nps1.service`：

```ini
[Unit]
Description=NPS Intranet Penetration Server
ConditionFileIsExecutable=/usr/bin/nps
Requires=network.target
Wants=network-online.target
After=network-online.target syslog.target

[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart=/usr/bin/nps "-conf_path=/app/nps1" "service"
Restart=always
RestartSec=120

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable nps1
systemctl start nps1

# 卸载
systemctl stop nps1
systemctl disable nps1
rm /etc/systemd/system/nps1.service
systemctl daemon-reload
```

`Wants=network-online.target` 从 0.35.0 起写入自动安装的服务，已安装的服务需重新 `install` 才生效。参考 [systemd 文档](https://docs.redhat.com/zh-cn/documentation/red_hat_enterprise_linux/9/html/configuring_basic_system_settings/managing-system-services-with-systemctl_managing-systemd#starting-a-system-service_managing-system-services-with-systemctl)。

### Windows（sc）

以管理员身份运行 PowerShell：

```powershell
cmd /c 'sc create Nps1 binPath= "D:\NPS\nps.exe -conf_path=D:\NPS\ service" DisplayName= "NPS Server 1" start= auto'
sc start Nps1

sc stop Nps1
sc delete Nps1
```

手动注册的服务更新前，必须先手动停止所有运行中的服务。参考[微软 sc 命令指南](https://learn.microsoft.com/zh-cn/windows-server/administration/windows-commands/sc-create)。
