# 说明

本文档补充介绍 NPS 的常见特性与注意事项，便于在部署和使用过程中快速查阅。

## 旧版连接支持

若需兼容旧版客户端，可在 `nps.conf` 中设置 `secure_mode=false`。

旧版客户端连接新版服务端时，在启动参数中添加 `-proto_version=0`。

## 获取用户真实 IP

在 `nps.conf` 中启用 `http_add_origin_header=true` 后，域名代理模式会在每个 HTTP/HTTPS 请求头中注入 `X-Forwarded-For` 和 `X-Real-IP`，以便后端获取访问者的真实地址。

## 热更新支持

大多数配置可在 Web 管理界面实时生效，无需重启客户端或服务端。

## 客户端地址显示

Web 管理界面会显示每个客户端的连接地址。

## 流量统计

系统可统计每个代理的流量使用情况，但受压缩和加密影响，数值可能与实际环境略有差异。

## 当前客户端带宽

可查看每个客户端的当前带宽，结果仅供参考，可能与实际情况存在差异。

## 客户端与服务端版本匹配

为保证程序正常运行，客户端与服务端的核心版本必须一致，否则可能导致连接失败。

## Linux 系统限制

默认情况下 Linux 对连接数量有限制，可根据机器性能调整 `tcp_max_syn_backlog`、`somaxconn` 等内核参数以处理更多连接。

使用 QUIC 时可能会出现缓冲区警告，参考 [quic-go Wiki](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes)。可通过以下命令增大缓冲区以缓解问题：

```bash
echo -e "\nnet.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

## Web 管理保护

当某个 IP 连续登录失败超过 10 次时，将在 1 分钟内禁止其再次尝试。系统还支持 TOTP、图形验证码和 PoW 等多重保护机制。

