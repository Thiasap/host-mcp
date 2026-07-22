# host-mcp 完整使用与配置指南

本文档包含 `host-mcp` 的完整操作说明。项目概览和最短上手流程请先阅读 [README](../README.md)。授权规则和威胁模型分别以 [权限策略](POLICY.md) 与 [安全模型](SECURITY.md) 为准。

## 术语与功能

- **Profile**：初始化时选择的运行环境，支持 `termux`、`linux`、`wsl`。
- **授权文件夹（Root）**：允许内置 `fs_*` 文件工具访问的目录；不是 Android root 权限。
- **Grant**：某个 Root 内相对路径的 `read`、`write` 或 `delete` 权限。
- **受控命令**：MCP 工具 `exec_run`，只运行本地批准的固定 executable、参数和起始目录。
- **可信 Shell**：MCP 工具 `shell_run`，显式启用后可以执行任意 Shell 文本，不受 Root 文件边界约束。
- **命令状态**：MCP 工具 `command_status`，只读显示两种命令模式和本地设置提示。

## 构建与测试

需要 Go 1.26.5 或兼容的更新版本：

```sh
git clone https://github.com/thiasap/host-mcp.git
cd host-mcp
go build -trimpath -o host-mcp ./cmd/host-mcp
```

运行检查：

```sh
go vet ./...
go test ./...
go test -race ./...
```

如果构建环境需要代理，只应为当前构建命令临时设置 `HTTP_PROXY` 和 `HTTPS_PROXY`。程序不会保存或使用构建代理。

## 初始化与 Profile

首次安装后运行：

```sh
host-mcp init
```

程序会检测环境并显示 Profile、MCP 地址、配置位置、文件策略和命令状态。确认提示为：

```text
Create these configuration files and generate a new Bearer Token? [y/N]
```

默认答案是 `N`。只有输入 `y` 才会创建配置和 Token；直接回车或输入其他内容会取消。

非交互初始化必须显式指定 Profile 并确认：

```sh
host-mcp init --profile termux --yes
host-mcp init --profile linux --yes
host-mcp init --profile wsl --yes
```

只预览、不写文件：

```sh
host-mcp init --profile termux --dry-run
```

初始化成功后会直接显示 Bearer Token。之后可查看或轮换：

```sh
host-mcp token show
host-mcp token rotate
```

运行文件位置：

```text
配置：~/.config/host-mcp/config.json
Token：~/.config/host-mcp/token
审计：~/.local/state/host-mcp/audit.jsonl
```

配置目录权限为 `0700`，配置和 Token 文件权限为 `0600`。

### Termux Profile

Termux Profile 验证：

```text
PREFIX=/data/data/com.termux/files/usr
HOME=/data/data/com.termux/files/home
```

并自动配置：

- `termux-home`：Termux HOME，默认可读；可为具体子目录增加写入和删除权限。
- `termux-prefix`：Termux 安装目录，对内置文件工具永久只读。
- `termux-storage-*`：经过验证的 `~/storage` 目标，对内置文件工具永久只读。

常见共享存储 ID：

```text
termux-storage-shared
termux-storage-downloads
termux-storage-dcim
termux-storage-movies
termux-storage-music
termux-storage-pictures
```

`/sdcard` 只有在确认与 `~/storage/shared` 指向同一目录时才被视为共享存储别名。这些只读约束不能通过 CLI 或手工修改配置解除，但它们只约束内置文件工具，不约束可信 Shell。

### Linux Profile

Linux 默认不配置 Root、Grant 或命令规则，也不会自动授权 HOME 或 `/`。以下路径被硬性禁止作为普通 Root：

```text
/
/proc
/sys
/dev
/run
/boot
```

### WSL Profile

WSL 继承 Linux 默认拒绝策略，并额外禁止普通授权整个 `/mnt`。如确实需要 Windows 文件，应在审查风险后调整安全策略，不要默认开放整个挂载目录。

## 启动与服务管理

`host-mcp serve` 和后台服务最终运行的是同一个服务器，不能同时占用相同监听地址和端口。

### Termux 后台服务

```sh
host-mcp service enable
host-mcp service status
```

Termux 使用 `termux-services` / runit。`enable` 会移除 `down` 文件并立即启动服务。只运行 `service start` 可能得到：

