<!--
 * NXContainer Verification Record
 *
 * 记录实际执行的验证、发现的兼容性边界以及尚未完成的验收。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 -->

# 当前依赖库验证记录

此文区分历史结果与本次独立库复验。NXContainer 不包含上层 GUI 或 LMC 业务实现；Linux 已有下述 Alpine 客体中的真实内核执行结果，`validation: 'pending'` 仍表示更广泛的平台与安全验证不足，不是否认已完成的限定环境验收。

## 实际运行环境

Windows 11 amd64，Go 1.27.4，Node.js 24.21.0。使用当前开发账户和临时目录，没有使用 LMC 的真实 `data/`，没有创建系统用户、安装虚拟机或修改系统范围的设备权限。

## 只读工作区与 Windows 显示联接复验（0.1.0-alpha.2）

- Windows x64：从待提交内容导出独立干净副本，四目标构建成功；`NXCONTAINER_REQUIRE_RUNTIME=1 npm test` 共 35 项，34 通过、0 失败，仅跳过 Linux 专项；原生 Go 测试、`go vet`、离线打包安装与仓库外消费端验收全部通过。
- Linux：Alpine 3.24 amd64/arm64，Node 24.22.3、bubblewrap 0.12.0、普通用户；只读介质、既有系统盘的临时 qcow2 覆盖层、串行 1 vCPU/1024 MiB 客体，不改原磁盘。两架构原生测试均通过；各 24 项 Node 测试中 22 通过、0 失败，仅跳过两个 Windows 专项；各自离线打包、安装与功能验收通过。
- 新增只读工作区用例实际拒绝文件 API 写入，以及 shell 覆盖、新建、删除、改名与建目录；确认原文件不变。释放后重新授予读写可用，不把提示词或命令字符串过滤当作只读隔离。
- Windows 显示联接分别验证目标身份、祖先/其他 Agent 的内容读写拒绝、只读重授权，以及正常/失败路径的 DACL 恢复。联接祖先只加属性读取、穿越、同步，不列目录或继承到子项；不改卷根 DACL。
- 上层临时数据探针实际验证 D 盘持久目录经 `C:/Agents/<UUID>/workspace` 执行 PowerShell 7.7.0 并写回源目录，释放后联接消失。未开放全局 ETW/设备 ACL，未增加网络能力；固定能力新增 `lpacInstrumentation` 的限制见 SECURITY.md。
- `cmd.exe del` 遇到拒绝访问时可能仍返回 0。测试核对错误输出与实际文件不变，不将退出码当作唯一权限证据。
- 软件模拟测试使用已有 `NXCONTAINER_TEST_SLOW=1`；启动客体时补齐正常设备节点权限。上层网关的 fetch 测试不能使用 `--jitless`（会禁用 WebAssembly）；这属于测试运行环境问题，没有为此放宽库权限或增加宿主后备。
- 以上不是 Windows arm64 实机验收、跨发行版保证或独立安全审计；不把以下历史漏洞扫描结果当作对新代码的完整安全证明。

## Windows 独立复测与 Linux CI（2026-11-18，先前批次）

复测前，Git 历史仅有初始化提交 `b0a3943`：已包含 JS SDK、Go 执行器、两套平台后端和包验收，但没有 GitHub Actions 工作流。以下是新增验证，不把历史记录当作本轮结果。

