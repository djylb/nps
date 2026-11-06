# 更新与维护

## 更新

进入程序目录执行，已在运行的服务会自动重启，无需先停止：

```bash
sudo npc update  # Linux/macOS
npc.exe update   # Windows
npc update-force # 跳过版本比较，重新安装最新发布版本
```

已是最新版本时 `update` 不替换文件也不重启。`update`、`update-force` 和远程更新都不会跨大版本（如 0.x.x 到 1.x.x），大版本升级需手动进行。失败时手动下载[最新版本](https://github.com/djylb/nps/releases/latest)，覆盖原有 `npc` 文件。

## 远程更新

管理员可以在客户端列表点击在线客户端的更新按钮，让它自行更新到最新版本并重启。按钮仅在客户端支持远程更新（0.34.9 及以上）且版本低于服务端时显示。

- 客户端从 GitHub 下载官方发布版本并校验，替换前先验证新版本能在本机运行，失败时保持原版本。
- Linux、macOS、OpenWrt 等系统下，无论以服务还是命令行运行都支持；Windows 只支持 `npc install` 安装的系统服务，命令行方式运行时忽略更新指令。
- `-disable_remote_update` 禁止远程更新；官方 Docker 镜像默认已禁止。

## 断线重连

默认自动重连（`-auto_reconnect=true`，配置文件 `auto_reconnection=true`）。每次重连都重新解析服务器地址；KCP、QUIC 依次尝试域名的所有地址。

`dns_server` 不可达时回退到系统 DNS；该服务器有应答（包括域名不存在）时不回退。

## 判断服务是否真正在线

系统服务状态正常只表示进程在运行。请结合 npc 日志和 Web 管理界面确认。
