# 功能清单

各项功能的参考说明。想一步步操作，看对应的场景教程；这里只讲“它是什么、有哪些取值”。

| 分类 | 内容 |
| --- | --- |
| [传输与连接](/reference/features-transport.md) | 连接方式、混合端口、压缩与加密、嵌套转发、断线判定 |
| [站点与 HTTP](/reference/features-http.md) | 证书、TLS、请求头与响应头、重定向、CORS、路由与重写、站点保护、负载均衡 |
| [限制与统计](/reference/features-limits.md) | 流量、带宽、时间、连接数、隧道数限制 |
| [日志与调试](/guide/server/logging.md) | 日志、pprof |
| [访问控制](/guide/server/access-control.md) | 登录来源、IP 黑白名单、Geo 数据、IP 登记、端口白名单 |
| [健康检查](/guide/tunnels/health-check.md) | 由客户端探测目标，故障目标不再分配连接 |
| [Webhook](/guide/server/webhook.md) | 事件和定时全量上报 |
| [备份与恢复](/guide/server/backup.md) | 迁移、导出导入配置 |
| [启动链接与多条连接](/guide/client/launch.md) | 一个 npc 同时维护多条连接 |
| [npc 配置文件](/reference/npc-config.md) | 配置文件格式与环境变量模板 |
| 同一密钥多个客户端 | 多个 npc 使用同一个 `vkey` 时由 `bridge_select_mode` 决定选哪个，见[服务端配置](/reference/server-config.md) |
| [Web API](/reference/web-api.md)、[管理页面说明](/reference/web-ui.md) | 管理界面的接口与字段 |
| [npc 动态库](/reference/npc-sdk.md) | 把 npc 嵌入自己的程序 |
