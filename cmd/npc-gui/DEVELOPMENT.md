# NPC GUI 开发指南 / Development Guide

本文档提供 NPC GUI 的详细开发指南。

This document provides a detailed development guide for NPC GUI.

## 架构概述 / Architecture Overview

NPC GUI 使用 Wails v2 框架构建，结合了 Go 后端和现代 Web 前端。

NPC GUI is built using the Wails v2 framework, combining a Go backend with a modern web frontend.

### 技术栈 / Technology Stack

**后端 / Backend:**
- Go 1.25+
- Wails v2.11.0
- NPS Client 库 / NPS Client libraries

**前端 / Frontend:**
- Vanilla JavaScript
- Vite (构建工具 / Build tool)
- CSS3 (自定义样式 / Custom styling)

### 目录结构 / Directory Structure

```
cmd/npc-gui/
├── app.go                  # Wails 应用主结构 / Main Wails app structure
├── main.go                 # 应用入口点 / Application entry point
├── npc_service.go          # NPC 客户端管理服务 / NPC client management service
├── build.sh                # Linux/macOS 构建脚本 / Build script for Linux/macOS
├── build.bat               # Windows 构建脚本 / Build script for Windows
├── go.mod                  # Go 模块依赖 / Go module dependencies
├── wails.json              # Wails 配置文件 / Wails configuration
├── frontend/               # 前端源码 / Frontend source code
│   ├── src/
│   │   ├── main.js        # 主应用逻辑 / Main application logic
│   │   ├── style.css      # 全局样式 / Global styles
│   │   └── app.css        # 应用样式 / Application styles
│   ├── index.html         # HTML 入口 / HTML entry point
│   ├── package.json       # NPM 依赖 / NPM dependencies
│   └── wailsjs/           # Wails 生成的绑定 / Wails generated bindings
└── build/                  # 构建资源和输出 / Build assets and output
    └── bin/               # 编译后的二进制文件 / Compiled binaries
```

## 核心组件 / Core Components

### 1. NPCService (npc_service.go)

NPCService 是核心服务，负责管理 NPC 客户端的生命周期。

NPCService is the core service responsible for managing NPC client lifecycle.

**主要功能 / Key Features:**

```go
type NPCService struct {
    ctx          context.Context
    cancel       context.CancelFunc
    configs      map[string]*NPCConfig  // 配置存储 / Configuration storage
    clients      map[string]*client.TRPClient  // 活动客户端 / Active clients
    configsMutex sync.RWMutex           // 配置锁 / Config lock
    clientsMutex sync.RWMutex           // 客户端锁 / Client lock
    configDir    string                 // 配置目录 / Config directory
    logBuffer    []string               // 日志缓冲 / Log buffer
    logMutex     sync.RWMutex           // 日志锁 / Log lock
}
```

**方法 / Methods:**

- `SaveConfig(cfg NPCConfig)`: 保存配置 / Save configuration
- `DeleteConfig(id string)`: 删除配置 / Delete configuration
- `StartClient(id string)`: 启动客户端 / Start client
- `StopClient(id string)`: 停止客户端 / Stop client
- `GetLogs()`: 获取日志 / Get logs

### 2. App (app.go)

App 结构体暴露给前端的方法。

The App struct exposes methods to the frontend.

**暴露的方法 / Exposed Methods:**

```go
func (a *App) GetVersion() string
func (a *App) ListConfigs() []NPCConfig
func (a *App) SaveConfig(cfg NPCConfig) error
func (a *App) DeleteConfig(id string) error
func (a *App) StartClient(id string) error
func (a *App) StopClient(id string) error
func (a *App) GetClientStatus(id string) map[string]interface{}
func (a *App) GetLogs() []string
func (a *App) ClearLogs()
```

这些方法通过 Wails 自动生成的绑定在前端可用。

These methods are available in the frontend through Wails auto-generated bindings.

### 3. 前端架构 / Frontend Architecture

