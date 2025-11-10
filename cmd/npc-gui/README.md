# NPC GUI - NPS Client Manager

一个基于 Wails 和 Go 开发的跨平台 NPC (NPS Client) 配置管理桌面 GUI 应用程序。

A cross-platform desktop GUI application for managing NPC (NPS Client) configurations built with Wails and Go.

## 功能特性 / Features

- **多配置管理**: 创建、编辑和管理多个 NPC 客户端配置 / Create, edit, and manage multiple NPC client configurations
- **简易连接控制**: 一键启动和停止客户端 / Start and stop clients with a single click
- **实时日志**: 实时查看客户端日志 / View client logs in real-time
- **现代化界面**: 使用现代 Web 技术构建的简洁直观界面 / Clean and intuitive interface built with modern web technologies
- **跨平台**: 支持 Windows、Linux 和 macOS / Runs on Windows, Linux, and macOS

## 配置选项 / Configuration Options

GUI 支持所有主要的 NPC 配置选项 / The GUI supports all major NPC configuration options:

- **连接设置 / Connection Settings**:
  - 服务器地址 (host:port) / Server address (host:port)
  - 验证密钥 (vkey) / Verification key (vkey)
  - 连接类型 (TCP, TLS, KCP, QUIC, WebSocket, WebSocket TLS) / Connection type
  
- **高级选项 / Advanced Options**:
  - 代理 URL (SOCKS5 代理支持) / Proxy URL (SOCKS5 proxy support)
  - DNS 服务器配置 / DNS server configuration
  - 保持连接间隔 / Keep-alive interval
  - 自动重连 / Auto reconnect
  - 跳过 TLS 验证 / Skip TLS verification
  - 禁用 P2P 连接 / Disable P2P connections
  
- **日志 / Logging**:
  - 可配置的日志级别 (Trace, Debug, Info, Warn, Error) / Configurable log levels
  - 实时日志查看器 / Real-time log viewer

## 从源码构建 / Building from Source

### 前置要求 / Prerequisites

- Go 1.25 或更高版本 / Go 1.25 or later
- Node.js 18+ 和 npm / Node.js 18+ and npm
- Wails CLI v2.11.0 或更高版本 / Wails CLI v2.11.0 or later

#### Linux 前置要求

```bash
# Ubuntu/Debian
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev

# Fedora
sudo dnf install gtk3-devel webkit2gtk4.1-devel

# Arch Linux
sudo pacman -S webkit2gtk-4.1
```

#### macOS 前置要求

```bash
# macOS 无需额外依赖 / No additional dependencies required for macOS
```

#### Windows 前置要求

```bash
# Windows 无需额外依赖 / No additional dependencies required for Windows
```

### 安装 Wails CLI / Install Wails CLI

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### 构建应用程序 / Build the Application

```bash
cd cmd/npc-gui

# 开发构建 / Development build
wails dev

# 生产构建 / Production build
wails build

# Linux 使用 webkit2gtk-4.1 的生产构建
# Production build for Linux with webkit2gtk-4.1
wails build -tags webkit2gtk_4_1
```

编译后的二进制文件位于 `build/bin/` / The compiled binary will be located in `build/bin/`.

## 使用方法 / Usage

### 启动应用程序 / Starting the Application

双击可执行文件或从终端运行 / Double-click the executable or run from terminal:

```bash
./npc-gui
```

### 添加配置 / Adding a Configuration

1. 点击"添加新配置"按钮 / Click "Add New" button
2. 填写配置详情 / Fill in the configuration details:
   - 配置名称（用于识别）/ Configuration name (for identification)
   - 服务器地址（例如 example.com:8024）/ Server address (e.g., example.com:8024)
   - 验证密钥 / Verification key
   - 连接类型 / Connection type
   - 其他可选设置 / Other optional settings
3. 点击"保存配置"/ Click "Save Configuration"

### 启动客户端 / Starting a Client

1. 在列表中找到您的配置 / Find your configuration in the list
2. 点击"启动"按钮 / Click the "Start" button
3. 状态将变为"运行中"/ The status will change to "Running"
4. 在"日志"标签中查看日志 / View logs in the "Logs" tab

### 停止客户端 / Stopping a Client

