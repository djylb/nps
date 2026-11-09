# 启动 NPC 客户端

## 获取启动命令

进入 Web 管理界面，点击客户端前的 `+` 号，复制启动命令。命令里是启动链接 `-launch="npc://密钥@host:8025?type=tls"`，承载写在 `type`，vkey 已按百分号编码；写法见[快速启动 -launch](/guide/client/launch.md)。`-server`、`-vkey` 仍可使用（多个连接共用一组列表、`tier`、`weight` 等多承载参数的组合只能用 `-server`），两种写法不能同时给出。

## 直接运行

```bash
./npc -launch="npc://VKEY@xxx:123?type=tls" -log=off
```

```powershell
npc.exe -launch="npc://VKEY@xxx:123?type=tls" -log="off"
```

多个服务端重复 `-launch`；旧写法：`./npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off`。

> PowerShell 中请用双引号括起参数。

## 安装服务

```bash
./npc install -launch="npc://VKEY@xxx:123?type=tls" -log=off
./npc start
npc stop
npc restart
npc uninstall
```

```powershell
npc.exe install -launch="npc://VKEY@xxx:123?type=tls" -log="off"
npc.exe start
```

修改参数后重新执行 `install`。

| 平台 | 配置目录 | 程序 |
| --- | --- | --- |
| Linux / macOS | `/etc/npc` | `/usr/bin/npc` |
| Windows | `C:\Program Files\npc` | 当前解压目录 |

> Windows 请勿删除或移动解压目录下的 `npc.exe`。

## 手动注册系统服务（多开）

一个进程可以用逗号连接多个服务端，也可以重复 `-launch` 或给 `-config` 传多个文件，通常不需要手动多开。确有需要时：

### Linux（systemd）

`/etc/systemd/system/npc1.service`（自动安装的服务名为 `Npc`）：

```ini
[Unit]
Description=NPS Intranet Penetration Client
ConditionFileIsExecutable=/usr/bin/npc
Requires=network.target
Wants=network-online.target
After=network-online.target syslog.target

[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart=/usr/bin/npc "-server=xxx:123,yyy:456" "-vkey=xxx,yyy" "-type=tcp,tls" "-debug=false" "-log=off"
Restart=always
RestartSec=120

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable npc1
systemctl start npc1

# 卸载
systemctl stop npc1
systemctl disable npc1
rm /etc/systemd/system/npc1.service
systemctl daemon-reload
```

### Windows（sc）

```powershell
cmd /c 'sc create Npc1 binPath= "D:\tools\npc.exe -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off -debug=false" DisplayName= "NPS Client 1" start= auto'
sc start Npc1

sc stop Npc1
sc delete Npc1
```

手动注册的服务更新前，必须先手动停止所有运行中的服务。

更多用法见[连接与系统服务](/guide/client/connect.md)和 [NPC 命令行参数](/reference/npc-cli.md)。
