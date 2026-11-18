<!--
 * NXContainer Project Overview
 *
 * 项目说明、构建方法、使用示例与当前能力范围。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 -->

# NXContainer

面向 LLM 智能体的轻量原生沙箱依赖库。任何 Node.js 应用都可以通过 `import { NXContainer } from 'nxcontainer'` 使用，无需 LMCenter、Cordis、HTTP 服务或 GUI。ESM SDK 通过私有管道调用包内的 Go 执行器，不使用虚拟机，没有第三方 npm 依赖；Windows 原生 API 使用 `golang.org/x/sys/windows`。

> **当前版本：0.1.0-alpha.2，技术验证阶段，不是已经完成安全验收的跨平台沙箱。**
> Windows 需要开启 `allowExperimentalWindows`。Linux 已在 Alpine 3.24 amd64/arm64 虚拟机中完成限定环境的实际运行验收（见验证记录），其他发行版与全面安全验收仍未完成，使用需显式开启 `allowExperimentalLinux`。缺少能力时明确拒绝，不降级为宿主命令。不要用于真实敏感数据或对抗性生产负载。

## 依赖库范围

- 提供沙箱生命周期、授权目录、命令执行、文件访问、输出流、取消、能力查询及可用的资源限制。
- 不开发完整 GUI、桌面控制台或 LMC 服务/工具/界面；这些不作为本库当前交付的缺项。宿主应用自行负责用户鉴权、模型工具协议、目录分配和业务界面。
- 保留 Windows 私有 Window Station/Desktop 作为安全边界，不提供截图、输入或持久桌面 API；`desktop: true` 仍明确拒绝。
- 不要求为每个 Agent 创建系统用户：Windows 使用独立 LPAC 身份，Linux 使用 namespaces 等原生隔离机制。

| 目标 | 项目验证状态 |
|---|---|
| Windows x64 | 部分实机验证，仍为实验版本 |
| Windows arm64 | 待验证，仅交叉编译 |
| Linux x64 / arm64 | Alpine 双架构虚拟机的运行验收已通过；其他环境及全面安全验收待完成 |

`capabilities().validation` 分别使用 `partial`、`pending`、`unsupported` 描述项目验证进度；`exec` 等字段另行描述当前机器的能力自检结果。自检通过不会自动将 Linux 从“待验证”改为“已验证”。

## 当前能力

| 能力 | Windows | Linux amd64 / arm64（实验性） |
|---|---|---|
| Node.js ESM SDK、类型声明、配置校验 | 已实现 | 已实现 |
| 能力检测、缺失能力明确拒绝 | 已实现 | 已实现 |
| 原生命令隔离 | 实验性 LPAC，启动前检查令牌 | 实验性 bubblewrap 后端 |
| 授权目录读写 / 只读共享 | 临时 SID/DACL 授权，结束后撤销 | 实验性 bind mount / 只读 bind mount |
| 文件接口 | 在 LPAC 内通过 Go `os.Root` 执行 | 在沙箱内通过 Go `os.Root` 执行 |
| 独立 HOME、临时目录和环境 | 每次执行独立、结束后清除 | 每次执行独立、结束后清除 |
| 默认断网 | 不授予网络能力，已测试宿主回环连接被拒绝 | network namespace + seccomp，禁止 socket 创建 |
| 超时、取消、输出预算 | Job Object 终止进程树 | 杀死隔离进程树 |
| CPU、内存、进程数限制 | Job Object 硬限制 | 未实现，请求时明确拒绝 |
| 工作区磁盘硬配额 | 未实现 | 未实现 |
| 桌面安全边界 | 私有 Window Station/Desktop，禁止访问宿主及其他实例 | 未实现 |
| 完整 GUI / 桌面控制 API | 不在当前依赖库范围 | 不在当前依赖库范围 |

完整限制见 [SECURITY.md](SECURITY.md)，协议见 [docs/protocol.md](docs/protocol.md)，后续实现见 [docs/roadmap.md](docs/roadmap.md)，实际验证记录见 [docs/verification.md](docs/verification.md)。

## 构建与测试（源码开发）

开发需要 Node.js 22+、Go 1.25+。首次构建会下载 `go.mod` 固定版本的 `golang.org/x/sys`；不依赖 Rust 或 C/C++ 编译器。

```sh
cd nxcontainer
npm run build
npm test
npm run test:native
npm run check:native
```

交叉编译（只证明编译成功，不代表目标平台安全验收通过）：

```sh
npm run build -- win32 x64
npm run build -- linux x64
npm run build -- linux arm64
```

