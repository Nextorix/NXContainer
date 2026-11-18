/*
 * NXContainer Node.js SDK
 *
 * 可信主程序使用的沙箱生命周期、命令执行与文件访问接口。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { NXContainerError, fail } from './errors.js';
import { assertSharing, directory, filePath, integer, MAX_FILE_BYTES, normalizePolicy, object, within } from './policy.js';
import { invoke } from './transport.js';
import { prepareBundledRuntime } from './runtime.js';

export { NXContainerError };
export const protocolVersion = 1;

const suffix = process.platform === 'win32' ? '.exe' : '';
const defaultBinary = fileURLToPath(new URL(`../bin/nxcontainer-${process.platform}-${process.arch}${suffix}`, import.meta.url));

export class NXContainer {
  #binary;
  #bundled;
  #runtimeReady;
  #roots;
  #experimental;
  #agents = new Map();
  #closed = false;
  #creation = Promise.resolve();
  #lifetime = new AbortController();

  constructor(options) {
    object(options, ['runtimePath', 'allowedRoots', 'allowExperimentalLinux', 'allowExperimentalWindows'], '运行时选项');
    if (!Array.isArray(options.allowedRoots) || !options.allowedRoots.length) fail('INVALID_CONFIG', '必须明确设置 allowedRoots');
    if (options.allowedRoots.some((p) => typeof p !== 'string' || !path.isAbsolute(p))) fail('INVALID_CONFIG', 'allowedRoots 必须是绝对目录');
    if (options.runtimePath !== undefined && (typeof options.runtimePath !== 'string' || !path.isAbsolute(options.runtimePath))) {
      fail('INVALID_CONFIG', 'runtimePath 必须是绝对路径');
    }
    for (const key of ['allowExperimentalLinux', 'allowExperimentalWindows']) {
      if (options[key] !== undefined && typeof options[key] !== 'boolean') fail('INVALID_CONFIG', `${key} 必须是布尔值`);
    }
    this.#binary = options.runtimePath ?? defaultBinary;
    this.#bundled = options.runtimePath === undefined;
    this.#roots = [...options.allowedRoots];
    this.#experimental = (process.platform === 'win32' ? options.allowExperimentalWindows : options.allowExperimentalLinux) === true;
  }

  async capabilities() {
    this.#assertOpen();
    await this.#prepareRuntime();
    return (await invoke(this.#binary, { operation: 'capabilities' }, { signal: this.#lifetime.signal })).result;
  }

  createSandbox(config) {
    // 目录归属登记串行化，避免两个并发创建绕过重叠检查。
    const create = this.#creation.then(async () => {
      this.#assertOpen();
      const roots = await Promise.all(this.#roots.map(directory));
      const policy = await normalizePolicy(config, roots, this.#experimental);
      if (policy.mounts.some((mount) => mount.access === 'read-write' && within(mount.source, this.#binary))) {
        fail('PERMISSION_DENIED', '不能把包含执行器的目录授予 Agent 写权限');
      }
      assertSharing(policy, [...this.#agents.values()].map((entry) => entry.policy));
      await this.#prepareRuntime();
      const { result } = await invoke(this.#binary, { operation: 'prepare', sandbox: policy }, { signal: this.#lifetime.signal });
      this.#assertOpen();
      const sandbox = new Sandbox(this.#binary, policy, result, () => this.#agents.delete(policy.agentId));
      this.#agents.set(policy.agentId, { policy, sandbox });
      return sandbox;
    });
    this.#creation = create.catch(() => {});
    return create;
  }

  async close() {
    this.#closed = true;
    this.#lifetime.abort();
    await this.#creation;
    await Promise.all([...this.#agents.values()].map(({ sandbox }) => sandbox.dispose()));
  }

  async #prepareRuntime() {
    if (this.#bundled) {
      await (this.#runtimeReady ??= prepareBundledRuntime(this.#binary).catch((error) => {
        this.#runtimeReady = undefined; // 宿主修复安装目录后允许显式重试。
        throw error;
      }));
    }
    this.#assertOpen();
  }

  #assertOpen() {
    if (this.#closed) fail('CLOSED', 'NXContainer 已关闭');
  }
}

class Sandbox {
  #binary;
  #policy;
  #release;
  #active = null;
  #closed = false;
  #lifetime = new AbortController();
  #info;
  #released = false;

  constructor(binary, policy, info, release) {
    this.#binary = binary;
    this.#policy = policy;
    this.#release = release;
    this.#info = Object.freeze({ ...info, paths: Object.freeze({ ...info.paths }) });
  }

  get agentId() { return this.#policy.agentId; }
  get paths() { return this.#info.paths; }

  async exec(argv, options = {}) {
    object(options, ['cwd', 'env', 'stdin', 'timeoutMs', 'signal', 'onOutput'], '命令选项');
    const timeoutMs = integer(options.timeoutMs ?? this.#policy.limits.timeoutMs, 1, this.#policy.limits.timeoutMs, 'timeoutMs');
    const { result, stdout, stderr } = await this.#run({
      operation: 'exec',
      command: { argv, cwd: options.cwd ?? 'workspace', env: options.env ?? {}, stdin: options.stdin ?? '', timeoutMs },
    }, { signal: options.signal, onOutput: options.onOutput, timeoutMs: timeoutMs + 15_000, maxOutputBytes: this.#policy.limits.maxOutputBytes });
    return { ...result, stdout: stdout.toString('utf8'), stderr: stderr.toString('utf8') };
  }

  async readFile(mount, name, options = {}) {
    const result = await this.#file('read', mount, name, options);
    return Buffer.from(result.data, 'base64');
  }

  async writeFile(mount, name, data, options = {}) {
    if (typeof data !== 'string' && !(data instanceof Uint8Array)) fail('INVALID_CONFIG', '文件内容必须是字符串或字节数组');
    const bytes = Buffer.from(data);
    if (bytes.length > MAX_FILE_BYTES) fail('FILE_TOO_LARGE', '文件接口单次最多写入 1 MiB');
    await this.#file('write', mount, name, options, bytes.toString('base64'));
  }

  async listFiles(mount, name = '.', options = {}) { return (await this.#file('list', mount, name, options)).entries; }
  async mkdir(mount, name, options = {}) { await this.#file('mkdir', mount, name, options); }
  async removeFile(mount, name, options = {}) { await this.#file('remove', mount, name, options); }

  async #file(operation, mount, name, options, data = '') {
    object(options, ['signal'], '文件选项');
    const grant = this.#policy.mounts.find((item) => item.name === mount);
    if (!grant) fail('PERMISSION_DENIED', '未授权的挂载');
    if (['write', 'mkdir', 'remove'].includes(operation) && grant.access !== 'read-write') fail('PERMISSION_DENIED', '该挂载是只读的');
    const { result, stdout } = await this.#run({
      operation: 'file', file: { operation, mount, path: filePath(name), data },
    }, { signal: options.signal, timeoutMs: this.#policy.limits.timeoutMs + 15_000, maxOutputBytes: 2 * 1024 * 1024 });
    if (result.exitCode !== 0) fail('FILE_OPERATION_FAILED', '沙箱内文件执行器异常退出');
    let response;
    try { response = JSON.parse(stdout.toString('utf8')); }
    catch { fail('PROTOCOL_ERROR', '文件执行器返回了无效数据'); }
    if (response.error) fail(response.error.code, response.error.message);
    return response.result;
  }

  async #run(request, options) {
    if (this.#closed) fail('CLOSED', '沙箱已释放');
    if (this.#active) fail('AGENT_BUSY', '同一 Agent 同时只能执行一个操作');
    const signal = options.signal ? AbortSignal.any([this.#lifetime.signal, options.signal]) : this.#lifetime.signal;
    const promise = invoke(this.#binary, { ...request, sandbox: this.#policy }, { ...options, signal });
    this.#active = promise;
    try { return await promise; }
    finally { this.#active = null; }
  }

  async dispose() {
    this.#closed = true;
    this.#lifetime.abort();
    try { await this.#active; } catch { /* 释放时等待取消完成，不覆盖调用方收到的错误。 */ }
    if (!this.#released) {
      this.#released = true;
      this.#release();
    }
  }
}
