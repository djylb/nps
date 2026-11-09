# 启动链接与多条连接

npc 用 `-launch` 描述连接，用 `-L` 描述本机的点对点访问端口，两者都可以重复。下面的例子都能在网页的“启动命令”页面生成。完整的格式见[启动链接与配置格式](/reference/npc-launch.md)。

## 连接一个服务端

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
```

`YOUR_VKEY` 从网页“客户端”列表的“唯一验证密钥”处获取。

一条连接可以有备用路径，主路径不通时自动切换：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls&alt=quic://203.0.113.10:8024?standby=true"
```

## 连接多个服务端

每条连接写一个 `-launch`，彼此独立运行，一条断开不影响其他连接：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls#home" \
      -launch="npc://YOUR_VKEY@www.example.com:8024?type=quic#office"
```

`#` 后面是连接名，显示在日志里，`-L` 也靠它选择连接。

## 在本机开点对点访问端口

先在提供服务的客户端上创建[点对点隧道](/guide/tunnels/secret.md)，记下访问密钥（`YOUR_ACCESS_KEY`）。然后在访问端运行：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" \
      -L="tcp://127.0.0.1:2000?access=YOUR_ACCESS_KEY&target=192.168.1.10:22"
```

之后连接本机的 `2000` 端口，就到达了内网的 `192.168.1.10:22`：

```bash
ssh -p 2000 user@127.0.0.1
```

想把对端网络当作代理使用，把类型换成 `mixed`（同时支持 HTTP 和 SOCKS5），目标由每条连接决定，不用写 `target`：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" \
      -L="mixed://user:pass@127.0.0.1:1080?access=YOUR_ACCESS_KEY"
```

`-L` 可以写多个。有多条连接时，每个 `-L` 用 `via` 指定走哪一条：

```bash
./npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls#home" \
      -launch="npc://YOUR_VKEY@www.example.com:8024?type=tls#office" \
      -L="tcp://127.0.0.1:2000?access=YOUR_ACCESS_KEY&target=192.168.1.10:22&via=home" \
      -L="tcp://127.0.0.1:2001?access=YOUR_ACCESS_KEY&target=192.168.1.10:22&via=office"
```

默认 `conn_mode=auto`，能打通就直连，打不通经服务端中转。

## 使用配置文件

连接较多时，可以写一个 JSONC 文件（JSON 加注释）。“启动命令”页面的“配置文件 JSONC”可以直接生成。

```jsonc
// npc.jsonc
{
  "profiles": {
    "home": {
      "vkey": "YOUR_VKEY",
      "paths": [{"url": "tls://203.0.113.10:8024"}],
      "access": [
        {"access": "YOUR_ACCESS_KEY", "target": "192.168.1.10:22", "local_type": "tcp", "local_port": 2000}
      ]
    }
  }
}
```

```bash
./npc -launch=@npc.jsonc
```

`-L` 的内容较多时也可以放到文件里，每行一条，用 `-L=@文件` 读取。

## 用环境变量传入

容器等不方便改命令行的场合，可以用环境变量 `NPC_LAUNCH`，每行一条：

```bash
export NPC_LAUNCH="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
./npc
```

## 把配置打成一行

`npc launch pack` 把文件压缩成一行，可以直接作为 `-launch` 的值。结果含明文密钥，请妥善保管：

```bash
./npc launch pack npc.jsonc
./npc -launch=上一步输出的内容
```

## 检查命令

`npc launch show` 打印参数实际的含义，不会真正连接，默认遮盖密钥，可以用来检查写法：

```bash
./npc launch show -launch=@npc.jsonc
```

安装为系统服务的方法见[连接与系统服务](/guide/client/connect.md)，这里的 `-launch` 和 `-L` 在 `npc install` 后面同样可用。
