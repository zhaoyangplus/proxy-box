# Proxy Box - 多端 AI IDE 免 TUN 代理工作台

专为 Windows 用户打造的轻量单文件桌面与系统托盘工具，同时支持 **Clash Verge (Rev)**、**Clash Mi** 与 **v2rayN (v6 / v7+)** 三大主流代理客户端，让你在**不开启全局虚拟网卡（TUN 模式）**的前提下，一键打通 **Cursor**、**Google Antigravity**、**Claude Code** 与 **OpenAI Codex**，支持独立入站端口双向节点隔离，并在切换订阅/配置时实时自动优选低延迟合规节点。

## 支持的代理客户端

| 代理客户端 | 自动端口与状态识别机制 | AI 专属分流规则支持 |
| :--- | :--- | :--- |
| **Clash Verge (Rev)** | 自动读取 `verge.yaml` / `config.yaml` 混合端口（如 `7897`）及激活订阅 | **全自动注入/还原**全局 `Script.js`（建立 `🤖 AI-IDE` 组并剔除香港节点） |
| **Clash Mi** | 自动扫描 `%APPDATA%\clashmi`、内核监听端口及系统代理，并实测 HTTP 协议握手 | **一键生成并复制** Mihomo 防香港 JS 智能覆写脚本与 YAML 覆写规则 |
| **v2rayN (v6 / v7+)** | 自动解析 `guiConfigs\guiNConfig.json`，自动区分 v6 双端口（`10809` HTTP）与 v7 合并混合端口（`10808`）并实测 HTTP 协议 | **一键生成并复制** v2rayN 原生「从剪贴板导入规则」JSON 数组（含进程、域名与本地回环保护） |

## 核心功能

1. **多客户端智能优选与自由切换**
   - 默认「✨ 自动优选」会自动选中当前正在运行且 HTTP 代理端口畅通的客户端，也可在顶部胶囊栏随时手动切换 **Clash Verge / Clash Mi / v2rayN**。
2. **无损配置与一键还原（带原始备份）**
   - **Cursor**：自动开启 `"cursor.general.disableHttp2": true`（强制回退 HTTP/1.1 防止绕过代理裸连），同步写入 `http.proxy` 与内置终端环境变量。
   - **Claude Code**：自动写入 `~/.claude/settings.json` 的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`。
   - **Google Antigravity**：同步 IDE 代理及内置终端环境变量。
   - 修改前自动在 `~/.proxy-box/` 留存快照与备份，点击「还原默认」仅回滚代理相关键值，绝不覆盖你期间修改的其他编辑器设置。
3. **免 TUN 增强代理启动器与桌面快捷方式**
   - 内置「免 TUN 代理启动」及「桌面图标生成」（`ProxyBox.exe --launch antigravity` / `--launch cursor` / `--launch codex`），每次双击自动检测当前正在运行的代理客户端端口并注入环境变量与 `--proxy-server` 参数启动，确保后台 `language_server_windows_x64.exe` 同步继承代理。
4. **实测连通性与出口地区合规诊断 + 系统托盘常驻**
   - 并发实测本地端口握手、Cloudflare Trace 出口国家/地区合规性（若误选香港/大陆节点会主动预警），以及 Cursor / Claude / Antigravity 三大服务端点延迟。

## 编译命令

纯 Go 静态编译（`CGO_ENABLED=0`，单文件 `.exe`，免安装环境，可直接跨 Windows 10/11 电脑分发运行）：

```powershell
$env:CGO_ENABLED="0"
go build -ldflags="-s -w -H=windowsgui" -o ProxyBox.exe .
```