前端使用原生 JavaScript 构建，无需额外框架。

The frontend is built with vanilla JavaScript, no additional frameworks needed.

**主要模块 / Main Modules:**

```javascript
// 状态管理 / State Management
let currentConfigs = []    // 配置列表 / Configuration list
let currentView = 'list'   // 当前视图 / Current view
let editingConfig = null   // 正在编辑的配置 / Config being edited

// 视图渲染 / View Rendering
function renderConfigList()  // 渲染配置列表 / Render config list
function renderConfigForm()  // 渲染配置表单 / Render config form
function renderLogs()        // 渲染日志视图 / Render logs view

// API 调用 / API Calls
import { GetVersion, ListConfigs, SaveConfig, ... } from '../wailsjs/go/main/App'
```

## 开发工作流 / Development Workflow

### 启动开发环境 / Starting Development Environment

```bash
# 方法 1: 使用构建脚本 / Method 1: Using build script
./build.sh dev

# 方法 2: 直接使用 Wails / Method 2: Using Wails directly
wails dev
```

开发模式特性 / Development mode features:
- 前端热重载 / Frontend hot reload
- 后端自动重编译 / Backend auto recompile
- 浏览器开发者工具 / Browser dev tools

### 修改前端 / Modifying Frontend

1. 编辑 `frontend/src/main.js` 或 `frontend/src/style.css`
   Edit `frontend/src/main.js` or `frontend/src/style.css`

2. 保存后自动重载 / Changes reload automatically on save

3. 在浏览器中查看变化 / View changes in browser

### 修改后端 / Modifying Backend

1. 编辑 `.go` 文件 / Edit `.go` files

2. Wails 会自动重编译 / Wails will auto-recompile

3. 如果添加了新的导出方法，运行 / If you add new exported methods, run:
   ```bash
   wails generate module
   ```

### 添加新功能 / Adding New Features

#### 步骤 1: 后端方法 / Step 1: Backend Method

在 `app.go` 中添加新方法 / Add new method in `app.go`:

```go
func (a *App) MyNewFeature(param string) string {
    // 实现逻辑 / Implementation
    return result
}
```

#### 步骤 2: 生成绑定 / Step 2: Generate Bindings

```bash
wails generate module
```

这会在 `frontend/wailsjs/` 中生成 TypeScript/JavaScript 绑定。

This generates TypeScript/JavaScript bindings in `frontend/wailsjs/`.

#### 步骤 3: 前端调用 / Step 3: Frontend Call

```javascript
import { MyNewFeature } from '../wailsjs/go/main/App';

MyNewFeature("parameter").then(result => {
    console.log(result);
});
```

## 构建和打包 / Building and Packaging

### 开发构建 / Development Build

```bash
./build.sh dev
# 或 / or
wails dev
```

### 生产构建 / Production Build

```bash
# Linux/macOS
./build.sh

# Windows
build.bat
```

### 跨平台构建 / Cross-Platform Build

```bash
# 为 Windows 构建（在 Linux/macOS 上）/ Build for Windows (on Linux/macOS)
wails build -platform windows/amd64

# 为 macOS 构建 / Build for macOS
wails build -platform darwin/universal

# 为 Linux 构建 / Build for Linux
wails build -platform linux/amd64
```

## 调试技巧 / Debugging Tips

### 后端调试 / Backend Debugging

1. 使用 `log.Printf()` 或 NPS 的日志系统 / Use `log.Printf()` or NPS logging

2. 在 `npc_service.go` 中添加日志 / Add logs in `npc_service.go`:
   ```go
   s.addLog(fmt.Sprintf("Debug: %v", value))
   ```

3. 使用 Go 调试器 / Use Go debugger:
   ```bash
   dlv debug
   ```

### 前端调试 / Frontend Debugging

1. 在开发模式下打开浏览器开发者工具 / Open browser dev tools in dev mode