构建产物为 `bin/nxcontainer-<平台>-<架构>[.exe]`，SDK 默认选择当前平台产物，与应用工作目录无关。也可以由可信主程序通过 `runtimePath` 指定绝对路径。没有二进制时返回 `RUNTIME_UNAVAILABLE`，不自动下载，不使用 npm 安装生命周期脚本。

制作依赖分发包并验证实际安装（在源码目录执行）：

```sh
npm run build:all
npm run test:package
npm pack
```

`test:package` 会把真实 tar 包离线安装到仓库外的临时应用，禁用安装脚本，再仅通过公开包名验证导入、能力报告和本机可用的命令/文件接口。不是从仓库源码相对导入的替代测试。

Windows 制作的 npm 包可能丢失 Linux 二进制执行位。Linux 首次使用自带执行器时，SDK 通过文件句柄补齐所有者执行位，不跟随叶子符号链接，不修改自定义 `runtimePath`。只读安装目录应预先准备执行权限，或使用已构建且可执行的自定义路径；失败时返回 `RUNTIME_UNAVAILABLE`。

查看本机能力：

```sh
# Linux
./bin/nxcontainer-linux-x64 --capabilities

# Windows
./bin/nxcontainer-win32-x64.exe --capabilities
```

### Windows 运行前提与限制

- 当前实测 Windows 11 amd64；Windows arm64 只做交叉编译。其他系统版本和非管理员承载账户仍需验收。
- 宿主必须对授权树拥有 `READ_CONTROL` 和 `WRITE_DAC`。执行期间会修改授权树的 DACL，不修改系统用户、组或全局设备权限。
- 只接受本地盘符目录，拒绝 UNC、设备路径、目录联接、符号链接、硬链接和特殊文件。每棵树最多 65536 项。
- 现有对象通过稳定句柄恢复原 DACL，新建对象中移除本次授权 SID。正常取消先关闭控制管道，不直接用 Windows `SIGTERM` 跳过清理。
- 强制杀死原生执行器、断电、恶意文件 ACL 或清理失败仍可能需要宿主人工恢复；尚无持久恢复日志。
- 默认 Job 限制：512 MiB 提交内存、32 个进程、50% 总 CPU。可通过 `memoryBytes`（64 MiB～8 GiB）、`maxProcesses`（1～256）、`cpuPercent`（1～100）调整。没有磁盘配额或完整内核资源预算。
- `desktopIsolation: true` 只表示私有桌面的安全边界，不表示截图、输入或持久 GUI API 可用。
- Windows 默认返回真实授权路径；可信宿主可为挂载配置 `alias` 显示联接（见下文），例如 `C:\Agents\<UUID>\workspace`。必须使用 `sandbox.paths`。
- portable Node 的普通运行与继承标准流的子进程已测试。其默认管道使用全局命名管道，当前会被拒绝并可能在 libuv 中重试到超时；`stdio: 'ignore'` 访问 NUL 也会被拒绝。使用 `stdio: 'inherit'` 或授权文件，不会为兼容软件而放开全局 IPC/设备。

Windows 构造选项使用 `allowExperimentalWindows: true`，目录使用 Windows 绝对路径；例如执行 `sandbox.exec(['cmd.exe', '/d', '/c', 'echo', 'NXContainer'])`。临时工作区示例同时支持 Windows 和 Linux。

### Linux 运行前提（实验性，已验证环境见记录）

- 原生 Linux 内核，允许非特权 user namespace、PID、network、IPC、UTS、cgroup namespace 和 seccomp。
- 安装发行版提供的非 setuid `bubblewrap`，位置为 `/usr/bin/bwrap` 或 `/bin/bwrap`。
- 必须支持 `--disable-userns`、`--assert-userns-disabled`、`--seccomp`、`--size`；版本能力不足直接拒绝。
- 普通账户运行，不使用 root。系统安全策略禁止 namespace 时返回不可用，不自动修改系统策略。
- 当前系统布局需要 `/usr/bin/true` 及 `/usr`；兼容常见的 merged-/usr 和独立 `/bin`、`/lib` 布局。不支持任意发行版布局。
- 集成测试的 socket 检查需要 `/usr/bin/python3`。

`capabilities()` 会实际启动一个最小隔离进程进行自检，而不只是检测 `bwrap` 是否存在。自检不是全面安全审计。

在满足隔离前提的 Linux 测试机或独立虚拟机客体中运行：

```sh
NXCONTAINER_REQUIRE_RUNTIME=1 NXCONTAINER_REQUIRE_LINUX=1 npm test
```

