# 连接与系统服务

## 用网页给出的命令连接

最简单的方式是复制网页生成的命令：

1. 打开管理网址，如 `http://203.0.113.10:8081`，进入左侧菜单“客户端”。
2. 在客户端那一行点“启动命令”按钮（终端图标），打开“启动命令”页面。展开这一行也能看到一条可复制的“客户端命令”。
3. 选择“运行方式”（“临时启动”“安装为服务”“Docker”）和“系统”。
4. 复制“单行命令”，在内网机器上运行。

命令大致是这样（网页生成的链接通常是 `type=auto`，还带路径和其他参数，以网页显示为准）：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
```

`YOUR_VKEY` 是这个客户端的密钥（vkey），就是“客户端”列表里展开后看到的“唯一验证密钥”。`npc://` 开头的这段叫启动链接，它写清了连接哪个服务端、用什么方式。

运行后网页上客户端显示在线，就是连上了。接下来去[创建隧道](/guide/tunnels/README.md)。

命令里含明文密钥，不要公开。

## 选择连接方式

连接方式就是链接里的 `type`，也是“启动命令”页面的“载体”。服务端默认的混合端口是 `8024`，一个端口同时接受几种方式。

| `type` | 适用情况 |
| --- | --- |
| `auto` | 不确定用哪种时使用。npc 在混合端口上依次尝试 `tls`、`quic`、`wss`、`https`。不写 `type` 就是它 |
| `tls` | 大多数情况的首选，走 TCP |
| `quic` | 走 UDP。需要放行服务端端口的 UDP |
| `wss`、`https` | 只放行 HTTPS，或者前面有反向代理时使用，路径和参数建议用网页生成 |
| `tcp`、`ws`、`http` | 明文方式。只有服务端打开了 `mix_plain` 才能用，用于前面已经有 TLS 反向代理的情况 |

想要一条连接有多条备选路径，在“启动命令”页面点“添加路径”，生成的命令会自动带上。参数含义见[启动链接与配置格式](/reference/npc-launch.md)。

## 经代理连接服务端

内网机器不能直接访问外网时，在链接上加 `proxy` 参数：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls&proxy=socks5://user:pass@127.0.0.1:1080"
```

支持 `http`、`https`、`socks5`、`socks5h`、`socks4`、`socks4a`。

## 安装为系统服务

系统服务会开机自启，退出登录也继续运行。在“启动命令”页面把“运行方式”选为“安装为服务”，复制命令运行，然后启动服务。

Linux、macOS：

```bash
sudo ./npc install -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
sudo npc start
```

Windows（以管理员身份打开 PowerShell）：

```powershell
.\npc.exe install -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
.\npc.exe start
```

常用操作：

```bash
sudo npc stop       # 停止
sudo npc restart    # 重启
sudo npc uninstall  # 卸载
```

要修改参数，重新执行一次 `npc install` 并带上新参数即可，旧服务会先被卸载。然后再执行 `npc start`。

### 参数保存在哪里

服务定义可能被其他用户读到，所以 `install` 不把密钥写进去，而是写进安装目录的 `conf` 文件夹。文件只有属主可读。安装目录在 Linux、macOS 上是 `/etc/nps`，在 Windows 上是 `C:\Program Files\nps`。

| 你写的内容 | 保存到 | 服务实际的参数 |
| --- | --- | --- |
| `-launch` 的链接、网址或 base64，或者环境变量 `NPC_LAUNCH` | `conf/npc-service-launch-1.conf`（第几条就是几） | `--launch=@该文件` |
| `-launch=@文件` | 不复制 | `--launch=@文件的绝对路径` |
| `-L` | `conf/npc-service-local.conf` | `--local=@该文件` |
| `-L=@文件` | 不复制 | `--local=@文件的绝对路径` |
| `-config` | 不复制 | `--config=文件的绝对路径` |
| 日志等其他参数 | 不单独保存 | 原样带在服务定义里 |

几点说明：

- 用 `@文件` 时，安装后这个文件不能移动，修改后执行 `npc restart` 生效。
- 服务固定带 `-debug=false`，日志按 `-log` 输出，默认写文件，位置见[更新与维护](/guide/client/maintenance.md)。
- 重新安装会清理不再使用的 `npc-service*` 文件，不会改动 `conf/npc.conf`。
- 连接相关的 `-pipes` 不会带到服务里，需要时写进链接：`&pipes=multi`。
- 用配置文件时请写 `-config=绝对路径`，不要依赖服务的当前目录。

## 查询状态与登记 IP

这两个命令执行一次就退出，连接用 `-launch` 给出（可重复，也可以用 `NPC_LAUNCH`），给出多个连接时依次执行：

```bash
# 查询这个 vkey 的隧道和域名转发状态
./npc status -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
# 登记本机当前 IP，2 小时内有效
./npc register -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" -time=2
```

登记 IP 的用途见[访问控制](/guide/server/access-control.md)。
