# NPS 内网穿透 (全修)

[![GitHub Stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub Forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

> 在 [GitHub](https://github.com/djylb/nps) 点击右上角 ⭐ Star 以支持我在空闲时间继续开发

> 由于 GitHub 限制浏览器语言为中文（Accept-Language=zh-CN) 访问 *.githubusercontent.com ，图标可能无法正常显示。

- [English](https://github.com/djylb/nps/blob/master/README_en.md)

---

## 简介

NPS 是一款轻量高效的内网穿透代理服务器，支持多种协议（TCP、UDP、HTTP、HTTPS、SOCKS5 等）转发。它提供直观的 Web 管理界面，使得内网资源能安全、便捷地在外网访问，同时满足多种复杂场景的需求。

由于[NPS](https://github.com/ehang-io/nps)停更已久，本仓库整合社区更新二次开发而来。

- **提问前请先查阅：**  [文档](https://d-jy.net/docs/nps/) 与 [Issues](https://github.com/djylb/nps/issues)
- **欢迎参与：**  提交 PR、反馈问题或建议，共同推动项目发展。
- **讨论交流：**  加入 [Telegram 交流群](https://t.me/npsdev) 与其他用户交流经验。
- **Android：**  [djylb/npsclient](https://github.com/djylb/npsclient)
- **OpenWrt：**  [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)
- **Mirror：**  [djylb/nps-mirror](https://github.com/djylb/nps-mirror)

---

## 主要特性

- **多协议支持**  
  TCP/UDP 转发、HTTP/HTTPS 转发、HTTP/SOCKS5 代理、P2P 模式、Proxy Protocol支持、HTTP/3支持等，满足各种内网访问场景。

- **跨平台部署**  
  支持 Linux、Windows 等主流平台，可轻松安装为系统服务。

- **Web 管理界面**  
  实时监控流量、连接情况以及客户端状态，操作简单直观。

- **安全与扩展**  
  内置加密传输、流量限制、到期限制、证书管理续签等多重功能，保障数据安全。

- **多连接协议**  
  支持 TCP、KCP、TLS、QUIC、WS、WSS 协议连接服务器。

---

## 安装与使用

更多详细配置请参考 [文档](https://d-jy.net/docs/nps/)（部分内容可能未更新）。

### [Android](https://github.com/djylb/npsclient) | [OpenWrt](https://github.com/djylb/nps-openwrt)

### Docker 部署

***DockerHub***： [NPS](https://hub.docker.com/r/duan2001/nps) [NPC](https://hub.docker.com/r/duan2001/npc)

***GHCR***： [NPS](https://github.com/djylb/nps/pkgs/container/nps) [NPC](https://github.com/djylb/nps/pkgs/container/npc)

> 有真实IP获取需求可配合 [mmproxy](https://github.com/djylb/mmproxy-docker) 使用。例如：SSH

#### NPS 服务端
```bash
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v $(pwd)/conf:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

#### NPC 客户端
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -server=xxx:123,yyy:456 -vkey=key1,key2 -type=tls,tcp -log=off
```

### 服务端安装

#### Linux
```bash
# 安装（默认配置路径：/etc/nps/；二进制文件路径：/usr/bin/）
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
nps install
nps start|stop|restart|uninstall

# 更新
nps update && nps restart
```

#### Windows
> Windows 7 用户请使用 old 结尾版本 [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_server_old.tar.gz)
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
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls -log=off
npc start|stop|restart|uninstall

# 更新
npc update && npc restart
```

#### Windows
> Windows 7 用户请使用 old 结尾版本 [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_client_old.tar.gz)
```powershell
.\npc.exe install -server="xxx:123,yyy:456" -vkey="xxx,yyy" -type="tls,tcp" -log="off"
.\npc.exe start|stop|restart|uninstall

# 更新
.\npc.exe stop
.\npc-update.exe update
.\npc.exe start
```

> **提示：** 客户端支持同时连接多个服务器，示例：  
> `npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls`  
> 这里 `xxx:123` 使用 tcp, `yyy:456` 和 `zzz:789` 使用tls

> 如需连接旧版本服务器请添加 `-proto_version=0`

---

## 更新日志

### DEV

- **Main**
  - 待定，优先修BUG，新功能随缘更新
  - 增强P2P打洞能力（不再兼容旧版） [#198](https://github.com/djylb/nps/pull/198) (感谢[cr4n5](https://github.com/cr4n5))
  - 混合代理添加白名单功能 [205](https://github.com/djylb/nps/pull/205) (感谢[Yourdaylight](https://github.com/Yourdaylight))
  - 混合代理同时支持黑白名单功能

### Stable

- **v0.33.14 (2026-01-22)**
  - 调整限速逻辑
  - 修复pprof配置不生效 [#199](https://github.com/djylb/nps/issues/199)
  - 优化程序稳定性 [#201](https://github.com/djylb/nps/pull/201)
  - 优化高压转发场景
  - 更新相关依赖包

- **v0.33.13 (2026-01-05)**
  - 优化程序稳定性
  - 优化吞吐性能
  - 更新相关依赖包

- **v0.33.12 (2025-12-13)**
  - 域名转发支持强制自动证书
  - 更新相关依赖包

- **v0.33.11 (2025-11-06)**
  - 域名转发支持仅监听HTTPS
  - 更新相关依赖包

- **v0.33.10 (2025-11-01)**
  - 支持npc配置文件配置代理协议
  - 更新相关依赖包

- **v0.33.9 (2025-10-03)**
  - 优化拥塞控制逻辑
  - 重写Socks5逻辑
  - 移除仪表盘无效内容
  - 优化HTTPS流量统计
  - 配置文件添加默认值
  - 优化连接释放
  - 修复Win环境下连接数统计
  - 支持点击关闭弹窗
  - 更新相关依赖包

- **v0.33.8 (2025-09-20)**
  - 兼容旧版本`nps.conf`解析
  - 优化命令行传参处理逻辑
  - 调整默认配置
  - 更新相关依赖包

- **v0.33.7 (2025-09-17)**
  - 客户端允许配置空SNI
  - 支持单独定制连接命令提示
  - HTTP/3端口支持复用

- **v0.33.6 (2025-09-16)**
  - 支持隧道目标指向桥接
  - 域名转发支持TLS终止
  - 调整页面翻译说明
  - 支持指定客户端连接Host/SNI

- **v0.33.5 (2025-09-11)**
  - 允许WS向浏览器返回错误
  - 混合代理禁用代理协议
  - 添加隧道嵌套转发支持 [使用说明](https://d-jy.net/docs/nps/#/feature?id=%e5%b5%8c%e5%a5%97%e8%bd%ac%e5%8f%91%e6%94%af%e6%8c%81)
  - 域名转发请求头默认插入`X-Forwarded-Host`
  - 添加头部删除支持
  - 更新相关依赖包

- **v0.33.4 (2025-09-10)**
  - 添加客户端延迟检测API
  - 优化域名转发内存使用
  - API接口添加校验检查
  - 允许关闭客户端自动重连
  - 客户端列表添加延迟检测（点击在线按钮）
  - 更新相关依赖包

- **v0.33.3 (2025-09-08)**
  - 调整P2P重试逻辑
  - 支持WS获取真实IP
  - 更新相关依赖包
  - 升级GO至1.25

- **v0.33.2 (2025-09-05)**
  - 修复深色背景下分页按钮为浅色背景问题 [#149](https://github.com/djylb/nps/pull/149)
  - 添加P2P打洞重试次数限制
  - 更新相关依赖包

- **v0.33.1 (2025-08-30)**
  - 添加UDP不兼容提示
  - 添加Android编译
  - 调整默认兼容版本（需兼容旧版时配置`secure_mode=false`）
  - 更新相关依赖包

- **v0.33.0 (2025-08-28)**
  - 优化退出逻辑
  - P2P绑定具体地址
  - 允许配置P2P连接模式
  - 支持使用环境变量代理连接
  - UDP分包转发（UDP不兼容旧版客户端）
  - 支持 0 长度包转发
  - 调整日志输出
  - 更新相关依赖包

- **v0.32.10 (2025-08-26)**
  - 添加调试日志输出
  - 优化P2P连接释放
  - 修复深色主题下浅色加载背景 [#142](https://github.com/djylb/nps/issues/142)
  - 避免深色主题切换页面闪烁
  - 自动跟随浏览器默认主题
  - 允许配置KeepAlive间隔

- **v0.32.9 (2025-08-25)**
  - 避免下载过程中被阻断导致绕过换源 [#139](https://github.com/djylb/nps/issues/139)
  - 优化P2P断连检测逻辑
  - 调整重复隧道检查逻辑
  - 更新相关依赖包

- **v0.32.8 (2025-08-21)**
  - 支持客户端通过配置文件覆盖已有临时隧道
  - 添加仅转发相关配置说明
  - 调整客户端连接处理逻辑
  - 更新相关依赖包

- **v0.32.7 (2025-08-18)**
  - 避免状态获取失败引起崩溃 [#133](https://github.com/djylb/nps/issues/133)
  - 优化状态获取性能
  - 修复IPv6地址显示 [#135](https://github.com/djylb/nps/issues/135)

- **v0.32.6 (2025-08-18)**
  - 重构状态获取函数 [#134](https://github.com/djylb/nps/issues/134)

- **v0.32.5 (2025-08-17)**
  - 避免状态获取失败引起崩溃 [#133](https://github.com/djylb/nps/issues/133)

- **v0.32.4 (2025-08-12)**
  - 避免注释影响配置文件解析
  - 修复时区不生效

- **v0.32.3 (2025-08-12)**
  - 支持使用代理更新 [#128](https://github.com/djylb/nps/issues/128)
  - 调整日志输出
  - 优化ACK检查逻辑
  - 添加时区配置支持
  - 内置tzdata数据库
  - P2P多隧道时复用同一底层连接
  - 避免QUIC提前关闭

- **v0.32.2 (2025-08-11)**
  - 整理仓库代码
  - 安装脚本添加超时切换
  - 安装脚本减少环境依赖
  - 优化客户端体积大小
  - 更新获取版本失败时直接切换CDN下载

- **v0.32.1 (2025-08-10)**
  - 优化连接释放
  - P2P、私密代理支持代理到本地
  - 优化断线重连逻辑
  - 添加允许用户使用本地代理配置

- **v0.32.0 (2025-08-10)**
  - 使用UUID标记每个客户端实例
  - 私密代理支持连接复用
  - 修复文件隧道访问
  - 管理页面隧道添加点击复制
  - 允许配置多条文件隧道
  - 添加禁用P2P连接选项
  - 更新相关依赖包

- **v0.31.1 (2025-08-09)**
  - 更激进地连接状态检查策略
  - 避免系统时钟差异导致在线时间不正确
  - 避免系统时钟差异导致登录失败
  - 调整超时错误判断逻辑

- **v0.31.0 (2025-08-08)**
  - 优化连接复用
  - 支持原生QUIC特性

- **v0.30.6 (2025-08-07)**
  - P2P无感切换连接
  - NPC同时支持命令启动和传入配置文件启动
  - 自动转换全角冒号为半角冒号
  - 添加允许私密代理客户端连接任意地址配置
  - 添加是否允许用户使用vkey登录管理配置
  - 添加默认预读取最大限制
  - 添加是否允许P2P回落私密代理选项
  - 符合RFC1928规范要求
  - 允许Socks5的UDP连接端口号改变
  - 优化游戏场景隧道性能
  - 修复开启压缩或加密时代理到服务器本地失败
  - 文件隧道允许配置只读
  - 支持通过文件配置混合代理
  - 文件隧道支持配置为其他隧道的目的地址（不再兼容旧版）
  - 添加客户端节点数量统计（旧版客户端相同vkey每个IP地址最多连1个）
  - 管理页面添加文件隧道连接地址
  - 更新相关依赖包

- **v0.30.5 (2025-08-04)**
  - 优化小包转发性能
  - 优化P2P访问时延
  - 限制P2P访问隧道类型
  - 调整Socks5超时时间

- **v0.30.4 (2025-08-03)**
  - P2P 支持双栈连接
  - 整理优化代码
  - 修正拼写错误
  - P2P 在双方支持时自动使用 QUIC 建立连接
  - P2P 支持原生 QUIC 特性
  - 修复 NPC 的 status 查询功能
  - 优化超时释放逻辑
  - 增强断连状态检测
  - 私密代理支持UDP
  - 调整管理页面显示

- **v0.30.3 (2025-08-01)**
  - 支持客户端主备 / 轮询 / 随机模式选择
  - 提升并发安全性
  - 修复一些资源泄露问题
  - 优化连接池获取策略
  - 调整连接状态判断逻辑
  - 优化 Socks5 处理逻辑
  - P2P 支持多客户端连接
  - 更新相关依赖包

- **v0.30.2 (2025-07-30)**
  - 优化资源释放
  - 优化P2P连接
  - 更新相关依赖包

- **v0.30.1 (2025-07-29)**
  - 默认关闭NTP校准
  - 添加NTP查询最小间隔配置

- **v0.30.0 (2025-07-29)**
  - 优化流量特征
  - 优化依赖导入
  - 调整登录时间限制
  - 添加时间校准
  - 添加下载超时切换
  - 更新添加哈希校验
  - 添加NTP服务器配置
  - 调整日志输出
  - 更新相关依赖包

- **v0.29.38 (2025-07-18)**
  - 添加缓存避免多次写入
  - 优化网络包传输

- **v0.29.37 (2025-07-17)**
  - 修复非RSA证书导致解密错误 [#109](https://github.com/djylb/nps/issues/109)
  - 调整延迟检查逻辑
  - 更新相关依赖包

- **v0.29.36 (2025-07-14)**
  - 调整风控处理逻辑
  - 调整登录页面逻辑
  - 添加总是返回错误页面选项
  - 更新相关依赖包

- **v0.29.35 (2025-07-13)**
  - 列表添加流量和时间限制相关列
  - 域名转发重定向支持变量
  - 客户端支持更多域名转发配置
  - 更新相关依赖包

- **v0.29.34 (2025-07-11)**
  - 域名转发支持重定向
  - 调整管理页面显示
  - 更新相关依赖包

- **v0.29.33 (2025-07-07)**
  - 修复连接数统计

- **v0.29.32 (2025-07-04)**
  - 清理旧`vkey`索引
  - 启动命令添加引号
  - 更新相关依赖包

- **v0.29.31 (2025-07-02)**
  - 优化域名转发并发性能
  - 更新相关依赖包

- **v0.29.30 (2025-06-29)**
  - 支持同时配置密码和TOTP
  - 支持客户端用户配置TOTP
  - TOTP支持写在密码后或验证码后
  - 限制高频登录请求
  - 登录添加PoW验证
  - 调整IP封禁时间
  - 自动判断风险选择验证方式
  - `-gen2fa`添加二维码生成
  - 客户端列表TOTP支持生成二维码

- **v0.29.29 (2025-06-28)**
  - 添加返回头修改 [具体说明](https://d-jy.net/docs/nps/#/feature?id=%e8%87%aa%e5%ae%9a%e4%b9%89%e5%93%8d%e5%ba%94-header)
  - 更新相关依赖包
  - 优化页面显示
  - 避免替换转义

- **v0.29.28 (2025-06-26)**
  - 域名转发支持 HTTP/3
  - 更新相关依赖包
  - 避免插入Connection: close [#102](https://github.com/djylb/nps/issues/102)

- **v0.29.27 (2025-06-25)**
  - 调整UDP的Proxy Protocol处理逻辑
  - 允许bridge端口全部为0 [#100](https://github.com/djylb/nps/issues/100)

- **v0.29.26 (2025-06-25)**
  - 添加QUIC连接方式
  - 弃用`bridge_type`和`bridge_port`配置，通过指定端口号是否为0控制开关
  - 更新相关依赖包
  - 统一日志输出
  - 优化客户端连接

- **v0.29.25 (2025-06-24)**
  - 优化UDP流量统计
  - 优化流量限制机制
  - 优化安装逻辑
  - 重构UDP隧道
  - 添加缓冲队列
  - 优化内存拷贝
  - 优化资源释放

- **v0.29.24 (2025-06-23)**
  - 自动生成唯一标识密钥
  - UDP添加Proxy Protocol支持 [#99](https://github.com/djylb/nps/issues/99)

- **v0.29.23 (2025-06-20)**
  - Docker添加CA证书
  - 更新相关依赖
  - 允许自定义页面显示

- **v0.29.22 (2025-06-19)**
  - 修复禁用客户端失效 [#97](https://github.com/djylb/nps/issues/97)
  - 虚拟客户端支持禁用

- **v0.29.21 (2025-06-11)**
  - 增强 WebSocket 连接稳定性
  - 调整界面翻译
  - 域名转发完整支持 Proxy Protocol
  - 更新时自动创建不存在的文件夹

- **v0.29.20 (2025-06-10)**
  - 头部替换支持变量替换 [具体说明](https://d-jy.net/docs/nps/#/feature?id=%e8%87%aa%e5%ae%9a%e4%b9%89%e8%af%b7%e6%b1%82-header)
  - 避免重复添加 X-Forwarded-For
  - 修复上游错误导致发布失败 [#93](https://github.com/djylb/nps/issues/93)

- **v0.29.19 (2025-06-09)**
  - 默认添加 X-Forwarded-Proto 请求头
  - 后端验证用户名密码表单非空
  - 管理页面支持 X-NPS-Http-Only 头

- **v0.29.18 (2025-06-08)**
  - 调整字体 [#90](https://github.com/djylb/nps/pull/90) (感谢[yhl452493373](https://github.com/yhl452493373))
  - 登录表单非空时允许提交 [#89](https://github.com/djylb/nps/issues/89)
  - 更新相关依赖

- **v0.29.17 (2025-06-07)**
  - NPC配置文件支持兼容模式
  - 调整页面显示 [#87](https://github.com/djylb/nps/pull/87) (感谢[yhl452493373](https://github.com/yhl452493373))
  - 添加连接数、流量统计
  - 清空统计信息时保持隧道连接
  - 添加隧道级别连接数统计
  - 增强流量统计准确性
  - 解密失败时返回证书公钥

- **v0.29.16 (2025-06-06)**
  - 调整登录验证码逻辑
  - 管理页面添加信任代理服务器选项
  - 域名转发添加兼容模式（开启后可缓解 521 错误返回）
  - 修复代理到服务器本地
  - 域名转发添加 CONNECT 支持

- **v0.29.15 (2025-06-05)**
  - 调整相对路径处理逻辑 [#82](https://github.com/djylb/nps/issues/82)
  - 记录登录日志 [#81](https://github.com/djylb/nps/issues/81)
  - 添加获取证书公钥接口
  - 修复安装替换文件逻辑 [#83](https://github.com/djylb/nps/issues/83)

- **v0.29.14 (2025-06-04)**
  - 添加备用CDN下载更新
  - 新增编译架构
  - 调整更新替换文件逻辑

- **v0.29.13 (2025-06-03)**
  - 添加镜像下载更新文件 [nps-mirror](https://github.com/djylb/nps-mirror)
  - 域名转发支持客户端配置文件配置证书等内容
  - 调整域名转发管理列表显示

- **v0.29.12 (2025-05-28)**
  - 修复指定路径后验证码不显示
  - 修复域名转发 521 错误返回

- **v0.29.11 (2025-05-27)**
  - 避免每次TLS重新握手
  - 支持自动申请SSL证书 [#54](https://github.com/djylb/nps/issues/54)
  - 调整域名转发匹配逻辑
  - 调整掉线检测逻辑

- **v0.29.10 (2025-05-26)**
  - 修复域名转发重复判断逻辑

- **v0.29.9 (2025-05-25)**
  - 重写数据库，性能优化至 O(1) 级别
  - 重写HTTPS模块，支持高并发
  - 重写证书缓存，支持懒加载、LRU

- **v0.29.8 (2025-05-25)**
  - 域名转发支持路径重写
  - 健康检查支持HTTPS

- **v0.29.7 (2025-05-24)**
  - 允许多客户端使用相同密钥连接 （仅最后连接的客户端生效）
  - 支持客户端断连后自动切换
  - 优化服务端资源释放

- **v0.29.6 (2025-05-24)**
  - 重写P2P服务端代码
  - 优化服务端性能
  - 优化局域网下P2P联通能力
  - 优化P2P断线重连
  - 减少默认P2P超时检测时间

- **v0.29.5 (2025-05-23)**
  - 清空客户端流量时同时清空对应隧道流量
  - 仪表盘显示内容自动刷新
  - 优化客户端性能
  - 文件模式支持WebDav（仅允许通过npc配置文件设置）
  - 优化配置文件读取

- **v0.29.4 (2025-05-20)**
  - 登录失败时不刷新页面
  - 登录失败时自动刷新验证码图片

- **v0.29.3 (2025-05-20)**
  - 客户端参数缺失端口时使用默认端口
  - 调整仪表盘显示内容 [#16](https://github.com/djylb/nps/issues/16)
  - 修复英文翻译错误

- **v0.29.2 (2025-05-19)**
  - 退出登录按钮同时清空页面缓存
  - 登录注册页面密码使用公私钥加密传输
  - 修改注册页面配色

- **v0.29.1 (2025-05-19)**
  - 修复文字错误
  - 完善页面翻译
  - 调整混合代理页面状态为按钮
  - 网页提示命令自动选择协议
  - 调整模式显示
  - 修复XSS漏洞
  - 对齐网页文字和图标
  - 优化移动设备页面显示
  - TCP列表状态图标可点击访问网页
  - 调整列表显示题目（在右上角配置）
  - 隧道页面点击客户端ID复制vkey
    
- **v0.29.0 (2025-05-19)**
  - 合并HTTP代理和Socks5代理为混合代理
  - 美化Web界面 （参考 [#76](https://github.com/djylb/nps/issues/76) 感谢 [arch3rPro](https://github.com/arch3rPro)）
  - 支持明暗主题切换
  - 修复注册验证码校验
  - 优化TCP释放逻辑
  - 优化域名转发请求头插入逻辑
  - 登录支持TOTP验证登录 (nps.conf 新增 `totp_secret` 配置)
  - nps、npc 支持 `-gen2fa` 参数生成两步验证密钥
  - nps、npc 支持 `-get2fa=<密钥>` 参数获取登录验证码 （也可以用APP管理）
  - 记录客户端连接桥接类型
  - 调整管理页面展示内容
  - 添加客户端流量重置按钮
  - 列表页面允许直接点击清空元素
  - 混合代理允许直接点击状态控制开关
  - 调整页面配色

- **v0.28.3 (2025-05-16)**
  - 优化并发读写
  - 延长时间校验窗口
  - 完善服务端日志输出
  - 重写HTTP正向代理
  - 支持同一端口监听HTTP/Socks5代理 [#56](https://github.com/djylb/nps/issues/56)

- **v0.28.2 (2025-05-15)**
  - 修复HTTP正向代理 [#75](https://github.com/djylb/nps/issues/75)

- **v0.28.1 (2025-05-15)**
  - 客户端加密校验证书

- **v0.28.0 (2025-05-15)**
  - TLS、WSS 默认校验证书 （支持自签名证书）
  - 防重放攻击、中间人攻击等 （建议使用TLS、WSS）
  - 优化域名解析速度

- **v0.27.0 (2025-05-14)**
  - 启用隧道添加端口检查 [#74](https://github.com/djylb/nps/issues/74)
  - NPS添加`secure_mode`选项，开启后不再支持旧版客户端
  - NPC添加`proto_version`选项，如需连接旧版服务器需要配置`-proto_version=0`
  - 重写客户端连接协议，防止探测、重放攻击等 （系统时间需要配置正确，依赖系统时间）
  - 更换哈希算法
  - 增加记录客户端本地IP地址
  - 修复最快IP解析
  - HTTP正向代理使用相对路径 [#75](https://github.com/djylb/nps/issues/75)
  - 新增WS、WSS方式连接服务端 [#71](https://github.com/djylb/nps/issues/71)
  - 客户端服务器双向认证
  - 允许独立配置连接协议
  - 网页添加命令行提示

- **v0.26.56 (2025-05-07)**
  - 日志相对路径使用配置路径
  - 日志添加`log_color`选项
  - 调整关闭日志优先级
  - 减少登录错误弹窗等待时间

- **v0.26.55 (2025-05-05)**
  - 文档添加自动翻译
  - 重写日志输出模块
  - 修复日志轮换功能
  - 修复日志权限问题 [#70](https://github.com/djylb/nps/issues/70)
  - 更新上游依赖

- **v0.26.54 (2025-05-02)**
  - 更新文档说明
  - 添加弹窗翻译
  - 优化浏览器语言检测
  - 优化操作逻辑减少操作步骤 [#69](https://github.com/djylb/nps/issues/69)

- **v0.26.53 (2025-04-25)**
  - P2P同时转发TCP和UDP端口

- **v0.26.52 (2025-04-23)**
  - 优化服务器域名解析逻辑
  - 修复同时启用TCP和KCP时客户端不同步问题

- **v0.26.51 (2025-04-22)**
  - 优化P2P打洞算法
  - 使用迭代法解析服务器域名
  - 优选最快IP连接服务器
  - 允许同时监听KCP端口

- **v0.26.50 (2025-04-19)**
  - 优化P2P探测和连接速度
  - 隧道编辑页面支持保存为新配置 [#8](https://github.com/djylb/nps/issues/8)
  - 调整页面显示，添加排序支持

- **v0.26.49 (2025-04-18)**
  - vkey添加点击复制
  - 重写透明代理逻辑 [#59](https://github.com/djylb/nps/issues/59)
  - 修复linux、darwin、freebsd的透明代理

- **v0.26.48 (2025-04-17)**
  - 添加点击自动复制命令行 [#62](https://github.com/djylb/nps/issues/62)
  - 密码认证配置内容忽略空行
  - 修复NPS的IPv6自动识别
  - 修复管理页面显示
  - 隧道列表支持端口号排序
  - 重写客户端TLS功能，支持使用type传入tls （已弃用tls_enable）
  - 重写服务端TLS功能，支持TLS端口复用 （已弃用tls_enable）
  - 客户端支持连接多个服务器 [#9](https://github.com/djylb/nps/issues/9)
  - 更新证书随机生成

- **v0.26.47 (2025-04-14)** 
  - 优化P2P处理逻辑
  - 服务端支持配置`p2p_ip=0.0.0.0`来自动识别IP地址(IPv4/IPv6由`dns_server`配置决定)
  - 服务端支持配置`p2p_ip=::`来强制自动识别使用IPv6地址
  - 修复P2P的IPv6支持
  - NPC自动选择IPv4/IPv6进行P2P连接
  - **新增** 支持单条隧道独立配置密码认证

- **v0.26.46 (2025-04-14)** 
  - 调整日志输出等级
  - 优化写入性能
  - 修复端口复用时连接泄露和并发冲突
  - 清理代码更新相关依赖
  - 新增OpenWRT仓库 [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)
  - 修复拼写错误
  - 自动更新[Android](https://github.com/djylb/npsclient)和[OpenWrt](https://github.com/djylb/nps-openwrt)仓库
  - 自动识别服务器IP [#59](https://github.com/djylb/nps/issues/59)
  - P2P支持IPv6（需要纯IPv6网络环境）

- **v0.26.45 (2025-04-09)** 
  - 搜索功能匹配不限制大小写
  - 修复HTTP代理认证头 [#55](https://github.com/djylb/nps/issues/55)
  - 添加编译架构 [#53](https://github.com/djylb/nps/issues/53)
  - 增加自定义DNS支持非标准系统
  - 新增安卓客户端 [#53](https://github.com/djylb/nps/issues/53) [djylb/npsclient](https://github.com/djylb/npsclient)
  - 美化下拉框样式，使用标准JSON保存数据 [#51](https://github.com/djylb/nps/pull/51) (感谢[yhl452493373](https://github.com/yhl452493373))

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

更多历史更新记录请参阅项目 [Releases](https://github.com/djylb/nps/releases)
