# 连接与系统服务

## 无配置文件模式（推荐）

所有配置在 Web 管理界面完成，客户端只需一条命令。

```bash
# TCP
./npc -server=ip:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
# TLS
./npc -server=ip:8025 -vkey=YOUR_CLIENT_VKEY -type=tls
```

默认端口 `8024` 用于 TCP 和 KCP，`8025` 用于 TLS 和 QUIC，`8026` 用于 WS，`8027` 用于 WSS。

## 连接多个服务端

一个进程可以同时连接多个服务端，逗号分隔，较短的列表重复最后一个值：

```bash
npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls
```

这里 `xxx:123` 使用 tcp，`yyy:456` 和 `zzz:789` 使用 tls。`-vkey`、`-type`、`-local_ip` 都可以省略填写。

也可以用环境变量代替参数：`NPC_SERVER_ADDR`、`NPC_SERVER_VKEY`、`NPC_CONFIG_PATH`、`NPC_LOCAL_IP`、`NPC_LAUNCH`。

## 注册系统服务

```bash
sudo ./npc install -server=ip:8024 -vkey=xxx -type=tcp -log=off
sudo npc start
sudo npc stop
sudo npc uninstall  # 修改参数后重新 install
```

```powershell
npc.exe install -server=ip:8024 -vkey=xxx -type=tcp -log=off
npc.exe start
npc.exe stop
npc.exe install -server=xx,yy -vkey=xx,yy -type=tcp,tls -config=xxx,yyy -log=off
npc.exe uninstall
```

日志默认位置：Windows 为程序目录下的 `npc.log`，Linux/macOS 为 `/var/log/npc.log`，可用 `-log_path` 修改。

## 通过代理连接 NPS

机器无法直接访问外网时，可以经代理连接：

```bash
./npc -server=xxx:123 -vkey=xxx -proxy=socks5://111:222@127.0.0.1:8024
```

```ini
[common]
proxy_url=socks5://111:222@127.0.0.1:8024
```

| 类型 | 格式 |
| --- | --- |
| SOCKS5 | `socks5://username:password@ip:port`（`socks5h://` 同义，均由代理解析域名） |
| SOCKS4 | `socks4://userid@ip:port`（本地解析域名）、`socks4a://userid@ip:port`（代理解析域名） |
| HTTP | `http://username:password@ip:port`；省略协议的 `ip:port` 视为 HTTP 代理 |
| HTTPS | `https://username:password@host:port`（与代理之间使用 TLS，并校验代理证书） |

未配置时读取环境变量 `ALL_PROXY`（或 `all_proxy`），并按 `NO_PROXY` 跳过匹配的服务器地址。

## 查看状态与登记

```bash
# 查询此 vkey 的连接和隧道状态
./npc status -server=ip:8024 -vkey=xxx -type=tcp
# 登记当前 IP，见“仅允许已登记的 IP”
./npc register -server=ip:8024 -vkey=xxx -time=2
```

`npc restart` 重启系统服务，配置文件的修改由此生效。