2. 使用 `console.log()` 输出调试信息 / Use `console.log()` for debug output

3. 在浏览器中检查网络请求 / Inspect network requests in browser

### 常见问题 / Common Issues

**问题**: 前端无法调用后端方法 / Issue: Frontend can't call backend methods

**解决**: 确保运行了 `wails generate module` / Solution: Make sure you ran `wails generate module`

**问题**: 构建失败，CGO 错误 / Issue: Build fails with CGO errors

**解决**: 安装必要的系统依赖 / Solution: Install required system dependencies

**问题**: 配置未保存 / Issue: Configurations not saving

**解决**: 检查配置目录权限 / Solution: Check config directory permissions

## 性能优化 / Performance Optimization

### 前端优化 / Frontend Optimization

1. **最小化 DOM 操作** / Minimize DOM operations:
   ```javascript
   // 差 / Bad
   for (let config of configs) {
       document.getElementById('list').innerHTML += createItem(config)
   }
   
   // 好 / Good
   const fragment = document.createDocumentFragment()
   for (let config of configs) {
       fragment.appendChild(createItem(config))
   }
   document.getElementById('list').appendChild(fragment)
   ```

2. **使用事件委托** / Use event delegation:
   ```javascript
   // 在父元素上监听 / Listen on parent element
   document.getElementById('list').addEventListener('click', (e) => {
       if (e.target.matches('.btn-start')) {
           // 处理点击 / Handle click
       }
   })
   ```

### 后端优化 / Backend Optimization

1. **使用适当的锁** / Use appropriate locks:
   - 读多写少：`sync.RWMutex` / Read-heavy: `sync.RWMutex`
   - 写多：`sync.Mutex` / Write-heavy: `sync.Mutex`

2. **避免阻塞主 goroutine** / Avoid blocking main goroutine:
   ```go
   go func() {
       // 长时间运行的操作 / Long-running operation
   }()
   ```

## 测试 / Testing

### 单元测试 / Unit Tests

```bash
# 运行所有测试 / Run all tests
go test ./...

# 运行特定测试 / Run specific test
go test -v -run TestSaveConfig
```

### 集成测试 / Integration Tests

使用 `wails dev` 手动测试所有功能。

Use `wails dev` to manually test all features.

## 贡献指南 / Contributing Guidelines

1. **代码风格** / Code Style:
   - 遵循 Go 标准格式 / Follow Go standard formatting
   - 使用 `gofmt` 和 `golint` / Use `gofmt` and `golint`
   - JavaScript 使用 2 空格缩进 / 2 spaces for JavaScript

2. **提交消息** / Commit Messages:
   - 使用清晰的描述性消息 / Use clear, descriptive messages
   - 格式：`类型: 描述` / Format: `type: description`
   - 例如：`feat: add new connection type` / Example: `feat: add new connection type`

3. **测试** / Testing:
   - 添加功能前编写测试 / Write tests before adding features
   - 确保所有测试通过 / Ensure all tests pass
   - 测试边界情况 / Test edge cases

## 资源链接 / Resource Links

- [Wails 文档 / Wails Documentation](https://wails.io/)
- [Go 文档 / Go Documentation](https://golang.org/doc/)
- [NPS 项目 / NPS Project](https://github.com/djylb/nps)

## 获取帮助 / Getting Help

如果遇到问题 / If you encounter issues:

1. 查看本文档 / Check this documentation
2. 搜索现有 Issues / Search existing issues
3. 提交新 Issue / Create a new issue
4. 加入 Telegram 群组讨论 / Join Telegram group for discussion

## 未来计划 / Future Plans

- [ ] 添加配置导入/导出功能 / Add config import/export
- [ ] 支持配置文件编辑 / Support config file editing
- [ ] 添加连接测试功能 / Add connection test feature
- [ ] 支持主题切换 / Support theme switching
- [ ] 添加系统托盘支持 / Add system tray support
- [ ] 实现自动更新 / Implement auto-update
