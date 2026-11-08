# 快速启动 --launch

`--launch` 用一段内容启动 npc，不需要配置文件。可以重复传入，每个输入独立运行；也可以用环境变量 `NPC_LAUNCH` 传一个。

设置后 `-server`、`-vkey`、`-type`、`-config`、`-proxy`、`-local_ip`、`-password`、`-target` 被忽略并告警。日志、`-skip_verify` 等进程级设置仍只由命令行决定。

```bash
npc --launch 'npc://vkey@1.2.3.4:8024?type=tls'
npc --launch https://example.com/npc.yaml --launch @/etc/npc/office.json
```

## 输入形式

| 形式 | 示例 |
| --- | --- |
| npc URL | `npc://vkey@1.2.3.4:8024?type=tls` |
| 多服务器 | `npc://?server=a:8024,b:8024&vkey=k1,k2&type=tls,tcp` |
| 本地 P2P/Secret | `npc://vkey@1.2.3.4:8024?password=pw&target=127.0.0.1:22&local_port=2000` |
| 远程内容 | `https://example.com/npc.yaml` |
| 本地文件 | `@/etc/npc/launch.json` |
| Base64 | 以下任一内容的 Base64（标准或 URL 安全） |
| JSON / YAML / INI | 见下 |

npc URL 还支持 `server_addr`、`conn_type`、`proxy`、`proxy_url`、`local_ip`、`local_type`、`conn_mode`、`target_addr`、`local_proxy`、`name` 参数，未知参数会报错。

## JSON 与 YAML

顶层是一个配置、配置列表，或 `{"profiles": [...]}`：

```yaml
profiles:
  - name: direct            # 只有连接字段：用已有客户端的 vkey 直接连接
    server: a:8024,b:8024
    vkey: k1,k2
    type: tls
  - name: office            # 含 common 或下列列表：与配置文件模式相同
    common: {server_addr: a:8024, vkey: k3, conn_type: tls}
    hosts:
      - {remark: web, host: a.example.com, target_addr: [127.0.0.1:80], headers: {X-From: npc}}
    tasks:
      - {remark: ssh, mode: tcp, server_port: 2222, target_addr: 127.0.0.1:22}
    locals: []
```

- 连接字段：`server`、`vkey`、`type`、`proxy`、`local_ip`。
- `common`、`hosts`、`tasks`、`locals` 各项的键名与 [npc.conf](/reference/npc-config.md) 相同。
- `headers`、`response_headers` 对应 `header_*`、`response_*`。
- `remark` 不能以 `secret`、`p2p` 开头。

INI 内容就是配置文件本身，并按配置文件模式运行。

## 运行与回退

- 同一输入的多个配置一起运行，任一配置断开时整组 5 秒后重启，并重新读取来源。
- 远程来源获取或解析失败时继续使用内存中最后一份有效内容，并至少隔 30 秒再请求；首次启动没有可用内容时每 30 秒重试。
- 来源返回 `410 Gone` 表示已撤销：停止运行，5 分钟后重试。
- 最后一份有效内容不写入磁盘，npc 重启后需要来源可用。
- 非远程内容无效时直接退出。

## 安全

来源可以决定 npc 连接哪个服务器、开放哪些隧道，能让对方访问本机可达的服务。**只使用你信任的来源**，并尽量使用 https（http 会告警，https 不允许跳转到 http）。

远程内容限制 1 MiB，请求超时 15 秒，最多跟随 5 次跳转。它不执行命令，也不能：

- 使用模板，引用其他 URL 或本地文件；
- 设置 `dns_server`、`multi_account`、`cert_file`、`key_file`、`local_path`、`strip_pre`、`pprof_addr` 或 `mode=file`。

需要这些功能时使用本地配置文件。

此功能到 1.0 不会原样保留，见[版本规则](../../getting-started/versioning.md)。

点对点访问的本地服务器只接受新写法：`local_type`（`tcp_udp`、`tcp`、`udp`、`mixed`、`transparent`）和 `conn_mode`（`auto`、`relay`、`direct`）；`target_type`、`fallback_secret` 和旧的 `local_type` 取值会报错。
