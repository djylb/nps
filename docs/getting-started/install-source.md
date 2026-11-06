# 源码编译

需要 Go 1.27 或更高版本。

```bash
git clone https://github.com/djylb/nps.git
cd nps

go build -trimpath -ldflags "-s -w" -o nps ./cmd/nps
go build -trimpath -ldflags "-s -w" -o npc ./cmd/npc
```

也可以使用仓库的 `make`。编译后运行 `./nps`、`./npc`。

`nps` 运行时还需要 `conf/` 和 `web/` 目录（`web/views`、`web/static`），请从发布包或源码目录一起部署。

编译 C 动态库见 [NPC SDK](/reference/npc-sdk.md)。
