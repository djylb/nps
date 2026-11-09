# 从源码编译

需要 Go 1.27 或更高版本。

```bash
git clone https://github.com/djylb/nps.git
cd nps

go build -trimpath -ldflags "-s -w" -o nps ./cmd/nps
go build -trimpath -ldflags "-s -w" -o npc ./cmd/npc
```

编译得到 `nps`、`npc` 两个程序。

nps 运行时要读取程序旁边的 `conf/nps.conf` 和 `web/views`、`web/static`，所以留在源码目录运行即可；要移到别处时把这三项一起带上。npc 只需要程序本身。

```bash
./nps
```

接下来按[快速开始](/getting-started/quick-start.md)的第 2 步继续，配置文件在 `conf/nps.conf`。

编译 C 动态库见 [NPC SDK](/reference/npc-sdk.md)。
