# nps 命令行

```text
nps [参数] [子命令]
```

不带子命令时，nps 在前台运行，按 `Ctrl+C` 停止。带子命令时，nps 执行一件事就退出（`service` 除外）。参数和子命令可以交错书写，`nps -c /etc/nps install` 与 `nps install -c /etc/nps` 一样。

## 参数

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `-c`、`--conf_path` | 空 | 指定配置文件或运行目录，见下文[配置文件在哪里](#配置文件在哪里) |
| `-v`、`--version` | 关 | 显示版本后退出，和子命令 `version` 一样 |
| `--gen2fa` | 关 | 生成一个双因素认证密钥并显示二维码后退出。把密钥填进 `nps.conf` 的 `totp_secret`，并用验证器应用扫描二维码 |
| `--get2fa=密钥` | 空 | 显示这个密钥当前的 6 位动态码后退出，用来在没有验证器应用时登录 |
| `--disable_remote_update` | 关 | 禁止从网页更新服务端。可写成 `--disable_remote_update` 或 `--disable_remote_update=true` |
| `-h`、`--help` | | 显示参数说明 |

书写上的宽容：

- 参数名里的 `-`、`.` 和 `_` 通用，`--conf-path` 与 `--conf_path` 一样。
- 单横线也能写长参数，`-conf_path=/etc/nps` 等同于 `--conf_path=/etc/nps`。
- 不认识的参数（比如旧版本留下的）会被忽略并在日志里提示一条警告，不会让 nps 启动失败。这样已安装的服务在升级后不会因为某个旧参数起不来。
- 参数 `--` 之后的内容原样保留，不再解析。

`--gen2fa`、`--get2fa`、`--version` 不读取配置文件。

## 子命令

| 子命令 | 需要管理员权限 | 作用 |
| --- | --- | --- |
| （无） | 否 | 前台运行 |
| `install` | 是 | 安装并注册系统服务，见下文[install](#install) |
| `uninstall` | 是 | 删除系统服务，不删除程序和配置文件 |
| `start` | 是 | 启动系统服务 |
| `stop` | 是 | 停止系统服务 |
| `restart` | 是 | 重启系统服务 |
| `update` | 是 | 更新到最新版本，已是最新则什么也不做，见下文[update 和 update-force](#update-和-update-force) |
| `update-force` | 是 | 强制更新，已是最新或更新版本时也重新安装一次最新版 |
| `update-geo [geoip\|geosite]` | 视文件位置 | 更新 IP 归属地数据，见下文[update-geo](#update-geo) |
| `version` | 否 | 显示版本，和 `-v` 一样 |
| `service` | | 供系统服务管理器调用的内部子命令，运行 nps 本体，不要手动使用 |

写错的子命令会打印用法并以状态码 `2` 退出。

`start`、`stop`、`restart`、`uninstall` 只操作已经注册的系统服务；服务名是 `Nps`。没有注册服务时它们会在日志里报错。Windows 请在管理员权限的命令行里运行，用法见[在 Windows 上安装](/getting-started/install-windows.md)。

### install

`nps install` 做这几件事：

1. 先停止并删除已有的同名服务，所以重复执行 `install` 就是重新安装。
2. 把网页文件（`web/views`、`web/static`）和参考配置（`conf/nps.conf.default`）复制到安装目录。安装目录不存在时，还会把解压目录的整个 `conf` 目录复制过去；已存在时保留里面的配置文件。
3. Linux 和 macOS 上把 `nps` 程序复制到 `/usr/bin`（不行再试 `/usr/local/bin`），之后在任何位置都能运行 `nps`。Windows 上程序留在原处。
4. 注册系统服务。

安装目录默认是 `/etc/nps`（Windows 为 `C:\Program Files\nps`）。用 `-c` 给了目录时，安装到那个目录。

`install` 时写在命令行上的参数会保存进服务，服务每次启动都带着它们。例如 `sudo nps -c /app/nps install` 注册的服务，等同于运行 `nps -c /app/nps service`。要改参数，重新执行一次 `install`。

除了 `update`、`update-force`、`version` 和 `--gen2fa`、`--get2fa` 之外，其余子命令（包括 `install`、`start`、`stop`、`restart`、`uninstall`）都会先读取配置文件。配置读不到或有语法错误时它们会失败，错误信息打印在屏幕上。

### update 和 update-force

`update` 下载最新发布版，验证签名并检查新程序能在本机运行，然后一次性替换程序、网页文件和参考配置 `conf/nps.conf.default`。你自己的 `nps.conf` 和数据文件不会被改动。正在运行的系统服务会自动重启，不必先停止。更新失败或新版本没有确认正常启动时，会自动回滚。

`update-force` 在已是最新版、甚至当前版本更新时也会重新安装最新版，用来修复损坏的安装。

这两个子命令不读取配置文件，所以配置文件损坏时也能用来换上新版本。Linux 和 macOS 上需要对程序所在目录有写权限，通常用 `sudo`。更新的规则、跨大版本的限制见[运维](/guide/server/operations.md)。

### update-geo

`nps update-geo` 更新访问控制名单用到的 IP 归属地数据：

- 不带名字：更新 `geoip.dat`，以及磁盘上已存在的 `geosite.dat`。
- `nps update-geo geoip` 或 `nps update-geo geosite`：只更新指定的一个。`geosite.dat` 不存在时不会自动下载，要用 `geosite` 明确指定。

文件位置由配置里的 `geoip_path`、`geosite_path` 决定（默认 `conf/geoip.dat`、`conf/geosite.dat`，相对运行目录）。已是最新时显示 `Geodata is up to date`。运行中的 nps 会在一分钟内用上新文件，不用重启。

## 配置文件在哪里

nps 先确定**运行目录**，再在里面找配置文件。数据文件（`conf/clients.json`、`conf/tasks.json`、`conf/hosts.json`、`conf/global.json`）、网页文件（`web/`）和配置里的相对路径也都以运行目录为准。

### 不用 -c 时

运行目录按下面的顺序确定：

1. 命令行**完全没有参数**（直接运行 `./nps`）：运行目录是程序所在的目录。适合解压后直接运行。
2. 其他情况（带了任何参数或子命令，包括系统服务运行的 `nps service`）：如果安装目录存在，运行目录就是安装目录（Linux/macOS `/etc/nps`，Windows `C:\Program Files\nps`）；不存在则是程序所在的目录。

配置文件按下面的顺序找，用第一个能读成功的：

1. `<运行目录>/conf/nps.conf`、`nps.ini`、`nps.yaml`、`nps.yml`、`nps.json`
2. `<程序所在目录>/conf/nps.conf`、`nps.ini`、`nps.yaml`、`nps.yml`、`nps.json`

某个文件存在但读不了（比如语法错误）时，会接着试后面的候选，全部失败才报错，错误信息会列出每个候选的失败原因。

安装后在命令行直接输入 `nps`（没有参数）时，运行目录是 `nps` 程序所在的 `/usr/bin`，找不到配置文件。安装之后请用 `sudo nps start` 启动服务；要前台运行，加上目录参数：`nps -c /etc/nps`。

### 用 -c 时

`-c` 的值按扩展名区分：

| 写法 | 例子 | 含义 |
| --- | --- | --- |
| 以 `.conf`、`.ini`、`.yaml`、`.yml`、`.json` 结尾 | `-c /app/nps/conf/nps.conf` | **配置文件**。优先读这个文件，其余候选仍作后备。这个文件所在的目录被当作运行目录 |
| 其他 | `-c /app/nps` | **运行目录**。配置文件在 `/app/nps/conf/nps.conf`。目录不存在时退回程序所在的目录 |

推荐写运行目录。写成配置文件时要留意：它所在的目录整个成了运行目录，数据文件会放到那个目录下的 `conf/` 里。例如 `-c /app/nps/conf/nps.conf` 会把数据放进 `/app/nps/conf/conf/`，配置里的 `conf/server.pem` 也会去 `/app/nps/conf/conf/` 找。所以配置文件放在 `<运行目录>/conf/nps.conf` 的标准结构时，直接写 `-c <运行目录>`。

`-c` 同时决定 `install` 的安装目录，见上文。

配置文件各项的含义见[服务端配置](/reference/server-config.md)。

## 信号

仅限 Linux 和 macOS，Windows 没有这两个信号。

| 信号 | 作用 |
| --- | --- |
| `SIGUSR1` | 重新读取配置文件，等同于网页“全局参数”里的“重新加载配置”。哪些项能立即生效见[运维](/guide/server/operations.md) |
| `SIGHUP` | 重新打开日志文件，配合 logrotate 等工具移走旧日志后使用 |

## 退出状态

| 状态码 | 含义 |
| --- | --- |
| `0` | 正常 |
| `1` | 读取配置失败、日志设置错误、启动失败或更新失败 |
| `2` | 子命令不认识 |
