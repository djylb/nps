# npc 动态库（SDK）

SDK 把 npc 编译成 C 动态库，供其他语言（C、C++、Python、Android 原生层等）在自己的进程里启动和管理客户端。

## 编译

```bash
CGO_ENABLED=1 go build -tags sdk -buildmode=c-shared -o npc_sdk.so ./cmd/npc/sdk.go
```

会生成 `npc_sdk.so`（Windows 为 `.dll`）和头文件 `npc_sdk.h`。需要 C 编译器。发布包里的 `npc_sdk.tar.gz` 是各平台预编译好的版本。

## 约定

- 字符串参数是以 `\0` 结尾的 C 字符串；多个值用换行分隔，每行一个。
- 返回 `char*` 的函数（`Version`、`LastError`、`Logs`）返回的字符串由库分配，调用方用完后应释放（`free`），不要反复调用后不管。
- 动态库加载时日志默认只写在内存里（最近 1000 条，级别 `trace`），不输出到屏幕或文件，用 `Logs` 取走。
- 库内的客户端不自动重连：第一条连接结束后 `StartClient` 就返回，是否重连由调用方决定。

## 函数

### StartClient

```c
extern GoInt StartClient(char* launch, char* local);
```

按 [`-launch` 和 `-L`](/guide/client/launch.md) 同一套规则启动，写法见 [npc 启动链接与 -L 语法](/reference/npc-launch.md)。会阻塞，直到连接结束才返回。

- `launch`：一个或多个 `-launch` 的值（`npc://` 链接、`@文件`、`http(s)://` 地址、base64 启动包），每行一个。
- `local`：一个或多个 `-L` 的值，每行一个；没有本地访问传空字符串。有 `-L` 关联的连接只作访问者，没有的是普通客户端。
- 返回：`0` 表示输入无效（原因用 `LastError` 取），`1` 表示客户端运行过并已结束。

### StartClientByVerifyKey

```c
extern GoInt StartClientByVerifyKey(char* server, char* vkey, char* type, char* proxy);
```

只描述一条连接的简化写法。新代码请用 `StartClient`。会阻塞，直到连接结束才返回。

- `server`：服务端地址，如 `203.0.113.10:8024`。
- `vkey`：客户端密钥（网页“客户端”页里的验证密钥）。
- `type`：连接方式，如 `tcp`、`tls`、`quic`、`ws`、`wss`；取值范围见[传输与连接](/reference/features-transport.md)。
- `proxy`：连接服务端时使用的代理，如 `socks5://user:password@127.0.0.1:9007`；不用代理传空字符串。
- 返回：与 `StartClient` 相同，`0` 表示输入无效或没给服务端地址，`1` 表示运行过并已结束。

### GetClientStatus

```c
extern GoInt GetClientStatus();
```

`1` 表示已连上服务端，`0` 表示未连接或未启动。

### CloseClient

```c
extern void CloseClient();
```

停止正在运行的客户端并释放资源，阻塞中的 `StartClient` 随之返回。

### Version

```c
extern char* Version();
```

返回 npc 版本号。

### LastError

```c
extern char* LastError();
```

返回最近一次启动失败，或客户端结束的原因；没有错误时返回空字符串。`StartClient` 和 `StartClientByVerifyKey` 开始时会先清空它，所以应在它们返回后读取。返回的字符串同样需要调用方释放。

### SetLogsLevel

```c
extern void SetLogsLevel(char* level);
```

设置日志级别：`trace`、`debug`、`info`、`warn`、`error`、`off`，也可按模块写，如 `info,p2p=debug`。无法识别的值会被忽略。

### Logs

```c
extern char* Logs();
```

取走内存里还没取过的日志，每行一条，logfmt 格式；取走后不会再次返回。没有新日志时返回空字符串。

### SetDnsServer

```c
extern void SetDnsServer(char* dns);
```

设置解析服务端域名用的 DNS，如 `8.8.8.8` 或 `8.8.8.8:53`。

## 示例

```c
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>
#include <unistd.h>
#include "npc_sdk.h"

static void *client_thread(void *arg) {
    // YOUR_VKEY 在网页“客户端”页的“验证密钥”列取得
    int rc = StartClient("npc://YOUR_VKEY@203.0.113.10:8024?type=tls", "");
    if (rc == 0) {
        char *err = LastError();
        printf("启动失败：%s\n", err);
        free(err);
    }
    return NULL;
}

int main() {
    char *ver = Version();
    printf("版本：%s\n", ver);
    free(ver);

    pthread_t tid;
    pthread_create(&tid, NULL, client_thread, NULL);

    for (int i = 0; i < 10; i++) {
        sleep(5);
        char *logs = Logs();
        printf("%s", logs);
        free(logs);
        printf("状态：%s\n", GetClientStatus() == 1 ? "在线" : "离线");
    }

    CloseClient();
    pthread_join(tid, NULL);
    return 0;
}
```
