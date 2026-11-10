# 10 分钟快速开始

这一页带你从零做到：在公网上访问内网机器的 SSH。

你需要：

- 一台有公网 IP 的 Linux 服务器，已装好 Docker。下文假设它的 IP 是 `203.0.113.10`。没有 Docker 时见[安装指南](/getting-started/install.md)。
- 一台内网机器，下文假设地址是 `192.168.1.10`，上面运行着 SSH（22 端口）。

整个过程只有两个程序：公网上的 **nps**（服务端）和内网里的 **npc**（客户端）。npc 主动连接 nps，访问者连 nps 的端口，流量经 npc 转到内网机器。

## 1. 启动 nps

在公网服务器上运行：

```bash
docker run -d --restart=always --name nps --net=host \
  -v /opt/nps:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

第一次启动时，容器把默认配置复制到 `/opt/nps/nps.conf` 后退出一次，`--restart=always` 会让它立刻按这份配置重新启动。

在防火墙或云服务商的安全组放行下面的端口：

| 端口 | 协议 | 用途 |
| --- | --- | --- |
| `8081` | TCP | 网页管理 |
| `8024` | TCP 和 UDP | npc 连接 nps（混合端口） |
| `8001` | TCP | 第 5 步要创建的隧道 |

默认配置还会占用 `80`、`443`（域名转发）和 `6000-6002/udp`（点对点访问）。这些端口被其他程序占用时，修改 `/opt/nps/nps.conf` 里的 `http_proxy_port`、`https_proxy_port`、`p2p_port`，然后 `docker restart nps`。设为 `0` 表示不启用该功能。

日志用 `docker logs nps` 查看（`nps.conf` 里 `log=stdout`）；想同时写文件 `/opt/nps/nps.log`，把 `log` 设为 `both`。日志里出现 `bridge certificate not loaded, using a generated one` 和 `cert_failed` 是正常现象：你还没有配置证书，nps 会自己生成，不影响下面的步骤。

## 2. 设置管理员密码

发布包的 `nps.conf` 里管理员账号密码是空的，且 `web_require_password=true`：设置前任何人都登不进管理页，登录页会提示。**先在 `/opt/nps/nps.conf` 里设置 `web_username` 和 `web_password`**，再启动（或重启、重载）：

```ini
web_username=admin
web_password=换成你自己的密码
```

然后重启：

```bash
docker restart nps
```

## 3. 创建客户端

这里的“客户端”是 nps 上的一条记录，一台内网机器对应一条。

1. 浏览器打开 `http://203.0.113.10:8081`，用 `admin` 和新密码登录（登录页需要输入验证码）。
2. 点左侧菜单的“客户端”，再点“新增”。
3. “备注”随便填，比如 `home`；“唯一验证密钥”留空，系统会自动生成。这个密钥（下文写作 `YOUR_VKEY`）相当于这台内网机器的密码，不要公开。
4. 保存后回到列表，就能看到新客户端。

## 4. 在内网机器上启动 npc

先下载 npc（这是 x86_64 机器的包，ARM64 机器把文件名换成 `linux_arm64_client.tar.gz`，其他架构见[安装指南](/getting-started/install.md)）：

```bash
wget https://github.com/djylb/nps/releases/latest/download/linux_amd64_client.tar.gz
tar -xzf linux_amd64_client.tar.gz
```

然后拿到启动命令：

1. 在“客户端”列表里，点这一行最左边的 `+` 展开详情，或者点行尾终端图标的“启动命令”按钮。
2. 复制显示的命令，到内网机器上解压的目录里运行。

命令形如下面这样。**请直接复制网页上的命令，不要手敲**，里面的地址、密钥和参数都是当前服务器的实际值：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024/ws?type=auto&subprotocol=binary&admit_key=..."
```

> 命令里的服务器地址来自你打开网页时用的地址。如果你是用 `127.0.0.1` 或内网地址打开的管理页面，命令里也会是这个地址，内网机器可能连不上。请用公网地址打开，或者在 `nps.conf` 设置 `bridge_addr=203.0.113.10` 后重启 nps。

回到“客户端”列表，这一行的“连接”列显示“在线”，就说明 npc 连上了。

连不上时，先在内网机器上确认能访问服务器的 8024 端口：

```bash
nc -vz 203.0.113.10 8024
```

不通一般是防火墙或安全组没放行；通了但仍然离线，见[排错](/guide/server/troubleshooting.md)。

## 5. 新建 TCP 隧道

1. 点左侧菜单的“TCP 隧道”，再点“新增”。
2. “模式”保持“TCP 隧道”，“客户端 ID”选刚才的客户端。
3. “服务端端口”填 `8001`。
4. “目标 (IP:端口)”填内网机器上要访问的服务，这里是 `192.168.1.10:22`。如果 npc 就运行在这台机器上，也可以写 `127.0.0.1:22`。
5. 保存。

## 6. 验证

在任意一台能上网的电脑上执行（把 `root` 换成内网机器的用户名）：

```bash
ssh -p 8001 root@203.0.113.10
```

能登录到内网机器，就全部跑通了。如果只想确认端口通：

```bash
nc -vz 203.0.113.10 8001
```

## 让 npc 一直运行

刚才的 npc 关掉终端就停了。要开机自启，在网页“启动命令”页把“运行方式”选成“安装为服务”，复制新的命令运行，详见[连接与系统服务](/guide/client/connect.md)。

## 下一步

- 想映射网站、UDP，或者不开公网端口访问：[按用途选择隧道](/guide/tunnels/README.md)
- 弄清各个名词：[核心概念](/getting-started/concepts.md)
- 换一种安装方式：[安装指南](/getting-started/install.md)
