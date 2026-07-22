# 权限策略

配置版本 2 使用持久化 Profile、命名文件系统 Root 和 Root 内相对路径授权。

每个 Root 都作为独立能力通过 `os.OpenRoot` 打开。MCP 客户端只能提交 Root ID 和 Root 内相对路径，不能提交任意主机绝对路径。

## Profile 强制规则

### Termux

- `termux-home` 默认只有读取授权，但允许管理员为具体子目录增加写入和删除权限。
- `termux-prefix` 永久只读。
- 经过验证的 `termux-storage-*` 永久只读。
- 共享存储不能通过 CLI 或手工修改配置提升为可写。

### Linux

默认不配置任何 Root。以下路径被硬性禁止：

```text
/
/proc
/sys
/dev
/run
/boot
```

### WSL

继承 Linux 限制，并额外禁止 `/mnt`。

Profile 规则会在配置校验和打开 Root 时重复执行，配置文件不能削弱这些限制。

## 文件权限

权限由三个元素组成：

```text
Root ID + 操作 + Root 内相对路径
```

支持的操作：

```text
read
write
delete
```

示例：

```sh
host-mcp permissions grant --root workspace --operation read --path .
host-mcp permissions grant --root workspace --operation write --path output
host-mcp permissions grant --root workspace --operation delete --path output
```

删除授权必须存在覆盖同一路径的写入授权。撤销写入权限前，必须先撤销依赖它的删除权限。

## 文件变更保护

- 只在存在对应有效授权时发布变更类 MCP 工具。
- 重命名只能发生在同一个命名 Root 内。
- 不支持递归删除。
- 拒绝删除符号链接。
- 覆盖已有普通文件必须提供其当前 SHA-256。
- 删除已有普通文件必须提供其当前 SHA-256。
- 所有读写、目录列表、搜索和命令输出都受大小或数量限制。

## 命令执行

### 受控命令 `exec_run`

`exec_run` 是提供给 MCP 客户端的受控命令工具，程序内部由 Go 函数 `RunExec` 实现。`RunExec` 不是用户命令，也不是另一个 MCP 工具。

受控命令默认关闭。启用规则时：

- executable 必须是固定、绝对、canonical、非符号链接的可执行普通文件。
- 不通过 Shell 解释参数。
- 常见 Shell 和解释器不能作为 executable。
- 每条规则必须声明允许的 Root-aware 工作目录。
- 参数数量、参数长度、正则规则、执行时长和输出大小均受限制。
- 命令只获得最小环境变量集合。

命令规则属于应用层能力控制，不等同于容器、SELinux、seccomp 或虚拟机提供的内核级隔离。

### 可信 Shell `shell_run`

`shell_run` 是单独的高风险 MCP 工具，默认关闭。禁用时工具仍可见，但只返回风险说明和由设备所有者执行的启用命令。启用后，它通过固定、canonical 的 Shell 执行任意命令文本。

允许的起始目录必须引用一个命名 Root，但这只限制进程从哪里启动，不限制 Shell 后续使用 `cd`、绝对路径、网络或子进程。因此：

- `shell_run` 不受 Root 文件边界约束；
- `termux-prefix` 和 `termux-storage-*` 对内置 `fs_*` 工具的只读保证不适用于 Shell；
- Shell 拥有运行 `host-mcp` 的操作系统账户权限；
- Shell 不是容器、SELinux、seccomp、虚拟机或其他内核级沙箱；
- 只能由设备所有者在本地 CLI 完成专用风险确认后启用。

`command_status` 始终作为只读 MCP 工具发布，用于区分受控命令和可信 Shell 的状态；MCP 客户端不能通过它自行提升权限。
