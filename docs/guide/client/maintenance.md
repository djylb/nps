# 更新与维护

## 更新版本

运行下面的命令。系统服务正在运行时，更新后会自动重启，不用先停止。

```bash
sudo npc update      # Linux、macOS
```

```powershell
.\npc.exe update     # Windows
```

- 已是最新版本时，不替换文件，也不重启。
- `npc update-force` 无论当前版本如何，都安装最新发布版本，可以用来重新安装或退回。
- 更新前会校验发布包，并确认新程序能在本机运行，失败时保持原版本。
- 跨主版本不能直接更新，需要阅读发布说明后手动升级。
- 更新失败时，可以从[发布页](https://github.com/djylb/nps/releases/latest)下载对应系统的文件，覆盖原来的 `npc`。

如果有其他进程还在运行旧程序，命令会打印它们的进程号，需要自己重启。

## 在网页上远程更新

在网页“客户端”列表里，管理员可以看到在线客户端“版本”一栏旁的更新按钮（仅在客户端支持远程更新时显示），点击后客户端自行更新并重启。

- Linux、macOS、OpenWrt 等系统上，服务方式和命令行方式都支持。
- Windows 上只有 `npc install` 安装的服务支持。
- 不希望被远程更新时，启动时加 `-disable_remote_update`。官方 Docker 镜像默认已加。

## 查看日志

| 运行方式 | 默认输出 |
| --- | --- |
| 命令行运行 | 终端 |
| 系统服务（Linux、macOS） | `/var/log/npc.log` |
| 系统服务（Windows） | npc.exe 所在目录下的 `npc.log` |
| OpenWrt | 不输出 |

默认每个文件最大 5 MB，保留 10 个文件、7 天。

需要改变时，把参数加在启动命令或 `npc install` 后面，例如：

```bash
sudo npc install -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" \
  -log=both -log_level=debug
```

全部日志参数见 [npc 命令行参数](/reference/npc-cli.md)。

## 断线重连

npc 默认在断线后自动重连，每次都重新解析服务端域名。有多条路径时，先尝试上次成功的路径。

## 判断客户端是否在线

系统服务状态正常只说明进程在运行，不代表已连上服务端。请看网页“客户端”列表的状态，或者运行：

```bash
sudo npc restart
sudo tail -n 50 /var/log/npc.log
```

连不上时先检查服务端端口是否放行、`vkey` 是否正确、连接方式是否被服务端接受，详见[排错](/guide/server/troubleshooting.md)。
