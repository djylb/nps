# NPC GUI 界面预览 / UI Preview

由于无法在 CI 环境中生成实际截图，这里提供界面的文字描述。

Since we cannot generate actual screenshots in CI environment, here's a text description of the UI.

## 主界面 / Main Interface

```
┌─────────────────────────────────────────────────────────────────┐
│ NPC GUI - NPS Client Manager          Version: 0.33.11          │
├─────────────────────────────────────────────────────────────────┤
│ [Configurations] [Logs]                                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Client Configurations                          [+ Add New]     │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Home Server                                              │  │
│  │ Server: example.com:8024  Type: TLS  ● Running          │  │
│  │                          [Stop] [Edit] [Delete]          │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Office Network                                           │  │
│  │ Server: 192.168.1.100:8024  Type: TCP  ○ Stopped        │  │
│  │                          [Start] [Edit] [Delete]         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Development Server                                       │  │
│  │ Server: dev.example.com:8024  Type: QUIC  ● Running     │  │
│  │                          [Stop] [Edit] [Delete]          │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 配置表单 / Configuration Form

```
┌─────────────────────────────────────────────────────────────────┐
│ Add New Configuration                            [Cancel]       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Configuration Name *                                            │
│  [________________________]                                      │
│                                                                  │
│  Server Address *                                                │
│  [________________________]                                      │
│  Format: host:port (e.g., example.com:8024)                     │
│                                                                  │
│  Verification Key *                                              │
│  [________________________]                                      │
│                                                                  │
│  Connection Type          Log Level                             │
│  [TCP ▼]                 [Info ▼]                               │
│                                                                  │
│  Proxy URL (Optional)                                            │
│  [________________________]                                      │
│  socks5://user:pass@127.0.0.1:9007                              │
│                                                                  │
│  DNS Server              Keep Alive (seconds)                   │
│  [8.8.8.8______]        [0__]                                   │
│                                                                  │
│  ☑ Auto Reconnect                                               │
│  ☐ Skip TLS Verification                                        │
│  ☐ Disable P2P                                                  │
│                                                                  │
│  [Save Configuration]  [Cancel]                                 │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 日志视图 / Logs View

```
┌─────────────────────────────────────────────────────────────────┐
│ NPC GUI - NPS Client Manager          Version: 0.33.11          │
├─────────────────────────────────────────────────────────────────┤
│ [Configurations] [Logs]                                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Client Logs                                    [Clear Logs]    │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ [2025-01-10 14:32:15] NPC Version: 0.33.11              │  │
│  │ [2025-01-10 14:32:15] Loaded 3 configurations           │  │
│  │ [2025-01-10 14:32:20] Starting client: Home Server      │  │
│  │ [2025-01-10 14:32:20] Connecting to server...           │  │
│  │ [2025-01-10 14:32:21] Connected successfully            │  │
│  │ [2025-01-10 14:32:21] Client Home Server started        │  │
│  │ [2025-01-10 14:35:10] Starting client: Dev Server       │  │
│  │ [2025-01-10 14:35:11] Connected successfully            │  │
│  │ [2025-01-10 14:35:11] Client Dev Server started         │  │
│  │                                                           │  │
│  │ ▼                                                         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 配色方案 / Color Scheme

**背景色 / Background Colors:**
- 主背景: `#1e1e1e` (深灰)
- 卡片背景: `#2d2d2d` (中灰)
- 边框: `#3d3d3d` (浅灰)

**强调色 / Accent Colors:**
- 主色: `#667eea` (紫蓝色)
- 渐变头部: `#667eea` → `#764ba2` (紫蓝到紫色)
- 成功: `#4caf50` (绿色)
- 危险: `#f44336` (红色)
- 警告: `#ff9800` (橙色)

**文字颜色 / Text Colors:**
- 主文字: `#e0e0e0` (浅灰白)
- 次要文字: `#a0a0a0` (灰色)
- 禁用: `#808080` (深灰)

## 状态指示器 / Status Indicators

- **运行中 / Running**: `● Running` (绿色圆点)
- **已停止 / Stopped**: `○ Stopped` (灰色空心圆)

## 按钮样式 / Button Styles

- **主要按钮 / Primary**: 紫蓝色背景，白色文字
- **次要按钮 / Secondary**: 灰色背景，白色文字
- **成功按钮 / Success**: 绿色背景，白色文字 (Start)
- **危险按钮 / Danger**: 红色背景，白色文字 (Stop, Delete)

## 响应式布局 / Responsive Layout

- 最小宽度: 1200px
- 最小高度: 800px
- 自动调整窗口大小
- 滚动条适配内容

## 交互效果 / Interactions

- **悬停 / Hover**: 按钮和卡片高亮
- **点击 / Click**: 立即响应
- **加载 / Loading**: 显示加载状态
- **错误 / Error**: 弹出错误提示

## 动画效果 / Animations

- 平滑过渡 (0.3s)
- 按钮悬停效果
- 卡片边框高亮
- 页面切换淡入淡出

## 可访问性 / Accessibility

- 清晰的对比度
- 足够的点击区域
- 键盘导航支持（待实现）
- 屏幕阅读器友好（待实现）

---

**注意 / Note**: 实际界面可能与描述略有不同，以实际运行效果为准。

The actual interface may differ slightly from the description. Please run the application to see the real UI.