1. 找到正在运行的配置 / Find the running configuration
2. 点击"停止"按钮 / Click the "Stop" button
3. 客户端将优雅地断开连接 / The client will disconnect gracefully

## 配置存储 / Configuration Storage

配置存储在 / Configurations are stored in:
- Linux: `~/.npc-gui/configs.json`
- macOS: `~/.npc-gui/configs.json`
- Windows: `%USERPROFILE%\.npc-gui\configs.json`

## 开发 / Development

### 项目结构 / Project Structure

```
cmd/npc-gui/
├── app.go              # 主应用结构和方法 / Main app structure and methods
├── main.go             # 应用程序入口点 / Application entry point
├── npc_service.go      # NPC 客户端管理服务 / NPC client management service
├── frontend/           # 前端 Web 应用 / Frontend web application
│   ├── src/
│   │   ├── main.js     # 主 JavaScript 代码 / Main JavaScript code
│   │   └── style.css   # 样式 / Styling
│   ├── index.html      # HTML 模板 / HTML template
│   └── package.json    # 前端依赖 / Frontend dependencies
├── go.mod              # Go 依赖 / Go dependencies
└── wails.json          # Wails 配置 / Wails configuration
```

### 开发模式运行 / Running in Development Mode

```bash
cd cmd/npc-gui
wails dev
```

这将启动应用程序并为前端更改启用热重载。
This will start the application with hot-reload enabled for frontend changes.

### 前端开发 / Frontend Development

前端使用原生 JavaScript 和 Vite 构建。要开发前端：
The frontend is built with vanilla JavaScript and Vite. To work on the frontend:

```bash
cd frontend
npm install
npm run dev
```

### 后端开发 / Backend Development

后端使用 Go 编写，并使用主要的 NPS 客户端库。关键文件：
The backend is written in Go and uses the main NPS client libraries. Key files:

- `app.go`: 向前端暴露方法 / Exposes methods to the frontend
- `npc_service.go`: 管理 NPC 客户端生命周期和配置 / Manages NPC client lifecycle and configurations

## 与 NPC 集成 / Integration with NPC

此 GUI 应用程序直接与主仓库中的 NPC 客户端代码集成。它使用相同的客户端库，支持与命令行 NPC 客户端相同的所有功能。

This GUI application integrates directly with the NPC client code from the main repository. It uses the same client libraries and supports all the same features as the command-line NPC client.

## 故障排除 / Troubleshooting

### Linux: 缺少 webkit2gtk

如果在 Linux 上遇到 webkit2gtk 错误，请安装开发库：

If you encounter webkit2gtk errors on Linux, install the development libraries:

```bash
# Ubuntu 24.04 及更新版本 / Ubuntu 24.04 and newer
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev

# 较旧的 Ubuntu 版本 / Older Ubuntu versions
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.0-dev
```

### 构建失败，出现 CGO 错误 / Build fails with CGO errors

确保已安装 C 编译器 / Make sure you have a C compiler installed:

```bash
# Linux
sudo apt-get install build-essential

# macOS
xcode-select --install
```

### 应用程序无法启动 / Application won't start

检查是否已安装所需的依赖项，以及二进制文件是否具有执行权限：

Check that you have the required dependencies installed and that the binary has execute permissions:

```bash
chmod +x npc-gui
./npc-gui
```

## 贡献 / Contributing

欢迎贡献！请确保 / Contributions are welcome! Please ensure:

1. 代码遵循 Go 最佳实践 / Code follows Go best practices
2. 前端代码简洁且有良好的注释 / Frontend code is clean and well-commented
3. 新功能包含适当的错误处理 / New features include appropriate error handling
4. 在提交之前测试您的更改 / Test your changes before submitting

## 许可证 / License

此项目是 NPS 项目的一部分，遵循相同的许可证。

This project is part of the NPS project and follows the same license.

## 相关项目 / Related Projects

- [NPS 服务器 / NPS Server](../../): 主 NPS 服务器和客户端 / Main NPS server and client
- [NPS Android 客户端 / NPS Android Client](https://github.com/djylb/npsclient): Android 客户端
- [NPS OpenWrt](https://github.com/djylb/nps-openwrt): OpenWrt 软件包 / OpenWrt packages
