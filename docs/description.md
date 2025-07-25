# 说明

## 旧版连接支持

服务端如需支持旧版本客户端需要在`nps.conf`中设置`secure_mode=false`

客户端如果需要连接旧版服务端需要在启动时添加`-proto_version=0`参数

## 获取用户真实IP

如需使用需要在`nps.conf`中设置`http_add_origin_header=true`

在域名代理模式中，可以通过request请求 header 中的 X-Forwarded-For 和 X-Real-IP 来获取用户真实 IP。

**本代理前会在每一个http(s)请求中添加了这两个 header。**

## 热更新支持

对于绝大多数配置，在Web管理中的修改将实时使用，无需重启客户端或者服务端

## 客户端地址显示

在Web管理中将显示客户端的连接地址

## 流量统计

可统计显示每个代理使用的流量，由于压缩和加密等原因，会和实际环境中的略有差异

## 当前客户端带宽

可统计每个客户端当前的带宽，可能和实际有一定差异，仅供参考。

## 客户端与服务端版本对比

为了程序正常运行，客户端与服务端的核心版本必须一致，否则将导致客户端无法成功连接致服务端。

## Linux系统限制

默认情况下linux对连接数量有限制，对于性能好的机器完全可以调整内核参数以处理更多的连接。
`tcp_max_syn_backlog` `somaxconn`
酌情调整参数，增强网络性能

QUIC在使用时可能会缓冲区警告，具体参考 [Wiki](https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes)

可使用下面命令配置增大缓冲区来缓解

```bash
echo -e "\nnet.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

## Web管理保护

当一个ip连续登陆失败次数超过10次，将在一分钟内禁止该ip再次尝试。此外还支持TOTP、图形验证码和PoW保护。

