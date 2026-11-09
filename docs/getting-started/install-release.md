# 发布包安装

官方二进制包适用于 Windows、Linux、macOS、FreeBSD 等平台，见[最新发布页面](https://github.com/djylb/nps/releases/latest)。Windows 见 [Windows 安装](/getting-started/install-windows.md)。

## Linux
Linux 推荐使用 Docker 运行。

### x86/x64
- [64 位（Server）](https://github.com/djylb/nps/releases/latest/download/linux_amd64_server.tar.gz)
- [64 位（Client）](https://github.com/djylb/nps/releases/latest/download/linux_amd64_client.tar.gz)
- [32 位（Server）](https://github.com/djylb/nps/releases/latest/download/linux_386_server.tar.gz)
- [32 位（Client）](https://github.com/djylb/nps/releases/latest/download/linux_386_client.tar.gz)

### ARM
- [ARM64（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm64_server.tar.gz)
- [ARM64（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm64_client.tar.gz)
- [ARMv5（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_server.tar.gz)
- [ARMv5（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_client.tar.gz)
- [ARMv6（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_server.tar.gz)
- [ARMv6（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_client.tar.gz)
- [ARMv7（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_server.tar.gz)
- [ARMv7（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_client.tar.gz)

解压后进入文件夹：
```bash
# NPS Server
./nps install
nps start|stop|restart|uninstall

# Support custom config path
./nps -conf_path="/app/nps"
./nps install -conf_path="/app/nps"

# Update
nps update

# NPC Client
./npc install
/usr/bin/npc install -launch="npc://VKEY@xxx:123?type=tls" -log=off
npc start|stop|restart|uninstall

# Update
npc update
```


## macOS
- [Intel（Server）](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_server.tar.gz)
- [Intel（Client）](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_client.tar.gz)
- [Apple Silicon（Server）](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_server.tar.gz)
- [Apple Silicon（Client）](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_client.tar.gz)

解压后进入文件夹：
```bash
# NPS Server
./nps install
nps start|stop|restart|uninstall

# Support custom config path
./nps -conf_path="/app/nps"
./nps install -conf_path="/app/nps"

# Update
nps update

# NPC Client
./npc install
/usr/bin/npc install -launch="npc://VKEY@xxx:123?type=tls" -log=off
npc start|stop|restart|uninstall

# Update
npc update
```


## FreeBSD
- [AMD64（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_server.tar.gz)
- [AMD64（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_client.tar.gz)
- [386（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_386_server.tar.gz)
- [386（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_386_client.tar.gz)
- [ARM（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_server.tar.gz)
- [ARM（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_client.tar.gz)

## 强制更新

服务端和客户端均支持以下命令，已是最新版本时重新下载安装最新发布包：

```bash
nps update-force
npc update-force
```

Windows 使用 `.\nps.exe update-force` 或 `.\npc.exe update-force`，请以管理员身份运行；Linux 系统安装通常需要 root 权限。无论当前版本是否更新都安装最新发布版本，包括同版本重新安装、从较新版本退回和重试失败过的版本，但不会跨过发布清单禁止的升级。仍保留原有的包摘要校验、下载程序可运行检查及发布标签与程序版本一致性检查；镜像回落沿用原更新流程。服务重启、网页及 Geo 数据更新行为与 `update` 相同，配置和实例密钥保持不变。
