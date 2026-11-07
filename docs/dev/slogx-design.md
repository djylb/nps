# slogx 设计（准备拆成独立库）

slogx 是 `log/slog` 之上的一层：提供 handler、路由、事件与运维能力。它不另起一套日志 API，调用方只需要 `*slog.Logger`。目标是做成通用库：nps 0.35、新版 nps 和其他项目都直接用它，库里不出现任何项目名，只依赖标准库。

## 取舍：从 zap 和 zerolog 借鉴什么

| 能力 | zap | zerolog | slogx 的做法 |
| --- | --- | --- | --- |
| 调用 API | 自有 Field | 链式调用 | **用 slog**：标准接口，第三方库（holepunch 等）可直接接收 |
| 级别未开启时零开销 | Check 后再写 | 链式提前返回 | `Enabled` 先判断；`LogValuer` 延迟求值；编码缓冲复用 |
| 运行时调级别 | AtomicLevel，自带 HTTP 端点 | 全局级别 | 按模块设置的原子级别表，**支持带过期时间的临时调高**，并提供 `http.Handler` 用于查询和修改 |
| 多路输出 | Tee 多个 Core | MultiLevelWriter | **路由（Route）**：过滤条件、编码器、输出三者组合，可以配多条 |
| 采样 | Sampler（首 N 条后每 M 条取 1） | BasicSampler、BurstSampler | 两种都支持：按事件和关键字段**限频**，以及**首 N 后每 M** 采样，并记录被抑制的条数 |
| 钩子 | Hooks | Hook | 统一用 Route，`Sink` 也是一种 Route，用于把事件接到事件总线 |
| 调用位置与堆栈 | AddCaller、AddStacktrace | Caller()、Stack() | 可选：调用位置按级别开启；error 级别可附带堆栈 |
| 控制台美化 | 开发模式编码器 | ConsoleWriter 带颜色 | 提供 `Pretty` 编码器，供开发和终端使用，是否上色看是不是 tty |
| 缓冲与异步 | BufferedWriteSyncer | diode 无锁写入 | **可选异步输出**：有界队列，满了就丢弃并计数，不阻塞业务；`Flush` 和 `Close` 保证落盘 |
| 写失败处理 | ErrorOutput | ErrorHandler | 写失败降级到 stderr，并发出 `log_fallback` |
| 上下文传递 | — | `Ctx(ctx)` | 从 `context` 中提取关联字段（会话 ID、请求 ID、trace），通过 `ContextAttrs` 注入 |
| 测试 | zaptest/observer | — | `slogxtest.Recorder`：在内存里记录结构化记录，可按事件或字段断言 |
| 脱敏 | — | — | `Redacted` 类型，另可按字段名配置屏蔽表 |
| 网页查看 | — | — | `Memory`：保存最近 N 条结构化记录，支持查询（级别、模块、事件、时间、关键字、按序号增量拉取）和订阅（订阅端慢则丢弃并计数），网页通过 SSE 或 WebSocket 接入 |
| 固定事件 | — | — | `Def`：名字、级别、是否安全事件、是否进总线、限频规则；**一个定义同时供日志和总线使用** |

## 结构

```
Logger(slog) ──► handler ──► Record（已展平，带 *Def）
                               │
                     Pipeline：脱敏 → 采样/限频 → 路由
                               │
          ┌────────────────────┼────────────────────┐
     Route console        Route file           Route security / Sink / 自定义
     Filter: level        Filter: level        Filter: def.Security
     Encoder: Pretty      Encoder: logfmt      Encoder: logfmt
     Output: stderr       Output: Rotating     Output: Rotating / Bus
```

- **Record 带 Def 指针**：`Emit` 把 `*Def` 放进记录本身，不再按名字查全局注册表。各个库可以各自定义事件，互不冲突；注册表只用于导出事件清单和生成文档。
- **Filter**：可以按级别、按模块级别表、按事件（只要安全事件或总线事件）筛选，也可以组合。
- **Encoder** 是接口，内置 logfmt、JSON 和 Pretty 三种；字段顺序固定为 `time level event module … msg`。
- **Output** 是接口，内置 `Writer`（stderr/stdout）、`Rotating`（按大小或时间轮转，保留份数和天数，可选压缩，支持 `Reopen`）和 `Async`（包装另一个输出）。syslog、journald 由使用方自己实现。
- **Logs.Configure** 原子地替换整条流水线，已经创建的 Logger 不受影响，所以可以热重载。
- **级别表**：默认级别加按模块覆盖，比如 `info,p2p=debug`；`Raise(module, level, ttl)` 临时调高，到期自动恢复。

## 对外接口（草案）

```go
logs := slogx.New(slogx.Config{...})          // 或 slogx.Default
l := logs.Module("bridge")                // *slog.Logger
slogx.Emit(l, &AuthFailed, "src_ip", ip)    // 固定事件
logs.Configure(cfg)                       // 热替换
logs.Levels().Set("info,p2p=debug")
logs.Levels().Raise("p2p", slog.LevelDebug, 10*time.Minute)
logs.Handler()                            // 交给 slog.SetDefault 或第三方库
logs.Flush(); logs.Close()
slogx.HTTPErrorLog(l, "web")                // net/http
slogx.CaptureStdLog(l)
slogxtest.NewRecorder()                     // 测试
```

Config 可以用代码构建，也可以从一份简单的配置（JSON 或键值）解析，nps.conf 和新版 JSON 配置都能直接映射过来。

## 格式取舍（主流方案调研）

- JSON Lines（zerolog、zap 生产模式、容器日志、Loki、ES）：机器直接解析，保留类型；
- logfmt（Heroku、Grafana 系、slog TextHandler）：人能读、grep 和 fail2ban 友好；
- 终端美化：只给人看，不作为契约；
- 字段命名标准：OpenTelemetry Logs 语义约定、Elastic ECS，只定字段名、不定格式。

三种编码器都内置，字段完全一致，可以无损互转。终端（tty）默认用美化格式，文件和管道默认用 logfmt，`json` 可选。以后可以加 OTel 字段映射或 OTLP 输出，契约不变。

## 实现状态（2026-11-08）

已实现：`Record` 携带 `*Def`、三种编码器、`Output`/`Rotating`/`Async`/`WriterOutput`、`Route` 与 `Match`、`Levels`（`Set`/`Raise`/`Lower`/`ServeHTTP`）、限频与采样、`AddSource`、`StackFrom`、`Context`、`Memory`（查询与订阅）、`slogxtest.Recorder`、`HTTPErrorLog`、`CaptureStdLog`。

## 与 nps 的边界

- 事件表（logevent）、zap 桥接（certmagic）、命令行和配置文件的映射（logsetup）都留在 nps。
- 新版 nps 直接依赖这个库，§5.11 的契约（字段顺序、限频、安全日志、临时调高级别）由库保证。

## 迁移

当前 0.35 已在用的 `Module`、`Def`、`Emit`、`EmitMsg`、`Redacted`、`HTTPErrorLog`、`CaptureStdLog` 保持不变，只重构内部结构。完成后拆到独立仓库（拆分前先确认）。
