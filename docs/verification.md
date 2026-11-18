<!--
 * NXContainer Verification Record
 *
 * 记录实际执行的验证、发现的兼容性边界以及尚未完成的验收。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 -->

# 当前依赖库验证记录

本轮按独立依赖库验收：不包含完整 GUI 或 LMC 业务集成。Linux 保留实验性实现并明确标记待验证，不用其缺少实机结果阻塞库的封装，也不将其记为已经通过。

## 实际运行环境

Windows 11 amd64，Go 1.27.4，Node.js 24.21.0。使用当前开发账户和临时目录，没有使用 LMC 的真实 `data/`，没有创建系统用户、安装虚拟机或修改系统范围的设备权限。

## Windows 独立复测与 Linux CI（2026-11-18）

复测前，Git 历史仅有初始化提交 `b0a3943`：已包含 JS SDK、Go 执行器、两套平台后端和包验收，但没有 GitHub Actions 工作流。以下是新增验证，不把历史记录当作本轮结果。

- Windows x64：在临时目录独立克隆 `a18609c`，确认没有 `bin/` 后执行 `npm run build:all`。以 `NXCONTAINER_REQUIRE_RUNTIME=1` 运行 `npm test`：32 项中 31 项通过、0 项失败，唯一跳过的是 Linux 专项。`go test -count=1 ./...`、`go vet ./...`（在 `native/` 中）、`npm run test:package` 和临时工作区示例均通过。
- 回归用例先复现了输出错误：`onOutput` 回调改写收到的字节，会连带改变最终返回的 stdout/stderr。现在回调接收独立副本，回归测试通过。仓库外消费端另测了恰好 1 MiB 二进制文件的写入及只读共享读取，以及多 1 字节时拒写。
- Linux：`.github/workflows/linux.yml` 在 Ubuntu 24.04 x64 和 arm64 runner 上分别构建并以普通用户强制执行集成和离线安装测试。缺少执行器或隔离后端时不得以跳过或仅验证导入报告成功。工作流已通过 `actionlint`，Linux 两架构的 Go 测试已交叉编译；本轮因推送未获 GitHub 认证，没有远端运行编号，**Linux 仍待验证**。

### Go 依赖漏洞修复与静默复验

`govulncheck v1.10.0` 使用 Go 官方漏洞库（更新时间 2026-11-11 02:51 UTC）。修复前，Windows x64 和 arm64 执行器均命中 [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024)，命中符号为 `windows.NewNTUnicodeString`；Linux 两个产物未命中。已将 `golang.org/x/sys` 从 v0.41.0 升至修复版本 v0.44.0。

- Windows x64 本机后台、无交互复验：`npm run build:all`、`go mod verify`、不使用测试结果缓存的 `go test -count=1 ./...`、`go vet ./...`、强制执行器的 `npm test`（31 通过、0 失败，仅跳过 Linux 专项）、`npm run test:package` 及临时工作区示例均通过。
- 对重建的四个 `bin/nxcontainer-*` 逐一执行 `govulncheck -mode=binary`，并在 Windows 的 `native/` 下执行 `govulncheck ./...`，均未报告已知漏洞。Linux 工作流已配置构建后扫描四个产物，命中则失败；工作流本身通过 `actionlint`，不等于远端作业已执行。
- 此扫描覆盖 Go 漏洞库中已知的问题，不覆盖自有隔离逻辑、宿主内核或 bubblewrap 的全部风险，不作为“无高危漏洞”的证明。Linux 仍需提供对应提交的实际 Actions 运行结果。

### 原生源码静态检查

除依赖库扫描外，另用 `gosec v2.30.0` 在 `native/` 下运行 `gosec -severity=high ./...`。初扫 Windows amd64 有 6 条 G115 整数转换告警，Linux amd64 有 1 条。这是静态分析告警，不代表发现 7 个可利用漏洞。

- Windows 使用固定 32 位 HRESULT 的失败位，ACL 清理从 `acl.AceCount` 倒序遍历，不再按偏移读长度。进程能力列表在传给系统 API 前显式检查恰有一个条目。
- Linux 按低 32 位识别受保护文件系统的魔数，不依赖高位的符号扩展。增加了普通文件系统、受保护类型及两种扩展形式的单元测试；在 Windows 只交叉编译了该 Linux 测试，实跑留给 Actions。
- 改后用 `CGO_ENABLED=0`、分别设置 `GOOS=windows/linux` 和 `GOARCH=amd64/arm64`，四种目标配置复扫均为 0 条高严重性告警、0 条包加载错误，无 `#nosec` 排除。四个重建产物的 `govulncheck -mode=binary` 均未命中已知漏洞。上述结果限于相应扫描规则及 Go 代码范围，并非独立安全审计。
- 改后 Windows x64 后台无交互复测，`go test -count=1 ./...`、`go vet ./...`、`npm test`（31 通过、0 失败、仅 Linux 专项跳过）、打包验收及示例均通过。

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
2. 缺少 `registryRead` 时，Win32/Winsock 初始化会失败，影响 Node 和 Go 文件操作。现在只增加该能力，不增加网络能力。
3. Node/libuv 的默认子进程管道使用全局命名管道，受到拒绝后可能不断重试；测试确认超时能够回收。继承标准流可用，默认管道尚不兼容。
4. `stdio: 'ignore'` 所需的 NUL 设备访问也受限制，没有修改全局设备 ACL 来掩盖问题。
5. 为保证新建文件能继续访问，目录授权必须可继承；回收时需要额外清除新对象上的临时 SID。

## 待验证与库级限制

- Linux 实机执行保持待验证（`validation: 'pending'`）；Linux cgroup CPU/内存/PID 限制和两端磁盘硬配额尚未实现。
- Windows arm64、非管理员专用账户、无人登录服务会话的实机测试。
- Windows 私有路径映射、通用 portable 软件兼容性。
- 原生执行器硬崩溃/断电恢复日志、恶意 ACL/目录竞态的完整处理、审计接口。
- 跨管理器目录归属协调。
- 独立安全审计。

## 不在当前依赖库范围

- 完整 GUI、持久桌面、截图/输入、剪贴板、GPU 管理。
- LMC/Cordis 服务、模型工具、HTTP 接口、WebUI 及业务数据集成。

Windows 私有桌面仍作为内部安全边界保留测试，不把它当作 GUI API。Linux 后续有原生测试环境时再验收；不以虚拟机、跳过测试或编译成功代替真实执行结果。
