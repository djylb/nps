# 透明代理

## 这能解决什么

让应用程序不做任何代理设置：由防火墙把发往某些地址的 TCP 连接导向访问端 npc 的一个本地端口，npc 取出连接原本要去的地址，经点对点隧道转发到内网。适合不支持设置代理的程序，或想让整台机器、整个局域网的流量按规则走隧道。

透明代理是[点对点访问](/guide/tunnels/secret.md)的一种本地类型（`local_type` 为 `transparent`），只处理 TCP，不处理 UDP。

## 系统要求

| 系统 | 导流方式 | 权限 |
| --- | --- | --- |
| Linux | `iptables` 的 `REDIRECT` | 普通权限即可 |
| Linux | `TPROXY` | root 或 `CAP_NET_ADMIN`；权限不足时退回普通监听并在日志里告警，此时 `TPROXY` 规则到达不了它，`REDIRECT` 不受影响 |
| macOS | `pf` 的 `rdr` | 取原始目标地址需要 root |
| FreeBSD | `pf` 的 `rdr` | 普通监听即可，权限足够时会设置透明选项 |
| Windows 等其他系统 | 不支持 | 启动时直接报错 |

网页“启动命令”页只在“系统”选 Linux 时提供“transparent”这个本地类型；其他系统要手写命令。npc 不会修改防火墙，规则需要自己配置。

## 操作步骤

先按[点对点访问](/guide/tunnels/secret.md)新增隧道并拿到访问密钥，然后：

1. 在访问端运行（不写 `target`，目标由每个连接自己的原始地址决定）：

   ```bash
   ./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" \
     -L="transparent://127.0.0.1:2000?access=YOUR_ACCESS_KEY"
   ```

   `-L` 的完整写法见 [npc 启动参数](/reference/npc-launch.md)。

2. 加防火墙规则，把要走隧道的流量导向 `2000` 端口。

REDIRECT 示例（本机发出的、去往 `192.168.0.0/16` 的 TCP 连接）：

```bash
iptables -t nat -A OUTPUT -p tcp -d 192.168.0.0/16 -j REDIRECT --to-ports 2000
```

TPROXY 示例（经过本机转发的流量，需要 root）：

```bash
ip rule add fwmark 1 lookup 100
ip route add local 0.0.0.0/0 dev lo table 100
iptables -t mangle -A PREROUTING -p tcp -d 192.168.0.0/16 -j TPROXY --on-port 2000 --tproxy-mark 1
```

macOS、FreeBSD 用 `pf` 的 `rdr` 规则把流量导向同一个端口。

**导流规则不要匹配到 npc 自己连接 nps 的流量**，否则会形成回环。

## 怎么验证

在访问端本机：

```bash
curl -I http://192.168.1.10/
```

应用不需要设置代理，直接得到内网机器的响应即可。直接连到监听端口的连接（没有经过导流规则）会被拒绝，这是正常的。

## 常见选项

- **监听地址**：写成 `127.0.0.1` 时只处理本机发出的流量；要处理局域网内其他机器经本机转发的流量，改成 `0.0.0.0`。透明代理本身没有账号认证，监听非本机地址时局域网内都可以使用。
- **访问范围**：能转发到哪些地址，由隧道的“目标访问控制”决定，见[点对点访问](/guide/tunnels/secret.md)。
- **连接模式**：`conn_mode` 可选 `auto`、`relay`、`direct`，含义同点对点访问。

## 相关页面

- [点对点访问](/guide/tunnels/secret.md)
- [npc 启动参数](/reference/npc-launch.md)
