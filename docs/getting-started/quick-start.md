# 10 分钟快速开始

假设公网服务器 IP 为 `1.1.1.1`，内网机器上有一个要访问的 SSH 服务。

## 1. 启动服务端

```bash
docker run -d --restart=always --name nps --net=host \
  -v /opt/nps:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

首次运行时容器把示例配置复制到 `/conf/nps.conf`，重启后按它运行。其他安装方式见[安装指南](/getting-started/install.md)。

确保防火墙放行：Web 管理端口 `8081/tcp`，客户端连接端口 `8024/tcp`。默认配置还会占用 `80`、`443`、`8024/udp`、`8025`、`8026`、`8027`、`6000-6002/udp`，端口冲突请修改 `nps.conf`。

## 2. 修改默认密码

示例配置的管理员账号是 `admin`，密码是 `123`。**正式使用前修改 `nps.conf` 的 `web_username`、`web_password`**，然后重启 nps。

## 3. 创建客户端

打开 `http://1.1.1.1:8081`，在“客户端”页新增一个客户端，点击它前面的 `+` 号，复制启动命令。

## 4. 启动 npc

在内网机器上运行刚才复制的命令，形如：

```bash
./npc -server=1.1.1.1:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
```

客户端列表显示在线即连通。连不上时先用 `telnet`、`nc` 确认能访问 `1.1.1.1:8024`。

## 5. 添加隧道

在该客户端下新增一条 TCP 隧道：监听端口 `8001`，目标 `10.1.50.101:22`。然后：

```bash
ssh -p 8001 root@1.1.1.1
```

## 下一步

- 其他用途：[选择隧道类型](/getting-started/tunnel-types.md)
- 注册系统服务：[启动 NPC 客户端](/getting-started/start-client.md)
- 给客户端连接加密：把 `-server` 端口换成 `8025` 并使用 `-type=tls`