这会将执行器缺失、隔离能力缺失或测试机不是 Linux 视为失败。普通 `npm test` 在 Windows 上会明确跳过 Linux 集成测试；跳过不算该平台验收。

## 作为依赖导入

独立应用可使用本地包依赖：

```json
{
  "dependencies": {
    "nxcontainer": "file:../nxcontainer"
  }
}
```

```js
import { NXContainer } from 'nxcontainer';
```

也可以在消费应用中安装本地打包产物，不访问 npm 注册表：

```sh
npm install /absolute/path/nxcontainer-0.1.0-alpha.2.tgz --offline --ignore-scripts
```

带有本机二进制的分发包在消费端只需要 Node.js，无需 Go 工具链；Linux 仍需系统提供 bubblewrap。TypeScript 应用使用 Node.js 类型及 `NodeNext` 模块解析；类型入口随包分发。公开导出为 `NXContainer`、`NXContainerError` 和 `protocolVersion`，完整接口见 [`src/index.d.ts`](src/index.d.ts)。

宿主应通过包名导入，不依赖 `src/`、`native/` 的内部文件布局。LMC 与其他应用一样只是潜在消费方，不包含在本库的开发或验收范围中。

## 用法

下面的目录应由可信主程序事先创建，并替换为应用自己管理的绝对路径。不要把模型输入直接展开到运行时配置中。

```js
import path from 'node:path';
import { NXContainer } from 'nxcontainer';

const root = process.platform === 'win32' ? 'D:\\AgentWorkspaces' : '/srv/agent-workspaces';
const nx = new NXContainer({
  allowedRoots: [root],
  allowExperimentalWindows: true,
  allowExperimentalLinux: true, // Linux 后端仍为实验性，验证范围见记录。
});

try {
  const capabilities = await nx.capabilities();
  if (!capabilities.exec) throw new Error(capabilities.reason);

  const sandbox = await nx.createSandbox({
    agentId: 'agent-a',
    mounts: [
      {
        name: 'workspace',
        source: path.join(root, 'agent-a', 'workspace'),
        access: 'read-write',
      },
      {
        name: 'reference',
        source: path.join(root, 'agent-b', 'workspace'),
        access: 'read-only',
      },
    ],
    network: 'none',
    desktop: false,
    limits: { timeoutMs: 60_000, maxOutputBytes: 1024 * 1024 },
  });

  await sandbox.writeFile('workspace', '说明.txt', '只写自己的工作区');
  const command = process.platform === 'win32'
    ? ['cmd.exe', '/d', '/c', 'echo', 'NXContainer']
    : ['/bin/sh', '-c', 'pwd; ls'];
  const result = await sandbox.exec(command, {
    cwd: 'workspace',
    onOutput: ({ stream, data }) => {
      // 输出是字节块；逐块解码文字时请使用 StringDecoder。
      (stream === 'stdout' ? process.stdout : process.stderr).write(data);
    },
  });
  console.log(result.exitCode, sandbox.paths.workspace);
  await sandbox.dispose();
} finally {
  await nx.close();
}
```

无真实数据的示例：`node examples/temporary-workspace.mjs`，只操作自动创建的临时目录。

### Agent 与挂载

- 一个 `NXContainer` 实例集中管理一组 Agent；同一 Agent 同时只能执行一个操作，繁忙时返回 `AGENT_BUSY`。
- 同一实例不能向两个 Agent 授予重叠目录的写权限。只读共享必须由主程序显式配置。
- 不自动让 Agent 读取其他 Agent。可以显式共享另一个 Agent 的完整工作区根，但其中所有普通文件也会变为可读，包括私有文档和凭据。
- 推荐给共享资料单独建目录，而不是共享整个工作区。
- 为避免可写祖先下的目录被替换，共享目录必须是独立的挂载根，不能是其他 Agent 可写挂载的子目录或祖先。
- 同一沙箱中的源目录不能重复或重叠，挂载名不区分大小写地判重。
- 本版没有跨进程全局目录归属数据库。多个 SDK 实例或宿主进程之间的所有权协调由可信主程序负责。
- Linux 返回 `/Agents/<agentId>/<挂载名>`，Windows 返回 `alias` 或实际授权目录。调用方必须使用 `sandbox.paths`。
- `workspace` 必须存在，但可以设为 `access: 'read-only'`。SDK 与原生协议都拒绝向只读工作区写入；shell 同样受 Linux 只读挂载／Windows LPAC ACL 约束。改变只读状态须先 dispose，再重新创建，不能在运行中修改策略。
- Windows 可选 `alias`：可信主程序事先创建的目录联接，必须指向对应 source，父目录在 allowedRoots 内、祖先不可为链接，且不能与任何源目录重叠。库不创建或删除该联接，只临时授予联接读取与祖先路径（不含卷根）的属性读取／穿越（不授予祖先列目录、文件内容读取或写入）；结束通过稳定句柄恢复 DACL。涉及共同祖先的操作串行授权。此能力不是完整私有文件系统视图，也不能把不可信工作区内的联接当作授权源。
- Windows 对相同源目录使用跨进程互斥量，在执行和 ACL 恢复期间持有；涉及共享源的任务会串行等待，不同源目录可并行。
- 挂载表在沙箱句柄存续期间不可修改；先 `dispose()`，再用新策略创建。不承诺在不停止进程的情况下撤销已打开的文件句柄。
- `dispose()` / `close()` 不删除宿主目录或文件，不代表删除 Agent。

