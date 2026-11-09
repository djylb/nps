# 透明代理与 TPROXY

透明代理让应用不改配置：防火墙把连接导向 nps 或 npc 的监听端口，程序取出原始目标后转发。有两种模式：

| 模式 | 运行位置 | 启用 |
| --- | --- | --- |
| `transparent` | 点对点访问的访问端 npc | `npc -launch=… -L="transparent://:2000?access=xx"` |
| `tcpTrans` | nps 服务端 | npc.conf 隧道段 `mode=tcpTrans`、`server_port=...` |

## 导流方式

| 方式 | 平台 | 权限 |
| --- | --- | --- |
| `REDIRECT` | Linux | 普通监听即可 |
| `TPROXY` | Linux | root 或 `CAP_NET_ADMIN` |
| `pf rdr` | macOS、FreeBSD | 普通监听即可 |

监听 socket 会设置透明选项。权限不足时退回普通监听并告警，此时 `TPROXY` 规则到达不了该监听，`REDIRECT` 不受影响。不支持透明代理的平台启动时直接报错。没有经过重定向、直接连到监听端口的连接会被拒绝。

## REDIRECT 示例

```bash
iptables -t nat -A OUTPUT -p tcp -d 10.2.0.0/16 -j REDIRECT --to-ports 2000
```

## TPROXY 示例

```bash
ip rule add fwmark 1 lookup 100
ip route add local 0.0.0.0/0 dev lo table 100
iptables -t mangle -A PREROUTING -p tcp -d 10.2.0.0/16 -j TPROXY --on-port 2000 --tproxy-mark 1
```

确保导流规则不匹配 npc、nps 自身的出站连接，否则会形成回环。
