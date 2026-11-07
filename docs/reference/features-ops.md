# 日志与调试

## 日志输出

日志输出级别

(trace|debug|info|warn|error|fatal|panic|off)

**对于npc：**

```
-log_level=info -log_path=npc.log
```

`-debug` 默认开启，此时日志输出到控制台且级别为 `trace`；要写入文件请同时加 `-debug=false`。

**对于nps：**

在`nps.conf`中设置相关配置即可

## pprof性能分析与调试

可在服务端与客户端配置中开启pprof端口，用于性能分析与调试，注释或留空相应参数为关闭。

默认为关闭状态。

开启后常用的地址（以 `127.0.0.1:9999` 为例）：

- `http://127.0.0.1:9999/debug/pprof/goroutine?debug=2`：所有 goroutine 的栈。服务端客户端连接、隧道和 npc 本地服务的 goroutine 带有标签，例如 `{bridge: tcp, client: 3}`、`{client: 3, mode: tcp, task: 7}`、`{local: ...}`，可以直接看出属于哪个客户端或隧道。程序崩溃时打印的栈也带这些标签。
- `http://127.0.0.1:9999/debug/pprof/goroutineleak`：找出永久阻塞、无法再被唤醒的 goroutine（泄漏）。请求时会触发一次垃圾回收，访问频率不宜过高。

