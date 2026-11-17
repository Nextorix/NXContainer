/*
 * NXContainer Runtime Transport
 *
 * 通过私有管道传输有版本的执行请求、输出事件与取消信号。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { spawn } from 'node:child_process';
import { NXContainerError } from './errors.js';

const MAX_LINE = 128 * 1024;
const MAX_REQUEST = 2 * 1024 * 1024;

// 每个请求启动一个执行器，控制管道只属于父进程，不向沙箱继承。
export function invoke(binary, request, { signal, onOutput, timeoutMs = 15_000, maxOutputBytes = 2 * 1024 * 1024, args = ['--stdio'] } = {}) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new NXContainerError('ABORTED', '操作已取消'));
    const input = JSON.stringify({ version: 1, ...request });
    if (Buffer.byteLength(input) > MAX_REQUEST) return reject(new NXContainerError('REQUEST_TOO_LARGE', '请求过大'));
    const child = spawn(binary, args, { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, shell: false });
    let pending = Buffer.alloc(0);
    let terminal;
    let failure;
    let stderr = '';
    let bytes = 0;
    const stdoutChunks = [];
    const stderrChunks = [];
    let killTimer;
    const stop = (error) => {
      if (failure) return;
      failure = error;
      // Windows 上 SIGTERM 会直接终止进程，跳过 ACL 恢复。先关闭控制管道，让原生执行器清理。
      child.stdin.end();
      killTimer = setTimeout(() => child.kill('SIGKILL'), 10_000);
      killTimer.unref();
    };
    const onAbort = () => stop(new NXContainerError('ABORTED', '操作已取消'));
    signal?.addEventListener('abort', onAbort, { once: true });
    const timer = setTimeout(() => stop(new NXContainerError('RUNTIME_TIMEOUT', '执行器未在期限内退出')), timeoutMs);
    timer.unref();
    if (signal?.aborted) onAbort();

    function accept(line) {
      const event = JSON.parse(line.toString('utf8'));
      if (event.version !== 1 || terminal) throw new Error('协议版本或事件顺序不正确');
      if (event.type === 'stdout' || event.type === 'stderr') {
        if (typeof event.data !== 'string') throw new Error('输出事件缺少数据');
        const data = Buffer.from(event.data, 'base64');
        bytes += data.length;
        if (bytes > maxOutputBytes) {
          stop(new NXContainerError('OUTPUT_LIMIT', '输出超过限制'));
          return;
        }
        (event.type === 'stdout' ? stdoutChunks : stderrChunks).push(data);
        onOutput?.({ stream: event.type, data });
      } else if (event.type === 'result') {
        terminal = event;
      } else if (event.type === 'error' && typeof event.error?.code === 'string' && typeof event.error?.message === 'string') {
        terminal = event;
      } else {
        throw new Error('未知协议事件');
      }
    }

    child.stdout.on('data', (chunk) => {
      if (failure) return;
      pending = Buffer.concat([pending, chunk]);
      try {
        let end;
        while ((end = pending.indexOf(10)) !== -1) {
          if (end > MAX_LINE) throw new Error('协议行过长');
          const line = pending.subarray(0, end);
          pending = pending.subarray(end + 1);
          accept(line);
          if (failure) return;
        }
        if (pending.length > MAX_LINE) throw new Error('协议行过长');
      } catch (cause) {
        stop(new NXContainerError('PROTOCOL_ERROR', `执行器协议错误：${cause.message}`, { cause }));
      }
    });
    child.stderr.on('data', (chunk) => {
      if (stderr.length < 4096) stderr += chunk.toString('utf8').slice(0, 4096 - stderr.length);
    });
    child.on('error', (cause) => {
      failure ??= new NXContainerError('RUNTIME_UNAVAILABLE', `无法启动 NXContainer 执行器：${cause.message}`, { cause });
    });
    child.stdin.on('error', (cause) => {
      if (cause.code !== 'EPIPE') stop(new NXContainerError('PROTOCOL_ERROR', '无法写入执行器请求', { cause }));
    });
    child.on('close', (code) => {
      clearTimeout(timer);
      clearTimeout(killTimer);
      signal?.removeEventListener('abort', onAbort);
      if (failure) return reject(failure);
      if (pending.length || !terminal) return reject(new NXContainerError('PROTOCOL_ERROR', `执行器提前退出（${code}）${stderr ? `：${stderr}` : ''}`));
      if (terminal.type === 'error') return reject(new NXContainerError(terminal.error.code, terminal.error.message));
      if (code !== 0) return reject(new NXContainerError('RUNTIME_FAILED', `执行器异常退出（${code}）`));
      resolve({ result: terminal.result, stdout: Buffer.concat(stdoutChunks), stderr: Buffer.concat(stderrChunks) });
    });
    // 保持管道打开；父进程退出时，原生执行器收到 EOF 并清理进程树。
    child.stdin.write(`${input}\n`);
  });
}
