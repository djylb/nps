# 服务端

nps 装好之后，日常要做的事都在这一组页面里：

- [运维：面板、重载与更新](/guide/server/operations.md)：网页管理界面、不重启就让配置生效、更新、停止和重启。
- [升级到 0.35](/guide/server/upgrade.md)：从 0.34 及更早版本升级的步骤，旧配置怎么对应到现在的写法，回退办法。
- [备份与恢复](/guide/server/backup.md)：要备份哪些文件，导出导入配置，怎么恢复。
- [排错](/guide/server/troubleshooting.md)：按现象排查：连不上、登录不了、隧道不通、证书失败、端口冲突。
- [日志](/guide/server/logging.md)：日志写到哪、级别和格式、事件名、轮转、fail2ban。
- [HTTPS 与反向代理](/guide/server/https-and-proxy.md)：域名转发的证书，放在 Nginx 或 Caddy 后面。
- [访问控制与登录来源](/guide/server/access-control.md)：IP 黑白名单、登记 IP、登录来源、端口白名单。
- [Webhook](/guide/server/webhook.md)：把客户端上下线、隧道变更等事件推送给你的程序。

全部配置项见[服务端配置](/reference/server-config.md)，命令行见 [nps 命令行](/reference/nps-cli.md)。
