# 文件访问

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

文件服务是一个虚拟后端，由 npc 上报给服务端。写了 `server_port` 时，会自动生成一条监听该端口、目标为这个虚拟后端的 tcp 隧道（网页上标“自动生成”），删除虚拟后端时一起删除。不写 `server_port` 时只声明虚拟后端，名称取段名；需要对外访问时，在网页的文件访问列表复制它的 `service://` 目标，填到其他隧道或域名的目标里。

可以从其他隧道嵌套转发到文件隧道，见[代理与嵌套转发](/reference/features-transport.md)。
