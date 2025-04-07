# NPS 内网穿透

[![GitHub stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

- [English](https://github.com/djylb/nps/blob/master/README_en.md)

---

## 简介

NPS 是一款轻量高效的内网穿透代理服务器，支持多种协议（TCP、UDP、HTTP、SOCKS5 等）转发。它提供直观的 Web 管理界面，使得内网资源能安全、便捷地在外网访问，同时满足多种复杂场景的需求。

由于[NPS](https://github.com/ehang-io/nps)停更已久，本仓库基于 nps 0.26 整合社区更新二次开发而来。

- **提问前请先查阅：**  [文档](https://d-jy.net/docs/nps/) 与 [Issues](https://github.com/djylb/nps/issues)
- **欢迎参与：**  提交 PR、反馈问题或建议，共同推动项目发展。
- **讨论交流：**  加入 [Telegram 交流群](https://t.me/npsdev) 与其他用户交流经验。

---

## 主要特性

- **多协议支持**  
  TCP/UDP 转发、HTTP/SOCKS5 代理、P2P 模式等，满足各种内网访问场景。

- **跨平台部署**  
  支持 Linux、Windows 等主流平台，可轻松安装为系统服务。

- **Web 管理界面**  
  实时监控流量、连接情况以及客户端状态，操作简单直观。

- **安全与扩展**  
  内置加密传输、流量限制、证书管理等多重功能，保障数据安全。

---

## 安装与使用

更多详细配置请参考 [文档](https://d-jy.net/docs/nps/)（部分内容可能未更新）。

### Docker 部署

***DockerHub***： [NPS](https://hub.docker.com/r/duan2001/nps) [NPC](https://hub.docker.com/r/duan2001/npc)

***GHCR***： [NPS](https://github.com/djylb/nps/pkgs/container/nps) [NPC](https://github.com/djylb/nps/pkgs/container/npc)

#### NPS 服务端
```bash
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v $(pwd)/conf:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

#### NPC 客户端
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -server=xxx:123 -vkey=key1,key2 -tls_enable=true -log=off
```

### 服务端安装

#### Linux
```bash
# 安装（默认配置路径：/etc/nps/；二进制文件路径：/usr/bin/）
./nps install
nps start|stop|restart|uninstall

# 更新
nps stop
nps-update update
nps start
```

#### Windows
```powershell
.\nps.exe install
.\nps.exe start|stop|restart|uninstall

# 更新
.\nps.exe stop
.\nps-update.exe update
.\nps.exe start
```

### 客户端安装

#### Linux
```bash
./npc install
/usr/bin/npc install -server=xxx:123 -vkey=xxx -type=tcp -tls_enable=true -log=off
npc start|stop|restart|uninstall

# 更新
npc stop
/usr/bin/npc-update update
npc start
```

#### Windows
```powershell
.\npc.exe install -server="xxx:123" -vkey="xxx" -type="tcp" -tls_enable="true" -log="off"
.\npc.exe start|stop|restart|uninstall

# 更新
.\npc.exe stop
.\npc-update.exe update
.\npc.exe start
```

> **提示：** 客户端支持同时传入多个隧道 ID，示例：  
> `npc -server=xxx:8024 -vkey=key1,key2`

---

## 更新日志

### DEV

- **Main** 
  - 待定，优先修BUG，新功能随缘更新
  - 搜索功能匹配不限制大小写
  - 修复HTTP代理认证头
  - 添加编译架构

### Stable

- **v0.26.44 (2025-03-26)** 
  - 修复客户端超过1000不显示问题
  - **增强** 隧道添加支持搜索客户端

- **v0.26.43 (2025-03-24)** 
  - 修复客户端隧道编辑按钮缺失
  - 隧道列表隐藏无用信息
  - **新增** 域名转发隧道支持暂停
  - **增强** 域名转发防止扫描探测

- **v0.26.42 (2025-03-23)** 
  - 修复管理页面表单Id标签重复
  - 修复隧道页面不显示
  - 整理nps.conf文件

- **v0.26.41 (2025-03-22)** 
  - Docker自动创建NPS默认配置 **（一定要记得改配置）**
  - 固定管理页面左侧菜单、顶部标题、底部footer [#49](https://github.com/djylb/nps/pull/49)
  - 优化运行速度，减少资源占用
  - 修复单条隧道流量统计 [#30](https://github.com/djylb/nps/issues/30)
  - 增强流量统计颗粒度 **（注意：客户端流量是隧道流量出入总和的两倍）**
  - 修复文件模式访问
  - 调整管理页面文件模式显示
  - **新增** 管理页面表单选项持久化储存
  - **新增** 表单添加显示全部选项
  - **新增** 单条隧道支持限制流量和时间
  - 调整隧道页面显示
  - 修复NPC客户端NAT检测  

- **v0.26.40 (2025-03-21)** 
  - 前端页面美化 [#47](https://github.com/djylb/nps/pull/47)
  - 增加docker支持架构，添加shell支持
  - 向NPS的docker镜像添加tzdata软件包支持时区配置 [#45](https://github.com/djylb/nps/issues/45)
  - 私密代理支持通过TLS连接 [#37](https://github.com/djylb/nps/issues/37)
  - docker添加主线分支发布
  - 修复连接数统计问题 [#48](https://github.com/djylb/nps/issues/48)

- **v0.26.39 (2025-03-16)** 
  - 切换包至本仓库
  - 更新说明文档至当前版本
  - 更新管理页面帮助
  - 优化 nps.conf 配置文件
  - 更新 SDK 组件

- **v0.26.38 (2025-03-14)** 
  - 域名转发支持HTTP/2
  - 当配置请求域名修时同时修改Origin头避免后端监测
  - 调整域名编辑页面逻辑
  - 更新相关依赖，修复CVE-2025-22870
  - 使用 [XTLS/go-win7](https://github.com/XTLS/go-win7) 编译旧版代码支持Win7
  - 整理仓库代码
  - 优化域名查找算法

更多历史更新记录请参阅项目 [Releases](https://github.com/djylb/nps/releases)。