```text
enabled: false
running: true
```

这表示当前运行，但没有持久启用。

### Linux/WSL 后台服务

```sh
host-mcp service install
host-mcp service enable
host-mcp service start
host-mcp service status
```

Linux/WSL 使用 `systemd --user`，不会回退到 system-wide systemd、cron 或 `nohup`。

### 前台调试

```sh
host-mcp serve
```

命令会占用当前终端，按 `Ctrl+C` 停止。后台服务已运行时，前台启动会提示端口被占用。

从后台切换到前台：

```sh
host-mcp service stop
host-mcp serve
```

常用服务命令：

```sh
host-mcp service install
host-mcp service enable
host-mcp service start
host-mcp service status
host-mcp service restart
host-mcp service stop
host-mcp service disable
```

## 网络监听与 MCP 客户端配置

默认连接参数：

```text
URL:           http://127.0.0.1:8765/mcp
Authorization: Bearer <host-mcp token show 的输出>
Transport:     Streamable HTTP
```

常见客户端格式：

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

部分客户端使用嵌套 transport：

```json
{
  "mcpServers": {
    "host-mcp": {
      "transport": {
        "type": "streamable-http",
        "url": "http://127.0.0.1:8765/mcp",
        "headers": {
          "Authorization": "Bearer <TOKEN>"
        }
      }
    }
  }
}
```

将 `<TOKEN>` 替换为完整 Token，不要提交到 Git、公开日志或发送给不可信客户端。

### 可信局域网监听

优先绑定主机的明确局域网 IPv4 地址：

```sh
host-mcp config set-listen 192.168.43.1:8765 --yes
host-mcp service restart
host-mcp status
```

确实需要监听所有 IPv4 接口：

```sh
host-mcp config set-listen 0.0.0.0:8765 --yes
host-mcp service restart
```

恢复本机访问：

```sh
host-mcp config set-listen 127.0.0.1:8765 --yes
host-mcp service restart
```

另一台设备应使用主机局域网地址，例如 `http://192.168.43.1:8765/mcp`，而不是它自己的 `127.0.0.1`。

浏览器环境还需在配置的 `origins` 中加入客户端精确 Origin。普通非浏览器 MCP 客户端通常不发送 Origin。

> LAN 模式仍是明文 HTTP。Bearer Token 只认证客户端，不加密 Token 或内容。不要暴露到公网或不可信 Wi-Fi；跨不可信网络应使用 VPN、SSH 隧道或经过审查的 TLS 代理。Android 客户端还可能因 Network Security Policy 禁止 cleartext HTTP，此时需要客户端允许明文、本机回环连接或 HTTPS/TLS 入口。

## 命令执行

三个名称的区别：

| 名称 | 用途 |
|---|---|
| `command_status` | MCP 只读状态工具，显示受控命令、可信 Shell、风险和本地设置命令。 |
| `exec_run` | MCP 受控命令工具，只运行设备所有者预先批准的规则，不经过 Shell。 |
| `shell_run` | MCP 可信 Shell 工具，显式启用后运行任意 Shell 文本。 |
| `RunExec` | Go 源代码内部函数，负责实现 `exec_run`；不是 MCP 工具，也无需用户配置。 |

默认情况下，`exec_run` 和 `shell_run` 都不会执行命令，但工具仍可见并返回设置提示。Claw 不能通过 MCP 自行开启权限。

### 受控命令

查看状态和预设：

```sh
host-mcp commands status
host-mcp commands presets
host-mcp commands setup
```

例如允许固定的 Git 状态查询：

```sh
host-mcp commands enable git-status --folder termux-home:. --yes
host-mcp service restart
```

MCP 调用示例：

```json
{
  "rule": "git-status",
  "args": ["status", "--short"],
  "cwd": {
    "root": "termux-home",
    "path": "."
  }
}
```

规则固定 executable、参数格式和允许的起始目录，不支持管道、重定向、命令替换或任意 Shell 文本。预设只在对应 canonical executable 已安装时出现。

高级手工规则：

```sh
host-mcp exec allow \
  --name git-status \
  --executable /usr/bin/git \
  --arg-pattern 'status' \
  --arg-pattern '--short' \
  --cwd workspace:.
```