- Windows x64：在临时目录独立克隆 `a18609c`，确认没有 `bin/` 后执行 `npm run build:all`。以 `NXCONTAINER_REQUIRE_RUNTIME=1` 运行 `npm test`：32 项中 31 项通过、0 项失败，唯一跳过的是 Linux 专项。`go test -count=1 ./...`、`go vet ./...`（在 `native/` 中）、`npm run test:package` 和临时工作区示例均通过。
- 回归用例先复现了输出错误：`onOutput` 回调改写收到的字节，会连带改变最终返回的 stdout/stderr。现在回调接收独立副本，回归测试通过。仓库外消费端另测了恰好 1 MiB 二进制文件的写入及只读共享读取，以及多 1 字节时拒写。
- Linux：`.github/workflows/linux.yml` 在 Ubuntu 24.04 x64 和 arm64 runner 上分别构建并以普通用户强制执行集成和离线安装测试。缺少执行器或隔离后端时不得以跳过或仅验证导入报告成功。工作流通过 `actionlint` 后，在两个架构的 Actions runner 上实际运行了 Go 测试、静态检查和构建。[首次运行](https://github.com/Nextorix/NXContainer/actions/runs/44229152046)与[诊断运行](https://github.com/Nextorix/NXContainer/actions/runs/44229832854)均未通过隔离集成测试。诊断运行 `6fc7bf1` 的两份内核日志都显示 AppArmor 的 `unprivileged_userns` 策略拒绝了 bubblewrap 初始化隔离网络所需的权限，自检报 `Failed RTM_NEWADDR: Operation not permitted`。该次运行未执行随后安排的安装包验收和漏洞扫描。**当前托管环境不满足后端前提，Linux 功能验收未通过**。不得将执行器在不兼容宿主上按预期拒绝启动计作隔离功能通过。

### Go 依赖漏洞修复与静默复验

`govulncheck v1.10.0` 使用 Go 官方漏洞库（更新时间 2026-11-11 02:51 UTC）。修复前，Windows x64 和 arm64 执行器均命中 [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024)，命中符号为 `windows.NewNTUnicodeString`；Linux 两个产物未命中。已将 `golang.org/x/sys` 从 v0.41.0 升至修复版本 v0.44.0。

- Windows x64 本机后台、无交互复验：`npm run build:all`、`go mod verify`、不使用测试结果缓存的 `go test -count=1 ./...`、`go vet ./...`、强制执行器的 `npm test`（31 通过、0 失败，仅跳过 Linux 专项）、`npm run test:package` 及临时工作区示例均通过。
- 对重建的四个 `bin/nxcontainer-*` 逐一执行 `govulncheck -mode=binary`，并在 Windows 的 `native/` 下执行 `govulncheck ./...`，均未报告已知漏洞。Linux 工作流已配置构建后扫描四个产物，命中则失败；工作流本身通过 `actionlint`，不等于远端作业已执行。
- 此扫描覆盖 Go 漏洞库中已知的问题，不覆盖自有隔离逻辑、宿主内核或 bubblewrap 的全部风险，不作为“无高危漏洞”的证明。Linux 仍需提供对应提交的实际 Actions 运行结果。

### 原生源码静态检查

除依赖库扫描外，另用 `gosec v2.30.0` 在 `native/` 下运行 `gosec -severity=high ./...`。初扫 Windows amd64 有 6 条 G115 整数转换告警，Linux amd64 有 1 条。这是静态分析告警，不代表发现 7 个可利用漏洞。

- Windows 使用固定 32 位 HRESULT 的失败位，ACL 清理从 `acl.AceCount` 倒序遍历，不再按偏移读长度。当时的进程能力列表在传给系统 API 前显式检查恰有一个条目；alpha.2 增加诊断初始化能力后仍核对固定的两个能力 SID。
- Linux 按低 32 位识别受保护文件系统的魔数，不依赖高位的符号扩展。增加了普通文件系统、受保护类型及两种扩展形式的单元测试；Windows 本机只交叉编译该 Linux 测试；上述两架构 Actions 的 `go test ./...` 已实际通过。
- 改后用 `CGO_ENABLED=0`、分别设置 `GOOS=windows/linux` 和 `GOARCH=amd64/arm64`，四种目标配置复扫均为 0 条高严重性告警、0 条包加载错误，无 `#nosec` 排除。四个重建产物的 `govulncheck -mode=binary` 均未命中已知漏洞。上述结果限于相应扫描规则及 Go 代码范围，并非独立安全审计。
- 改后 Windows x64 后台无交互复测，`go test -count=1 ./...`、`go vet ./...`、`npm test`（31 通过、0 失败、仅 Linux 专项跳过）、打包验收及示例均通过。

### Alpine 3.24 本地虚拟机复验

在本机 QEMU 中串行启动 Alpine 3.24 amd64 和 arm64 客体，每台配置 1 个 vCPU、1024 MiB 内存，使用只读测试介质和各自的临时虚拟磁盘。安装后的系统以普通用户执行测试，使用发行版的非 setuid bubblewrap 0.12.0；未放宽沙箱权限。

- 两种架构的 Go 测试均通过；Node 测试各 20 项通过、0 项失败，仅跳过 Windows 专项。Linux 隔离、只读共享、文件接口、取消和输出限制的集成用例实际执行通过。
- 两种架构的离线安装及仓库外消费端功能验收均通过，两台虚拟机均在退出码为 0 后关机。
- 初次运行中，软件模拟下 Node 子进程启动和 npm 打包超过测试工具的等待期限。复验显式使用 `NXCONTAINER_TEST_SLOW=1`，只调整协议测试子进程启动等待、Linux 集成测试父进程就绪等待及打包验收等待；默认测试期限、专门测试超时的断言和产品运行限制不变。
- 此结果仅验证了该 Alpine 客体配置。GitHub Actions 的 Ubuntu 24.04 托管环境仍因 AppArmor 策略未通过集成验收；`validation: 'pending'` 保持不变。

## 初始化提交记录的验证

- `npm test`：31 项，30 项通过，1 项 Linux 集成测试标记待验证并跳过。
- `npm run test:native`：通过；Windows 边界辅助用例会在受控 LPAC 子进程中执行，正常宿主测试入口会跳过该辅助入口。
- `npm run check:native`：通过。
- `node examples/temporary-workspace.mjs`：Windows 上完成双 Agent 文件共享、命令执行和只读拒绝。
- `npm run build:all`：Windows amd64/arm64、Linux amd64/arm64 执行器编译通过。
- Linux amd64/arm64 原生测试二进制：编译通过，未执行。
- `npm run test:package`：真实打包、在仓库外临时应用离线安装、禁用安装脚本并清空运行阶段 PATH 后，通过公开 `nxcontainer` 包名验证导入、命令、文件、只读共享、输出和释放。消费端不需要 Go 构建。
- 分发内容检查：包含源码、类型声明、四目标产物、GPL 正文和 BSD 许可证；不包含宿主应用目录，无第三方 npm 运行依赖或安装脚本。
- Linux 自带执行器的执行位准备通过模拟单元测试：只操作已打开的普通文件句柄，拒绝提权位、叶子链接和无法准备的只读目录。真实 Linux 文件系统行为仍待验证。

交叉编译不是目标平台执行验证。

## Windows 已验证的具体行为

- 真实 LPAC 进程、独立 SID、低完整性、无 UIAccess、指定能力列表及额外特权的永久移除。
- 两个身份同时存在时，不能读取另一身份的私有文件。
- 显式只读共享可读；写入、删除、改 DACL 被拒绝，包括由另一个 Agent 新建的文件。
- 文件读写、创建目录、列表、删除、中文路径与中文文件内容。
- 恢复原有对象的 DACL，清除新文件上的临时 SID；拒绝授权树中的 junction。
- 不继承宿主环境秘密；不能覆盖受控目录环境变量，也不能通过大小写重复绕过检查。
- portable Node 基础运行；继承标准流的子进程仍受相同权限边界限制。
- 连接宿主真实 TCP 回环监听端口被拒绝，监听端没有接收到连接。
- 超时、输出上限、取消、父进程被强制终止时，任务不能留下延迟写文件的子进程。
- 进程数量和提交内存硬限制；从内核查询确认 CPU 硬上限设置。
- 分别拒绝读取/写入宿主进程内存、复制句柄、终止宿主进程和创建远程线程的权限。
- 分别拒绝打开宿主与另一实例 Window Station 的屏幕、剪贴板及桌面枚举权限。

## 实际发现且没有绕过的限制

1. 仅创建低盒令牌不能替代特权检查。启动器现在在恢复主线程前永久移除额外特权，再重新核验。
2. 缺少 `registryRead` 时，Win32/Winsock 初始化会失败，影响 Node 和 Go 文件操作。当时只增加该能力；alpha.2 为 PowerShell ETW 初始化另外加入 `lpacInstrumentation`，仍不增加网络能力。
3. Node/libuv 的默认子进程管道使用全局命名管道，受到拒绝后可能不断重试；测试确认超时能够回收。继承标准流可用，默认管道尚不兼容。
4. `stdio: 'ignore'` 所需的 NUL 设备访问也受限制，没有修改全局设备 ACL 来掩盖问题。
5. 为保证新建文件能继续访问，目录授权必须可继承；回收时需要额外清除新对象上的临时 SID。

## 待验证与库级限制

- Linux 已完成上述 Alpine 客体限定配置的运行验收，但其他发行版与全面安全验证未完成（总状态仍为 `validation: 'pending'`）；cgroup CPU/内存/PID 限制和两端磁盘硬配额尚未实现。
- Windows arm64、非管理员专用账户、无人登录服务会话的实机测试。
- Windows 私有路径映射、通用 portable 软件兼容性。
- 原生执行器硬崩溃/断电恢复日志、恶意 ACL/目录竞态的完整处理、审计接口。
- 跨管理器目录归属协调。
- 独立安全审计。

## 不在当前依赖库范围

- 完整 GUI、持久桌面、截图/输入、剪贴板、GPU 管理。
- LMC/Cordis 服务、模型工具、HTTP 接口、WebUI 及业务数据集成。

Windows 私有桌面仍作为内部安全边界保留测试，不把它当作 GUI API。虚拟机客体中的 Linux 内核执行属于实际运行，但必须注明客体配置与用例结果；不以仅启动虚拟机、跳过测试或编译成功替代功能验收。
