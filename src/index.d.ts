/*
 * NXContainer TypeScript Definitions
 *
 * 声明公开 SDK、权限策略、运行能力与执行结果的类型。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

/// <reference types="node" />

export class NXContainerError extends Error {
  readonly code: string;
  constructor(code: string, message: string, options?: ErrorOptions);
}
export const protocolVersion: 1;

export interface Mount {
  /** 由可信主程序指定的绝对宿主目录，不能来自模型参数。 */
  source: string;
  /** 逻辑挂载名；workspace 必须存在且可写。 */
  name: string;
  access: 'read-only' | 'read-write';
}
export interface SandboxConfig {
  agentId: string;
  mounts: Mount[];
  network?: 'none';
  /** 保留兼容字段；当前依赖库不提供完整 GUI，只接受 false。 */
  desktop?: false;
  limits?: { timeoutMs?: number; maxOutputBytes?: number; memoryBytes?: number; maxProcesses?: number; cpuPercent?: number };
}
export interface RuntimeOptions {
  /** 自定义执行器绝对路径；调用方负责构建及执行权限，库不会修改它。 */
  runtimePath?: string;
  allowedRoots: string[];
  /** Linux 待验证，仅供实验；尚未完成安全审计与进程/磁盘配额实现。 */
  allowExperimentalLinux?: boolean;
  /** Windows 执行会临时修改已授权目录的 DACL；需要宿主拥有 WRITE_DAC。 */
  allowExperimentalWindows?: boolean;
}
export interface Capabilities {
  platform: string;
  architecture: string;
  backend: string;
  status: 'experimental' | 'unavailable';
  /** 项目验证进度，不代表当前机器完成验收；Linux 为 pending（待验证）。 */
  validation: 'partial' | 'pending' | 'unsupported';
  exec: boolean;
  files: boolean;
  readOnlyMounts: boolean;
  privateFilesystem: boolean;
  networkNone: boolean;
  desktop: boolean;
  resourceQuotas: boolean;
  processQuotas: boolean;
  desktopIsolation: boolean;
  reason?: string;
}
export interface ExecOptions {
  /** 以挂载名开头的相对路径，例如 workspace/project。 */
  cwd?: string;
  env?: Record<string, string>;
  stdin?: string;
  timeoutMs?: number;
  signal?: AbortSignal;
  /** 按原始字节推送，解码流时请使用 StringDecoder，避免拆断 UTF-8。 */
  onOutput?: (event: { stream: 'stdout' | 'stderr'; data: Buffer }) => void;
}
export interface ExecResult { exitCode: number; durationMs: number; stdout: string; stderr: string }
export interface FileOptions { signal?: AbortSignal }
export interface FileEntry { name: string; directory: boolean; symlink: boolean; size: number }
export interface Sandbox {
  readonly agentId: string;
  readonly paths: Readonly<Record<string, string>>;
  exec(argv: string[], options?: ExecOptions): Promise<ExecResult>;
  readFile(mount: string, path: string, options?: FileOptions): Promise<Buffer>;
  writeFile(mount: string, path: string, data: string | Uint8Array, options?: FileOptions): Promise<void>;
  listFiles(mount: string, path?: string, options?: FileOptions): Promise<FileEntry[]>;
  mkdir(mount: string, path: string, options?: FileOptions): Promise<void>;
  removeFile(mount: string, path: string, options?: FileOptions): Promise<void>;
  /** 终止正在执行的任务并释放登记，不删除宿主工作区。 */
  dispose(): Promise<void>;
}
export class NXContainer {
  constructor(options: RuntimeOptions);
  capabilities(): Promise<Capabilities>;
  createSandbox(config: SandboxConfig): Promise<Sandbox>;
  close(): Promise<void>;
}
