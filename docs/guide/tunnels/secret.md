# 私密代理

**适用范围：** 不想占用公网端口、防止他人连接的 TCP 服务，例如 SSH。所有流量经 nps 中转。

**场景：** 不开公网端口访问内网 `10.1.50.2:22`。

1. 在客户端中添加私密代理，设置唯一密钥 `secrettest` 和内网目标 `10.1.50.2:22`。
2. 在需要连接的机器上运行（具体命令可从 Web 复制）：

```bash
./npc -server=1.1.1.1:8024 -vkey=vkey -type=tcp -password=secrettest -local_type=secret
```

`-local_port` 指定本地端口，默认 `2000`。然后：

```bash
ssh -p 2000 root@127.0.0.1
```

配置 `visitor_vkey` 后，网页上的访问命令使用这个只能访问隧道的密钥，访问方不需要客户端自己的密钥。

## 指定目标

允许访问端自己指定目标：服务端设置 `allow_secret_link=true`，访问端加 `-target=ip:port`。此时连接遵守目标名单。`allow_secret_local=true` 允许访问端用 `-local_proxy=true` 连到服务端本地。
