# 补充说明

## 旧版连接支持

范围见[版本规则与兼容范围](/getting-started/versioning.md)。

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

## Linux 系统限制

默认情况下 Linux 对连接数量有限制，可根据机器性能调整 `tcp_max_syn_backlog`、`somaxconn` 等内核参数以处理更多连接。

使用 QUIC 时可能会出现缓冲区警告，参考 [quic-go Wiki](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes)。可通过以下命令增大缓冲区以缓解问题：

```bash
echo -e "\nnet.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

## Web 管理保护

同一 IP 连续登录失败达到 `login_max_fail_times`（默认 10 次）后被暂时禁止，并支持 TOTP、图形验证码、PoW 和[登录来源名单](/guide/server/access-control.md)。

## 已废弃的功能

HTTP 缓存已废弃：它把内容全部缓存在内存，消耗大、命中率低，有需要请用前置 Nginx。
