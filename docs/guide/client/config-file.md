# 配置文件模式

> **已弃用**：npc.conf（INI）及其隧道段、文件模式、`secret`/`p2p` 访问段、`fallback_secret`、`target_type` 等，新写法见[快速启动 -launch](/guide/client/launch.md#配置文件)；隧道在 Web 管理界面创建，点对点访问用 `-L` 或 `access`。这些旧写法仍可读取，转换时告警。新安装的 `conf/npc.conf` 只含 `[common]` 和一个注释掉的点对点访问示例。

适用于不使用 Web 配置、用公钥或客户端 `vkey` 验证、在 `npc.conf` 中完成所有设置的场景。完整键名见 [npc.conf 参考](/reference/npc-config.md)，也可参考[示例文件](https://github.com/djylb/nps/tree/master/conf/npc.conf)。

```bash
./npc -config=/path/to/npc.conf
# 多个配置文件，每个独立运行
./npc -config=/path/to/npc1.conf,/path/to/npc2.conf
```

不指定配置且没有 `-server`、`-vkey` 时，默认读取 `conf/npc.conf`。客户端需要开启“允许配置文件模式连接”，或使用 `public_vkey`。

Windows 下用记事本等编辑均可：UTF-8（含或不含 BOM）、CRLF 换行、段头或键值前后多余空格都能正确解析。

配置文件中出现 `health_*` 配置节时会被忽略并告警，健康检查改在服务端启用，见[健康检查](/guide/tunnels/health-check.md)。单个端口冲突只让该隧道或域名失败，不断开整个客户端。

## 全局配置

```ini
[common]
server_addr=127.0.0.1:8024
conn_type=tcp
vkey=123
dns_server=8.8.8.8
auto_reconnection=true
#local_ip=192.168.1.100
#max_conn=1000
#flow_limit=1000
#rate_limit=1000
#basic_username=11
#basic_password=3
#web_username=user
#web_password=1234
#crypt=true
#compress=true
#pprof_addr=0.0.0.0:9999
#disconnect_timeout=60
```

| 项              | 含义                         |
|----------------|----------------------------|
| server_addr    | 服务端ip/域名:port              |
| conn_type      | 与服务端通信模式（tcp、tls、kcp、quic、ws、wss）          |
| vkey           | 客户端 vkey 或服务端的 `public_vkey`          |
| basic_username | socks5或http(s)密码保护用户名(可忽略；用户名或密码任一非空即开启认证) |
| basic_password | socks5或http(s)密码保护密码(可忽略)  |
| compress       | 是否压缩传输(true或false或忽略)      |
| crypt          | 是否加密传输(true或false或忽略)      |
| rate_limit     | 速度限制，可忽略                   |
| flow_limit     | 流量限制，可忽略                   |
| remark         | 客户端备注，可忽略                  |
| max_conn       | 最大连接数，可忽略                  |
| pprof_addr     | debug pprof ip:port        |

## 域名代理

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[web1]
host=a.proxy.com
target_addr=127.0.0.1:8080,127.0.0.1:8082
host_change=www.proxy.com
header_set_proxy=nps
```

| 项           | 含义                                             |
|-------------|------------------------------------------------|
| web1        | 备注                                             |
| host        | 域名                                             |
| target_addr | 内网目标，负载均衡时多个目标，逗号隔开                            |
| host_change | 请求host修改                                       |
| header_xxx  | 请求header修改或添加，header_proxy表示添加header proxy:nps |

## tcp隧道模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[tcp]
mode=tcp
target_addr=127.0.0.1:8080
server_port=9001
```

| 项           | 含义        |
|-------------|-----------|
| mode        | tcp       |
| server_port | 在服务端的代理端口 |
| target_addr | 内网目标      |

## udp隧道模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[udp]
mode=udp
target_addr=127.0.0.1:8080
server_port=9002
```

| 项           | 含义        |
|-------------|-----------|
| mode        | udp       |
| server_port | 在服务端的代理端口 |
| target_addr | 内网目标      |

## http代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[http]
mode=httpProxy
server_port=9003
```

| 项           | 含义        |
|-------------|-----------|
| mode        | httpProxy |
| server_port | 在服务端的代理端口 |

## socks5代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[socks5]
mode=socks5
server_port=9004
multi_account=multi_account.conf
```

| 项             | 含义                                                                                                                                         |
|---------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| mode          | socks5                                                                                                                                     |
| server_port   | 在服务端的代理端口                                                                                                                                  |
| multi_account | socks5多账号配置文件（可选),配置后使用basic_username和basic_password无法通过认证 <br> multi_account.conf要与可执行文件npc同一目录，或者npc.conf里面写相对路径,conf/multi_account.conf |

## 点对点访问模式

提供端：

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[ssh]
mode=secret
password=ssh2
target_addr=10.1.50.2:22
```

| 项           | 含义     |
|-------------|--------|
| mode        | secret |
| password    | 唯一密钥   |
| target_addr | 内网目标   |

访问端写 `[secret...]` 节（`local_port`、`local_type`、`conn_mode`、`password`），见[点对点访问](/guide/tunnels/secret.md)和 [npc.conf](/reference/npc-config.md)。旧版的 `mode=p2p` 读作 `mode=secret` 并告警。

## 文件访问模式

利用nps提供一个公网可访问的本地文件服务（WebDAV，浏览器也可直接浏览）。

**文件模式只能在 npc.conf 中配置，并用 `npc -config=npc.conf` 启动**；网页管理界面不能创建文件隧道，命令行（`-server`/`-vkey`）模式也不会启动文件服务。

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[file]
mode=file
server_port=9100
local_path=/tmp/
strip_pre=/web/
read_only=false
multi_account=/etc/npc/file_users.conf
```

| 项             | 含义                                                         |
|---------------|------------------------------------------------------------|
| mode          | file                                                       |
| server_port   | 在 nps 服务端开启的端口                                            |
| server_ip     | 可选，nps 服务端监听该端口时绑定的地址（如 0.0.0.0），不是访问地址                   |
| local_path    | npc 本机上要共享的目录                                             |
| strip_pre     | URL 前缀，访问时需带上该前缀                                          |
| read_only     | 为 true 时只读：只允许浏览/下载，拒绝上传、删除、移动等写操作                         |
| multi_account | 可选，账号文件路径，每行 `用户名=密码`；配置后访问需 Basic 认证（`[common]` 的 basic_username/basic_password 也会生效） |

访问地址为 `http://<server-ip>:<server_port>/<strip_pre>`，例如上例访问 `http://1.1.1.1:9100/web/` 即相当于访问 npc 本机的 `/tmp/` 目录。其中：

- `<server-ip>` 是 **nps 服务端** 的地址（公网 IP 或域名），端口 `server_port` 由 nps 服务端监听；
- 它不是 npc 本机的 `127.0.0.1`，也不是 `[file]` 段里的 `server_ip`（那只是服务端监听的绑定地址）。

Windows 下用记事本等编辑 npc.conf 均可：UTF-8（含或不含 BOM）编码、CRLF 换行、段头或键值前后多余空格都能正确解析。

## 断线重连

```ini
[common]
auto_reconnection=true
```

## 端口范围映射

当客户端以配置文件的方式启动时，可以将本地的端口进行范围映射，仅支持tcp和udp模式，例如：

```ini
[tcp]
mode=tcp
server_port=9001-9009,10001,11000-12000
target_port=8001-8009,10002,13000-14000
```

逗号分隔，可单个或者范围，注意上下端口的对应关系，无法一一对应将不能成功

## 端口范围映射到其他机器

```ini
[tcp]
mode=tcp
server_port=9001-9009,10001,11000-12000
target_port=8001-8009,10002,13000-14000
target_ip=10.1.50.2
```

填写target_ip后则表示映射的该地址机器的端口，忽略则便是映射本地127.0.0.1,仅范围映射时有效

## 环境变量渲染

npc支持环境变量渲染以适应在某些特殊场景下的要求。

**在无配置文件启动模式下：**
设置环境变量

```
export NPC_SERVER_ADDR=1.1.1.1:8024
export NPC_SERVER_VKEY=xxxxx
```

直接执行./npc即可运行

**在配置文件启动模式下：**

```ini
[common]
server_addr={{.NPC_SERVER_ADDR}}
conn_type=tcp
vkey={{.NPC_SERVER_VKEY}}
auto_reconnection=true
[web]
host={{.NPC_WEB_HOST}}
target_addr={{.NPC_WEB_TARGET}}
```

在配置文件中填入相应的环境变量名称，npc将自动进行渲染配置文件替换环境变量

## URL路由

本代理支持根据URL将同一域名转发到不同的内网服务器，可在web中或客户端配置文件中设置，此参数也可忽略，例如在客户端配置文件中

```ini
[web1]
host=a.proxy.com
target_addr=127.0.0.1:7001
location=/test
[web2]
host=a.proxy.com
target_addr=127.0.0.1:7002
location=/static
```

对于`a.proxy.com/test`将转发到`127.0.0.1:7001/test`，对于`a.proxy.com/static/bg.jpg`将转发到`127.0.0.1:7002/static/bg.jpg`
