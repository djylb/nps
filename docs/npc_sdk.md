# NPC SDK 文档

---

## 接口说明

### 1. StartClientByVerifyKey

**功能描述：**  
此函数会阻塞，直到客户端退出返回，请自行管理是否重连

**参数：**  
- **p0 (char\*)** ：连接地址，格式示例 `"192.168.1.100:8080"`。
- **p1 (char\*)** ：验证密钥（vkey），用于身份认证。
- **p2 (char\*)** ：连接类型，支持 `"tcp"`、`"tls"`、`"udp"`。
- **p3 (char\*)** ：连接代理地址，格式示例 `"socks5://user:password@127.0.0.1:9007"`。如不使用代理，请传入空字符串。

**返回值：**  
- 返回 `1` 表示启动成功。

**接口原型：**
```c
extern GoInt StartClientByVerifyKey(char* p0, char* p1, char* p2, char* p3);
```

---

### 2. GetClientStatus

**功能描述：**  
查询当前客户端状态。  
- 在线状态返回 `1`。  
- 离线或未启动状态返回 `0`。

**接口原型：**
```c
extern GoInt GetClientStatus();
```

---

### 3. CloseClient

**功能描述：**  
关闭正在运行的内网穿透客户端，并释放相关资源。

**接口原型：**
```c
extern void CloseClient();
```

---

### 4. Version

**功能描述：**  
获取当前客户端版本信息，返回一个 C 字符串指针，包含版本号（例如 `"v0.26.10"`）。

**接口原型：**
```c
extern char* Version();
```

---

### 5. SetLogsLevel

**功能描述：**  
配置日志输出等级
- 支持传入 "trace"|"debug"|"info"|"warn"|"error"|"fatal"|"panic"|"disable"

**接口原型：**
```c
extern void SetLogsLevel(char* logsLevel);
```

---

### 6. Logs

**功能描述：**  
获取客户端日志信息，返回一个 C 字符串指针，适用于调试和运行时监控。

**接口原型：**
```c
extern char* Logs();
```

---

### 7. SetDnsServer

**功能描述：**  
配置解析服务器域名的DNS 
- 支持传入 `8.8.8.8` 或 `8.8.8.8:53`

**接口原型：**
```c
extern void SetDnsServer(char* dns);
```

---

## 示例用法

```c
#include <stdio.h>
#include <pthread.h>
#include <unistd.h>
#include "npc_sdk.h"

// 线程函数，用于启动客户端（阻塞式调用）
void* client_thread(void *arg) {
    char *serverAddr = "192.168.1.100:8080";
    char *vkey      = "your_vkey_here";
    char *connType  = "tcp";
    char *proxy     = "";
    
    // 阻塞式启动客户端
    StartClientByVerifyKey(serverAddr, vkey, connType, proxy);
    return NULL;
}

int main() {
    // 在启动前输出客户端版本信息
    char *versionInfo = Version();
    printf("客户端版本：%s\n", versionInfo);

    // 启动客户端线程（启动后该线程会阻塞，直到客户端退出）
    pthread_t tid;
    if (pthread_create(&tid, NULL, client_thread, NULL) != 0) {
        perror("创建客户端线程失败");
        return -1;
    }

    // 主线程循环获取日志快照，输出10次
    for (int i = 0; i < 10; i++) {
        char *logInfo = Logs();
        printf("当前日志（第 %d 次）：\n%s\n", i + 1, logInfo);
        sleep(5);
    }

    // 获取并输出当前客户端状态
    int status = GetClientStatus();
    printf("客户端状态：%s\n", status == 1 ? "在线" : "离线");

    // 关闭客户端
    CloseClient();
    printf("调用关闭客户端接口\n");

    // 等待一段时间以确保客户端关闭，然后再次获取状态
    sleep(2);
    status = GetClientStatus();
    printf("关闭后客户端状态：%s\n", status == 1 ? "在线" : "离线");

    // 等待客户端线程退出
    pthread_join(tid, NULL);

    return 0;
}
```

