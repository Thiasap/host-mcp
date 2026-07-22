# host-mcp

`host-mcp` 是一个独立的 MCP（Model Context Protocol）服务端，通过 Streamable HTTP 向 Claw、MiClaw 等 AI Agent 提供经过授权的文件访问和命令执行能力，不依赖 Python 或 Node.js 运行环境。

支持：

- Termux / Android
- 标准 Linux
- WSL

暂不支持 macOS 和原生 Windows。

> 起初就是为了让miclaw使用Termux，顺便适配一下其他环境。本项目的全部代码均由 ChatGPT 生成。使用前请自行审查代码、配置和权限策略，并仅在可信环境中运行。

## 主要功能

- 读取、搜索和管理授权文件夹中的文件。
- 通过 `exec_run` 运行设备所有者预先批准的受控命令。
- 通过显式启用的 `shell_run` 运行任意 Shell 命令。
- 通过 `command_status` 向 MCP 客户端解释命令状态和启用方式。
- 使用 Bearer Token 保护 Streamable HTTP MCP 接口。
- 支持 Termux runit 和 Linux/WSL systemd user 后台服务。

“授权文件夹”在配置中称为 **Root**。它不是 Android root 或超级用户权限，只表示允许内置文件工具访问的目录。

## 安全默认值

- 默认仅监听 `127.0.0.1:8765`，MCP 地址为 `http://127.0.0.1:8765/mcp`。
- 每个请求都需要初始化时生成的 Bearer Token。
- Linux/WSL 默认不授权任何文件夹；Termux HOME 默认可读，prefix 和共享存储对内置文件工具强制只读。
- `exec_run` 和 `shell_run` 默认都不会执行命令，但工具仍然可见并返回设置提示。
- 可信 Shell 不是沙箱，可以绕过授权文件夹和内置文件工具的只读策略。
- 不要将明文 HTTP、非回环监听和可信 Shell 暴露到公网或不可信网络。

完整边界请阅读：[安全模型](docs/SECURITY.md) 和 [权限策略](docs/POLICY.md)。

## 快速开始

### 1. 初始化

```sh
host-mcp init
```

确认后程序会显示新生成的 Token、MCP 地址和当前 Profile 的启动命令。Token 相当于密码，请复制到 MCP 客户端，不要提交到 Git 或公开日志。

之后查看或轮换 Token：

```sh
host-mcp token show
host-mcp token rotate
```

### 2. 启动服务

只选择一种启动方式，不要同时运行前台和后台实例。

Termux 后台运行：

```sh
host-mcp service enable
host-mcp service status
```

Linux/WSL 后台运行：

```sh
host-mcp service install
host-mcp service enable
host-mcp service start
host-mcp service status
```

前台调试：

```sh
host-mcp serve
```

检查状态：

```sh
host-mcp status
```

### 3. 配置 MCP 客户端

```json
{
  "mcpServers": {
    "host-mcp": {
      "type": "streamable-http",
      "url": "http://127.0.0.1:8765/mcp",
      "headers": {
        "Authorization": "Bearer <TOKEN>"
      }
    }
  }
}
```

将 `<TOKEN>` 替换为 `host-mcp token show` 的完整输出。

若客户端位于另一台设备，需要配置可信局域网监听并将 URL 改为主机的局域网地址。参见：[网络监听与 MCP 客户端配置](docs/USAGE.md#网络监听与-mcp-客户端配置)。

## 让 Claw 执行命令

### 受控命令（推荐）

`exec_run` 只能执行本地预先批准的固定规则，不经过 Shell：

```sh
host-mcp commands status
host-mcp commands presets
host-mcp commands setup
```

例如启用固定的 Git 状态查询：

```sh
host-mcp commands enable git-status --folder termux-home:. --yes
host-mcp service restart
```

### 可信 Shell（高风险）

如果需要执行 `pkg install`、`python xxx.py`、`mkdir ~/workdir` 等任意终端命令：

```sh
host-mcp shell enable
host-mcp service restart
```

启用时必须阅读风险说明并输入完整确认文本。可信 Shell 拥有 `host-mcp` 运行账户的系统权限，可以联网、启动子进程和修改 Termux prefix；它不受 Root 文件边界约束，也不会为每条命令弹出本地确认。

关闭：

```sh
host-mcp shell disable
host-mcp service restart
```

`exec_run`、`shell_run`、`command_status` 的区别和完整示例见：[命令执行指南](docs/USAGE.md#命令执行)。

## 构建

需要 Go 1.26.5 或兼容的更新版本：

```sh
git clone https://github.com/thiasap/host-mcp.git
cd host-mcp
go build -trimpath -o host-mcp ./cmd/host-mcp
```

完整构建、测试、发布产物和校验方式见：[完整使用指南](docs/USAGE.md)。

## 文档

- [完整使用与配置指南](docs/USAGE.md)：初始化、Profile、服务、客户端、局域网、命令、文件权限、诊断和发布产物。
- [权限策略](docs/POLICY.md)：Root、Grant、Termux 强制只读和命令能力规则。
- [安全模型](docs/SECURITY.md)：认证、网络、文件边界、可信 Shell 和威胁模型。
