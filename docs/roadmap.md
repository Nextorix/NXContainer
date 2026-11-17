<!--
 * NXContainer Library Roadmap
 *
 * 明确独立依赖库的交付边界、平台验证状态与后续加固方向。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 -->

# 依赖库范围与后续计划

## 当前定位

NXContainer 是可独立构建、打包、安装和导入的沙箱依赖库，不是 LMC 的内置业务模块，也不开发完整 GUI。

库负责：

- 不可变授权策略、沙箱句柄与生命周期。
- 隔离命令执行、输出流、超时、取消和进程树回收。
- 授权目录的文件接口与显式只读共享。
- 平台能力查询、验证状态和结构化错误。
- 已支持平台的资源限制及原生执行器分发。

消费应用负责：身份认证、业务权限、Agent 与目录的分配、模型工具协议、用户界面、业务审计存储及独立的宿主操作授权。LMC 可以作为消费方接入，但其服务、工具、模式与界面不属于本库交付。

## 已具备的库能力

- [x] 独立 ESM 包、TypeScript 声明、公开导出与 Go 执行器。
- [x] 不依赖 LMC、Cordis 或第三方 npm 模块；Windows API 使用 `golang.org/x/sys`。
- [x] 同一管理器内的目录写权限归属检查与显式只读共享。
- [x] 私有管道协议、输出预算、取消和父进程退出清理。
- [x] Windows LPAC、低完整性、额外特权永久移除、受控句柄继承与 Job Object。
- [x] Windows 稳定句柄授权、旧 DACL 恢复与新对象临时 SID 清理。
- [x] Windows CPU、提交内存、进程数限制及当前开发机上的隔离测试。
- [x] Windows 私有 Window Station/Desktop 安全边界，保留在库内部。
- [x] Linux bubblewrap、namespace、seccomp 后端代码与目标产物；**运行状态待验证**。
- [x] 本机及四目标构建入口；无自动下载或 npm 安装脚本。
- [x] 包内自带 Linux 执行器的执行位准备，失败时明确报错，不修改自定义执行器。
- [x] 独立消费应用的真实打包、离线安装、公开包名导入验收流程。

## 平台验证

| 目标 | 当前状态 |
|---|---|
| Windows x64 | 已做部分实机验证，仍为实验版本，未完成独立安全审计 |
| Windows arm64 | 待验证，仅交叉编译 |
| Linux x64 / arm64 | 待验证，不用交叉编译、自检成功或模拟测试代替实机验收 |

Linux 待验证不作为本轮依赖库封装的阻塞项，`validation: 'pending'` 和显式实验选项会持续保留。后续有原生 Linux 测试环境时，再运行强制集成测试、工具兼容性和对抗性测试。

## 后续库级加固

- [ ] Windows 非管理员承载账户、服务会话、arm64 实机及更多软件兼容性验证。
- [ ] 原生执行器硬崩溃/断电恢复日志、恶意 ACL/目录竞态的完整处理。
- [ ] 改善全局命名管道/NUL 兼容性，但不扩大宿主权限。
- [ ] Linux cgroup CPU/内存/PID 限制与磁盘配额方案；不静默忽略请求。
- [ ] Windows 工作区磁盘硬配额及可选的私有路径映射。
- [ ] 跨管理器目录归属协调、库级审计事件与独立安全审计。

这些限制仍属于安全与成熟度限制，不因“依赖库”定位而消失；具体见 SECURITY.md。

## 当前不开发

- 完整 GUI、持久桌面会话、截图/键鼠代理、剪贴板和 GPU 管理。
- Linux Wayland/Xwayland/Xvfb 显示服务。
- LMC/Cordis 服务、模式、模型工具、HTTP 接口、WebUI 或业务数据迁移。
- 自动创建 Windows 用户、启用系统特性或修改全局设备/管道权限。

`desktop: true` 继续返回不支持，不以已有的桌面隔离边界冒充完整 GUI 功能。所有库测试使用临时目录，不操作消费应用的真实业务数据。
