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

默认为关闭状态

