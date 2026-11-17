<!--
 * NXContainer Runtime Protocol
 *
 * 定义控制管道、请求格式、输出事件与结构化错误。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 -->

# 执行器协议 v1

这是独立依赖库的可信宿主控制协议，不是 HTTP 服务，也不是给模型直接调用的 API。公开 Node 类型见 `src/index.d.ts`；Node 应用通常只需要通过 `nxcontainer` 包入口调用 SDK。

## 传输与生命周期

SDK 启动 `nxcontainer --stdio`，通过 stdin 写入一行 UTF-8 JSON，保留 stdin 打开直到任务结束。

- 单请求最大 2 MiB，只有一个请求，不复用执行器。
- stdin EOF 或第二个请求意味着取消；退出时不会遗留一个脱离主程序的长期任务。
- stdout 只发送一行一个的 JSON 事件；辅助诊断走 stderr。
- JSON 未知字段被拒绝，版本必须严格等于 1。
- 每个请求恰好一个终止事件：`result` 或 `error`，终止后不可再发送输出。
- 后端内部启动失败或操作被拒绝用 `error`；被隔离程序正常返回非零退出码用 `result.exitCode`。
- 执行器退出码非零、不完整响应或非法事件顺序均视为协议/运行时错误。

Linux 能力自检最多 5 秒，Windows 进程自检最多 10 秒。SDK 对管理操作有 15 秒看门狗，对执行操作使用策略超时加 15 秒清理预算；取消先关闭 stdin，仍无响应时再等待最多 10 秒强制终止执行器。原生清理失败不能视为正常完成。

## 操作

### capabilities

```json
{"version":1,"operation":"capabilities"}
```

结果包含平台、后端、实验状态、项目验证进度 `validation`、`exec`、`files`、`readOnlyMounts`、`privateFilesystem`、`networkNone`、`desktop`、`resourceQuotas`、`processQuotas`、`desktopIsolation` 和不可用原因。

`validation` 描述项目验证进度，不是本次自检结果：

- `partial`：Windows amd64 已做部分实机验证，未完成完整安全验收。
- `pending`：Linux amd64/arm64、Windows arm64 待验证；自检成功也不自动升级该状态。
- `unsupported`：不支持的系统或架构。

完整 GUI 不在当前库范围中，`desktop` 保持 false；请求开启 GUI 会明确拒绝。

Windows 的 `processQuotas` 表示 CPU、提交内存和进程数限制；`resourceQuotas` 仍为 false，因为没有完整资源/磁盘配额。`desktopIsolation` 不表示 GUI 控制接口可用。

也可直接运行 `nxcontainer --capabilities`，以普通 JSON 输出结果，不使用 stdin 生命周期协议。

### prepare

```json
{
  "version": 1,
  "operation": "prepare",
  "sandbox": {
    "agentId": "agent-a",
    "mounts": [{"name":"workspace","source":"/srv/agents/a/workspace","access":"read-write"}],
    "network": "none",
    "desktop": false,
    "experimental": true,
    "limits": {"timeoutMs":60000,"maxOutputBytes":1048576}
  }
}
```

校验源目录与后端，返回 `agentId`、逻辑名称到沙箱路径的 `paths` 和 `ephemeral: true`。不创建宿主工作区、系统用户或常驻容器。能力自检会创建并清理临时运行资源，Windows 还包括临时 AppContainer 注册和私有桌面。

Windows 策略可额外提供 `limits.memoryBytes`、`limits.maxProcesses`、`limits.cpuPercent`，范围及默认值见 README；Linux 收到非零进程配额时返回 `UNSUPPORTED_CAPABILITY`。Windows `paths` 是真实授权路径，不是虚拟 C 盘路径。

每次后续请求再次提交完整策略，原生执行器重新校验。SDK 在父进程内持有不可变策略与目录归属登记；原生协议使用方必须自己负责多 Agent 的所有权协调。

### exec

```json
{
  "version": 1,
  "operation": "exec",
  "sandbox": {},
  "command": {
    "argv": ["/bin/sh","-c","pwd"],
    "cwd": "workspace",
    "env": {},
    "stdin": "",
    "timeoutMs": 60000
  }
}
```

示意中的 `sandbox` 必须替换为完整策略，不支持空策略。

`argv` 最多 256 项、合计 128 KiB；stdin 最多 256 KiB。环境变量最多 64 个，单值最多 32 KiB；总请求大小仍有限制。命令的超时不能高于沙箱策略。

不支持宿主路径形式的 cwd，始终使用挂载名及其相对路径。Linux 默认环境定义了 `WORKSPACE`、`HOME`、`TMPDIR`、`XDG_RUNTIME_DIR`、`PATH`、`LANG`、`NXCONTAINER_AGENT_ID`。Windows 另提供受控的 `SYSTEMROOT`、`SYSTEMDRIVE`、`COMSPEC`、`USERPROFILE`、`APPDATA`、`LOCALAPPDATA`、`TEMP`、`TMP`，其中用户目录指向临时私有目录。除 PATH/LANG 等可调整变量外，受控身份和目录变量不能被覆盖；Windows 按大小写不敏感规则检查，拒绝重复变量。

### file

```json
{
  "version": 1,
  "operation": "file",
  "sandbox": {},
  "file": {"operation":"read","mount":"workspace","path":"文档/a.txt"}
}
```

支持 `read`、`write`、`list`、`mkdir`、`remove`；写入额外提供 Base64 `data`。路径必须相对指定挂载根，禁止 `..`、绝对路径、反斜杠、冒号和空字符。

可信执行器先验证挂载和权限，再进入隔离环境运行自身的文件 worker。结果由 worker 输出 JSON，经相同的字节输出事件传回，SDK 解码；文件读写不会在宿主父进程完成。

## 输出事件

```json
{"version":1,"type":"stdout","data":"5L2g5aW9"}
{"version":1,"type":"stderr","data":""}
{"version":1,"type":"result","result":{"exitCode":0,"durationMs":12}}
```

输出事件的 `data` 是原始字节的 Base64。单块最多 16 KiB，不要求 UTF-8 字符恰好位于块边界。SDK 的 `onOutput` 返回 Buffer，最终的 stdout/stderr 在拼接后解码。

错误：

```json
{"version":1,"type":"error","error":{"code":"BACKEND_UNAVAILABLE","message":"后端不可用"}}
```

常见错误码：

- `INVALID_CONFIG` / `INVALID_REQUEST` / `INVALID_COMMAND` / `INVALID_PATH`
- `PERMISSION_DENIED` / `UNSAFE_PATH` / `MOUNT_CONFLICT`
- `AGENT_EXISTS` / `AGENT_BUSY` / `CLOSED`
- `UNSUPPORTED_CAPABILITY` / `BACKEND_UNAVAILABLE` / `EXPERIMENTAL_REQUIRED`
- `RUNTIME_UNAVAILABLE` / `BACKEND_FAILED` / `RUNTIME_FAILED`
- `ABORTED` / `TIMEOUT` / `RUNTIME_TIMEOUT` / `OUTPUT_LIMIT`
- `FILE_TOO_LARGE` / `FILE_OPERATION_FAILED` / `FILE_LIMIT`
- `CLEANUP_FAILED` / `RECOVERY_REQUIRED`
- `REQUEST_TOO_LARGE` / `PROTOCOL_ERROR`

请求错误、取消和超限会抛异常；此前已通过 `onOutput` 推送的内容仍属于这次失败操作，不能当成成功结果。
