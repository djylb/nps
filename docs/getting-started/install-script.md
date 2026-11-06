# 脚本安装

此方式不支持 **Windows**。

```bash
# NPS（默认配置目录 /etc/nps，程序安装到 /usr/bin 或 /usr/local/bin）
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
nps install
nps start|stop|restart|uninstall
nps update

# NPC
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls -log=off
npc start|stop|restart|uninstall
npc update
```

不传参数时安装最新版本的 `nps` 和 `npc`。参数和对应的环境变量：

| 位置 | 环境变量 | 含义 | 默认 |
| --- | --- | --- | --- |
| 第一个 | `NPS_INSTALL_MODE` | `nps`、`npc` 或 `all` | `all` |
| 第二个 | `NPS_INSTALL_VERSION` | 版本，如 `v0.35.0` | `latest` |
| 第三个 | `NPS_INSTALL_DIR` | 安装目录；指定后直接解压到该目录，不安装到系统路径 | 无 |
