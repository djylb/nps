# 备份与恢复

nps 的全部状态都是 `conf` 目录里的几个文件，没有数据库。备份就是复制这些文件；恢复就是把它们放回去再启动。网页上还有一个“导出配置/导入配置”功能，用来在两台 nps 之间搬条目，它不能代替备份。

## 要备份什么

`conf` 目录的位置：Linux 安装后是 `/etc/nps/conf`，Windows 是 `C:\Program Files\nps\conf`，用 `-conf_path` 指定过的以指定位置为准，Docker 是挂载到 `/conf` 的那个目录。

| 文件 | 内容 | 要不要备份 |
| --- | --- | --- |
| `nps.conf` | 服务端配置 | 必须 |
| `clients.json` | 客户端：vkey、限制、名单、累计流量 | 必须 |
| `tasks.json` | TCP、UDP、混合代理、点对点访问等隧道 | 必须 |
| `hosts.json` | 域名转发条目，含各域名自己的证书 | 必须 |
| `global.json` | 全局名单、全局限制等“全局参数”页的内容 | 必须 |
| `instance.key` | [实例主密钥](/guide/server/operations.md?id=实例主密钥)，网页登录状态的签名和启动链接的准入密钥由它派生 | 必须；丢了所有登录状态失效、已发出的带 `admit_key` 的启动链接作废 |
| 证书和私钥 | `https_default_cert_file`、`https_default_key_file`、`web_cert_file`、`web_key_file`、`bridge_cert_file`、`bridge_key_file` 指向的文件（配置里直接写 PEM 文本的不用另外备份） | 如果用了自己的证书则必须 |
| `ssl_path` 目录 | 自动申请得到的证书及缓存，发布包自带配置里是 `conf/ssl` | 建议；丢了会重新申请，注意证书机构有申请频率限制 |
| 自定义文件 | `error_page`、`fallback_404_file`、`shaping_file` 等你自己放的文件 | 用了就要 |
| `geoip.dat`、`geosite.dat` | Geo 数据 | 不用，随时可以用 `nps update-geo` 重新下载 |

说明：

- 配置里的相对路径一般以 nps 的运行目录为基准（Linux 安装后是 `/etc/nps`），所以 `conf/server.pem` 和 `conf/ssl` 实际都在 `conf` 目录下；`instance.key` 则相对 `nps.conf` 所在目录。不确定时，打开 `nps.conf` 看看这些键写了什么。
- 由 npc 配置文件发布的隧道和域名不存盘，它们随客户端上线再出现，不用备份。
- 这些文件里有**明文的 vkey、登录密码和证书私钥**，备份文件要放在受控的位置，不要提交到版本库或发到聊天里。
- `.bad` 结尾的文件（如 `tasks.json.bad`）是 nps 读不出某个数据文件时留下的原件，排查问题时保留。
- 累计流量按 `flow_store_interval` 设置的周期写入数据文件，所以备份里的流量数字可能比当前值旧一点。

## 怎么备份

数据文件是整体写入临时文件后再改名的，运行中复制每个文件都是完整的；要得到一致的整体（比如大批修改刚结束），先停一下服务再复制最稳妥。

```bash
sudo tar czf nps-backup-$(date +%Y%m%d-%H%M%S).tar.gz -C /etc/nps conf
```

```powershell
Copy-Item "C:\Program Files\nps\conf" "D:\backup\nps-conf" -Recurse
```

Docker 部署备份挂载的目录即可（`<local_conf_dir>`）。

定期备份可以用系统自带的计划任务，每次升级、批量改动之前也手动备份一次。想只保存条目，也可以用下面的导出。

## 导出和导入

管理员在“全局参数”页的“配置导出导入”区域操作：

- 点“导出配置”，下载 `nps-config-<时间>.json`。接口是 `GET /api/v1/global/export`。
- “导入文件”选择 JSON，点“导入配置”，接口是 `POST /api/v1/global/import`（表单字段 `data` 为文件内容），见 [Web API](/reference/web-api.md)。

**导出的内容**：客户端、隧道、域名，连同它们的 vkey、密码和证书。**不包含**：`nps.conf`、实例主密钥、全局参数页的内容（全局名单和限制）、由 npc 配置文件发布的条目、流量统计。

**导入的规则**：

- 所有条目作为**新条目**加入并分配新 ID，不会替换已有的内容。
- 先整体校验：vkey、登记口令、网页用户名、已启用隧道的端口、域名，只要与现有内容或文件自身冲突，或名单规则有误，整个文件被拒绝，一条都不会导入。
- 添加时才发现的冲突（例如端口无法绑定）会撤销已经添加的部分。
- 导入的条目流量从零开始，保留流量上限和时间限制。
- 关联到虚拟客户端（负数 ID）的条目，要求目标 nps 上有同一个虚拟客户端。
- 文件格式版本不符时拒绝。导出文件只用于 0.x 版本的 nps 之间搬配置，不是迁移到新大版本的途径；0.34 及更早导出的点对点隧道，导入时会换算成点对点访问。

导出文件含明文密钥，与备份文件同样对待。因为 vkey 不能重复，**导出的文件不能直接导回同一台 nps**，要在同一台上恢复请用下面的整目录恢复，或先删掉对应的条目。

## 恢复

### 整目录恢复（同一台或换机器）

1. 在新机器上安装 nps，版本不低于备份时的版本，见[安装](/getting-started/install.md)。先不要启动，或先 `nps stop`。
2. 把备份的 `conf` 目录内容放回去，覆盖 `nps.conf`、四个 `.json` 数据文件和 `instance.key`，以及证书、`ssl_path` 目录。
3. 检查 `nps.conf` 里写死的路径、`bridge_addr`、`p2p_ip` 等和新机器是否一致。
4. 启动 nps（`nps start`、`docker start nps`），登录网页确认客户端、隧道、域名都在。
5. 客户端的 vkey 没变，npc 会自动重连。如果新机器的 IP 或域名变了，把 npc 的连接地址改过去；网页“客户端”页的“启动命令”会显示新的完整命令。

恢复时保留了 `instance.key`，网页的登录状态和已发出的启动链接继续有效。

### 导入恢复（只有导出文件时）

1. 安装并启动一台全新的 nps，登录网页。
2. 在“全局参数”页导入 JSON 文件。新 nps 里没有冲突的条目，会全部以新 ID 加入。
3. 手动恢复 `nps.conf`、证书和全局参数页的设置，这些不在导出文件里。
4. 条目的 ID 与以前不同，但 vkey 不变，npc 不用改。

## 客户端的备份

npc 通常没有什么状态。需要保留的是你自己写的 `npc.conf`、启动命令，以及用 `npc install` 装成服务时在安装目录 `conf` 下生成的 `npc-service.json`、`npc-service-launch-N.conf` 和 `npc-service-local.conf`（仅属主可读，里面有 vkey 或点对点密钥）。