### 执行与文件

Windows PowerShell 5 在当前 LPAC 环境不兼容。使用 PowerShell 7 portable 时，由宿主把可信安装目录显式只读挂载，再执行其绝对路径，并设置 `DOTNET_EnableDiagnostics=0` 与 `POWERSHELL_TELEMETRY_OPTOUT=1`。库为 PowerShell 的 ETW 初始化增加固定的 `lpacInstrumentation` 能力；不更改全局 ETW／设备 ACL，不开放网络。程序是否兼容仍须实测，失败不得回退到宿主进程。

- `exec(argv, options)` 不调用宿主 shell。需要 shell 语法时，显式执行隔离环境中的 `/bin/sh -c` 或 `cmd.exe /d /s /c`。
- 最终 `stdout` / `stderr` 按 UTF-8 解码；传统 Windows 程序使用其他代码页时，应通过 `onOutput` 处理原始字节。
- 每次操作都是新的隔离进程环境。工作区持久化；HOME、TEMP、后台进程不跨调用保留。长任务应在同一次执行里完成。
- 程序退出码非零仍返回 `ExecResult`；超时、取消、输出超限、后端错误抛出 `NXContainerError`。
- 使用 `AbortSignal` 取消。主程序异常退出导致控制管道关闭时，执行器也会取消任务。
- 只继承固定运行环境和明确提供的 `env`，不会继承宿主应用的 API Key、HOME、代理、桌面连接变量。
- 文件路径必须为挂载内相对路径，`readFile` 返回 `Buffer`；`writeFile` 接受字符串或字节数组。
- 文件读写单次限制 1 MiB，列表最多 1000 项；大文件任务可用沙箱内程序，并由宿主管理磁盘配额。
- `writeFile` 在同目录创建临时文件后原子替换。父目录必须已存在，可先调用 `mkdir`。
- `removeFile` 只删除一个文件、链接或空目录，不递归删除。
- 命令输出默认合计 1 MiB，最多可配置 16 MiB；超时默认 60 秒，最多 1 小时。单次命令只能降低沙箱的超时上限。

## 目录结构

```text
src/                         Node.js SDK、策略校验与类型声明
native/cmd/nxcontainer/       原生执行器入口、单请求管道协议
native/internal/sandbox/      Windows/Linux 后端、权限与资源控制、文件执行器
scripts/                     本机/四目标构建、独立安装包验收
examples/                    临时工作区示例
test/                        SDK、协议与平台集成测试
docs/                        协议与后续实施计划
```

## 当前不是这些东西

NXContainer 不是虚拟机，不绕过宿主文件权限，不提供提权 shell，不把路径检查包装成 Windows 沙箱，不将不可信 Skill 加载到宿主 Node 进程，也不是业务应用或完整 GUI 产品。Linux 总体验证状态仍为 pending（已有的 Alpine 验收不代表广泛平台与安全验证完成）；崩溃恢复日志、磁盘配额和其他安全加固仍有明确限制，不能因依赖库分发完成就宣称生产安全。

## 作者与许可证

- 作者：MoyuZJ <moyuzj@nextorix.dev> @Nextorix
- Made in China with ♥
- Copyright (C) 2026 Nextorix
- 许可证：GPL-3.0-or-later，全文见 [LICENSE](LICENSE)。

源码和文档使用各自格式支持的文件头；`package.json` 使用作者及许可证元数据，生成的校验和文件、二进制与第三方许可证原文不插入注释。Go 运行时和 `golang.org/x/sys` 的 BSD 许可证原文随包放在 [licenses/Go-BSD.LICENSE](licenses/Go-BSD.LICENSE)。
