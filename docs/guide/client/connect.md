# 连接与系统服务

## 无配置文件模式（推荐）

所有配置在 Web 管理界面完成，客户端只需一条命令。

```bash
# TCP
./npc -server="ip:8024" -vkey="YOUR_CLIENT_VKEY" -type="tcp"
# TLS
./npc -server="ip:8025" -vkey="YOUR_CLIENT_VKEY" -type="tls"
```

默认端口 `8024` 用于 TCP 和 KCP，`8025` 用于 TLS 和 QUIC，`8026` 用于 WS，`8027` 用于 WSS。

## 连接多个服务端

一个进程可以同时连接多个服务端，逗号分隔，较短的列表重复最后一个值：

```bash
npc -server="xxx:123,yyy:456,zzz:789" -vkey="key1,key2,key3" -type="tcp,tls"
```

这里 `xxx:123` 使用 tcp，`yyy:456` 和 `zzz:789` 使用 tls。`-vkey`、`-type`、`-local_ip` 都可以省略填写。

## 一个连接的多个承载（候选）

逗号分开的是**不同的连接**（各自的服务端、vkey、承载）；同一个连接到同一服务端的几种走法用 `|` 分开，叫**承载候选**。候选可以写成 URL，方案就是承载：

```bash
./npc -server="tls://a.com:8025|wss://a.com:443/ws?tier=1|tcp://b.com:8024?standby=1" -vkey="YOUR_CLIENT_VKEY"
```

每次连接和断线重连时，npc 先试上次成功的候选，再按 `tier` 从小到大、同 tier 按书写顺序，`standby` 的放到最后；一个失败就换下一个。不带方案的候选仍是旧写法，承载取 `-type`。参数 `tier`、`weight`、`standby`、`count`、`dirs`、`kinds`、`host`、`sni`、`alpn` 及 `-pipes=multi` 的含义见 [NPC 命令行参数](/reference/npc-cli.md#服务器地址)。多管道写法（当前版本按单管道运行，告警一次）：

```bash
./npc -pipes=multi -server="tls://a.com:8025?tier=0&count=5|wss://b.com/ws?tier=0&dirs=up&count=3|tcp://c.com:8024?tier=1&dirs=down&count=5" -vkey="YOUR_CLIENT_VKEY"
```

**引号**：值里有 `|`、`&`、`?` 时必须加引号，建议一律用双引号。Windows cmd 只认双引号；`.bat` 脚本里 `%` 要写成 `%%`。

也可以用环境变量代替参数：`NPC_SERVER_ADDR`、`NPC_SERVER_VKEY`、`NPC_CONFIG_PATH`、`NPC_LOCAL_IP`、`NPC_LAUNCH`。

## 注册系统服务

```bash
sudo ./npc install -server="ip:8024" -vkey="xxx" -type="tcp" -log=off
sudo npc start
sudo npc stop
sudo npc uninstall  # 修改参数后重新 install
```

```powershell
npc.exe install -server="ip:8024" -vkey="xxx" -type="tcp" -log=off
npc.exe start
npc.exe stop
npc.exe install -server="xx,yy" -vkey="xx,yy" -type="tcp,tls" -config="xxx,yyy" -log=off
npc.exe uninstall
```

安装时连接参数（`-server`、`-vkey`、`-type`、`-pipes`、`-proxy`、`-local_ip`，带 `-password` 时连同 `-local_type`、`-local_port`、`-target`、`-conn_mode`、`-local_proxy`）不写进服务定义，而是写到安装目录下的 `conf/npc-service.json`（仅属主可读写，Linux/macOS 为 `/etc/nps/conf/`），服务以 `--launch=@<该文件>` 运行；同时给的 `-config` 文件也改为 `--launch=@<绝对路径>`。`--launch` 或 `NPC_LAUNCH` 的内容写到 `conf/npc-service-launch-N.conf`，`@文件` 只转为绝对路径。只用 `-config` 安装时服务仍用 `-config`，路径转为绝对路径。不会改动已有的 `conf/npc.conf`。

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