常见 Shell 和解释器不能加入受控规则，包括 `sh`、`bash`、`python`、`node`、`perl`、`ruby` 和 `php`。

### 可信 Shell

查看并开启：

```sh
host-mcp shell status
host-mcp shell enable
host-mcp service restart
```

启用时必须阅读警告并输入指定的完整确认文本。非回环监听会显示额外网络风险警告。

刚初始化时以下命令都不会执行：

```text
pkg install xxx
python xxx.py
mkdir ~/workdir
```

开启可信 Shell 后，在相应程序已安装且路径可访问的前提下可以执行：

- `pkg install xxx`：会真实安装软件并修改 Termux prefix。
- `python xxx.py`：需要已经安装 Python，脚本拥有当前账户权限。
- `mkdir ~/workdir`：会真实创建目录。

`shell_run` 是一次性非交互执行，不提供持续 TTY 会话。`pkg install` 等可能等待输入的命令应使用合适的非交互参数，或在 Termux 本地手动运行。

可信 Shell：

- 拥有运行 `host-mcp` 的 Termux/Linux/WSL 用户权限；
- 可以联网、启动子进程、使用绝对路径和修改 prefix；
- 不受授权文件夹或内置文件工具只读规则约束；
- 不会为每条命令弹出本地确认；
- 不是容器、虚拟机或内核级沙箱。

关闭：

```sh
host-mcp shell disable
host-mcp service restart
```

## 授权文件夹与文件权限

Root 是配置中的技术名称，普通用户可以理解为“授权文件夹”。客户端只能提交 Root ID 和内部相对路径，不能向内置文件工具提交任意主机绝对路径。

查看 Root：

```sh
host-mcp roots list
```

添加只读 Root：

```sh
host-mcp roots add \
  --id projects \
  --path "$HOME/projects" \
  --description "项目目录" \
  --read-only \
  --yes
```

添加允许后续授权写入的 Root：

```sh
host-mcp roots add \
  --id workspace \
  --path "$HOME/workspace" \
  --description "工作目录" \
  --write-eligible \
  --yes
```

管理 Grant：

```sh
host-mcp permissions grant --root workspace --operation read --path .
host-mcp permissions grant --root workspace --operation write --path output
host-mcp permissions grant --root workspace --operation delete --path output

host-mcp permissions revoke --root workspace --operation delete --path output
host-mcp permissions revoke --root workspace --operation write --path output
```

删除权限必须由覆盖同一路径的写权限支持；撤销时应先撤销 delete。解释一次判断：

```sh
host-mcp policy explain workspace write output/result.txt
```

移除 Root：

```sh
host-mcp roots remove --yes workspace
```

仍被命令规则引用的 Root 无法移除。

## MCP 文件工具

始终提供：

```text
fs_roots
fs_stat
fs_list
fs_read
fs_search
```

存在相应权限时提供：

```text
fs_write
fs_mkdir
fs_rename
fs_delete
```

文件工具使用 `{root, path}`：

```json
{
  "root": "workspace",
  "path": "src/main.go"
}
```

重命名必须在同一 Root 内，并显式给出来源和目标：

```json
{
  "source_root": "workspace",
  "source_path": "old.txt",
  "destination_root": "workspace",
  "destination_path": "new.txt"
}
```

覆盖或删除已有普通文件需要其当前 SHA-256，不支持递归删除。

## 诊断

```sh
host-mcp profile detect
host-mcp config check
host-mcp config show
host-mcp config show-effective
host-mcp status
host-mcp doctor
host-mcp commands status
host-mcp shell status
host-mcp service status
```

`host-mcp serve` 报地址占用时，通常已有后台服务或另一个前台实例。检查：

```sh
host-mcp service status
host-mcp status
```

切换到前台前先运行：

```sh
host-mcp service stop
```

## 发布产物

Termux Android arm64：

```text
host-mcp_2.0.0_aarch64.deb
```

Linux：

```text
host-mcp_2.0.0_linux_amd64.tar.gz
host-mcp_2.0.0_linux_arm64.tar.gz
```

校验：

```sh
sha256sum -c SHA256SUMS
sha256sum -c host-mcp_2.0.0_aarch64.deb.sha256
```

## 延伸阅读

- [README](../README.md)
- [权限策略](POLICY.md)
- [安全模型](SECURITY.md)
