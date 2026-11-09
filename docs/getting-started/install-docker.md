# Docker 安装

镜像同时发布在 **DockerHub**（推荐）和 **GitHub Container Registry**。

## NPS 服务端

```bash
# DockerHub
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v <local_conf_dir>:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps

# GHCR
docker pull ghcr.io/djylb/nps
docker run -d --restart=always --name nps --net=host -v <local_conf_dir>:/conf -v /etc/localtime:/etc/localtime:ro ghcr.io/djylb/nps
```

- `/conf` 保存配置、数据文件和实例密钥，必须持久挂载。
- `/conf/nps.conf` 不存在时，首次启动只复制示例配置然后退出，`--restart=always` 让容器按新配置再次启动。
- 镜像附带 Geo 数据；镜像里的版本更新时覆盖 `/conf` 中的旧文件，`nps update-geo` 下载的更新文件会保留。
- 镜像以 `-disable_remote_update` 运行，升级请更换镜像。

## NPC 客户端

```bash
# DockerHub
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -launch="npc://VKEY@xxx:123?type=tls" -log=off

# GHCR
docker pull ghcr.io/djylb/npc
docker run -d --restart=always --name npc --net=host ghcr.io/djylb/npc -launch="npc://VKEY@xxx:123?type=tls" -log=off
```

未使用 `--net=host` 时注意：隧道端口由 **NPS 服务端**开放，不是 NPC 容器。

群晖等 NAS 同样推荐 Docker。第三方群晖套件可在 [Telegram 群](https://t.me/npsdev)获取。
