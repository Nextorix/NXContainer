/*
 * NXContainer Permission Policy
 *
 * 校验目录授权、Agent 身份、共享规则与执行限制。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import path from 'node:path';
import { realpath, lstat } from 'node:fs/promises';
import { fail } from './errors.js';

const ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
export const MAX_FILE_BYTES = 1024 * 1024;

export function object(value, keys, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) {
    fail('INVALID_CONFIG', `${label} 必须是普通对象`);
  }
  for (const key of Object.keys(value)) {
    if (!keys.includes(key)) fail('INVALID_CONFIG', `${label} 不支持字段 ${key}`);
  }
}

export function within(parent, child) {
  const rel = path.relative(parent, child);
  return !path.isAbsolute(rel) && rel !== '..' && !rel.startsWith(`..${path.sep}`);
}

export function overlap(a, b) {
  return within(a, b) || within(b, a);
}

export function integer(value, min, max, label) {
  if (!Number.isSafeInteger(value) || value < min || value > max) fail('INVALID_CONFIG', `${label} 必须在 ${min} 到 ${max} 之间`);
  return value;
}

// 拒绝符号链接目录，包括父目录；运行时仍会重新校验。
// 目录及其祖先必须由可信宿主管理，不能由 Agent 重命名或替换。
export async function directory(input) {
  if (typeof input !== 'string' || !path.isAbsolute(input) || input.includes('\0')) {
    fail('INVALID_CONFIG', '宿主目录必须是绝对路径');
  }
  const absolute = path.resolve(input);
  if (absolute === path.parse(absolute).root) fail('INVALID_CONFIG', '不能授权整个文件系统根目录');
  try {
    let current = absolute;
    while (current !== path.dirname(current)) {
      if ((await lstat(current)).isSymbolicLink()) fail('UNSAFE_PATH', '挂载目录及父目录不能是符号链接或目录联接');
      current = path.dirname(current);
    }
    if (!(await lstat(absolute)).isDirectory()) fail('INVALID_CONFIG', '挂载源必须是现有目录');
    return await realpath(absolute);
  } catch (error) {
    if (error.code?.startsWith('E')) fail('INVALID_CONFIG', `无法访问目录：${input}`);
    throw error;
  }
}

export async function normalizePolicy(input, allowedRoots, experimental) {
  object(input, ['agentId', 'mounts', 'network', 'desktop', 'limits'], '沙箱配置');
  if (typeof input.agentId !== 'string' || !ID.test(input.agentId)) fail('INVALID_CONFIG', 'agentId 必须是 1～64 位字母、数字、下划线或连字符');
  if ((input.network ?? 'none') !== 'none') fail('UNSUPPORTED_CAPABILITY', '当前只支持完全断网');
  if (input.desktop !== undefined && input.desktop !== false) fail('UNSUPPORTED_CAPABILITY', '当前版本尚不支持图形桌面');
  if (!Array.isArray(input.mounts) || input.mounts.length < 1 || input.mounts.length > 32) {
    fail('INVALID_CONFIG', '需要提供 1～32 个挂载目录');
  }
  const limits = input.limits ?? {};
  object(limits, ['timeoutMs', 'maxOutputBytes', 'memoryBytes', 'maxProcesses', 'cpuPercent'], '资源限制');
  const names = new Set();
  const mounts = [];
  for (const mount of input.mounts) {
    object(mount, ['source', 'name', 'access', 'alias'], '挂载');
    if (typeof mount.name !== 'string' || !ID.test(mount.name) || names.has(mount.name.toLowerCase())) fail('INVALID_CONFIG', '挂载名不合法或重复');
    names.add(mount.name.toLowerCase());
    if (!['read-only', 'read-write'].includes(mount.access)) fail('INVALID_CONFIG', '挂载权限必须是 read-only 或 read-write');
    const source = await directory(mount.source);
    if (!allowedRoots.some((root) => within(root, source))) fail('PERMISSION_DENIED', '挂载源不在管理方授权的目录范围内');
    if (mounts.some((other) => overlap(other.source, source))) fail('MOUNT_CONFLICT', '同一沙箱不能挂载重叠或重复的源目录');
    let alias;
    if (mount.alias !== undefined) {
      if (process.platform !== 'win32' || typeof mount.alias !== 'string' || !path.isAbsolute(mount.alias) || mount.alias.includes('\0')) fail('INVALID_CONFIG', 'alias 只支持 Windows 上由可信宿主创建的绝对目录联接');
      alias = path.resolve(mount.alias);
      const parent = await directory(path.dirname(alias));
      if (!allowedRoots.some((root) => within(root, parent))) fail('PERMISSION_DENIED', '联接父目录不在管理方授权范围内');
      if (!(await lstat(alias)).isSymbolicLink() || (await realpath(alias)).toLowerCase() !== source.toLowerCase()) fail('UNSAFE_PATH', 'alias 必须是直接指向当前挂载源的目录联接');
      if (overlap(parent, source)) fail('MOUNT_CONFLICT', '联接父目录与挂载源不能重叠');
    }
    mounts.push(Object.freeze({ source, name: mount.name, access: mount.access, ...(alias ? { alias } : {}) }));
  }
  for (const mount of mounts) {
    if (mount.alias && mounts.some((other) => overlap(path.dirname(mount.alias), other.source))) fail('MOUNT_CONFLICT', '显示联接的父目录不能位于任何挂载源内');
  }
  if (!mounts.some((m) => m.name === 'workspace')) {
    fail('INVALID_CONFIG', '必须提供名为 workspace 的工作区（可读写或只读）');
  }
  return Object.freeze({
    agentId: input.agentId,
    mounts: Object.freeze(mounts),
    network: 'none',
    desktop: false,
    experimental,
    limits: Object.freeze({
      timeoutMs: integer(limits.timeoutMs ?? 60_000, 1, 3_600_000, 'timeoutMs'),
      maxOutputBytes: integer(limits.maxOutputBytes ?? 1024 * 1024, 1024, 16 * 1024 * 1024, 'maxOutputBytes'),
      ...(limits.memoryBytes === undefined ? {} : { memoryBytes: integer(limits.memoryBytes, 64 * 1024 * 1024, 8 * 1024 * 1024 * 1024, 'memoryBytes') }),
      ...(limits.maxProcesses === undefined ? {} : { maxProcesses: integer(limits.maxProcesses, 1, 256, 'maxProcesses') }),
      ...(limits.cpuPercent === undefined ? {} : { cpuPercent: integer(limits.cpuPercent, 1, 100, 'cpuPercent') }),
    }),
  });
}

export function assertSharing(policy, existing) {
  for (const other of existing) {
    if (other.agentId.toLowerCase() === policy.agentId.toLowerCase()) fail('AGENT_EXISTS', '该 Agent 已有沙箱');
    const grants = (p) => p.mounts.flatMap((m) => [m, ...(m.alias ? [{ source: path.dirname(m.alias), access: 'read-only' }] : [])]);
    for (const a of grants(policy)) for (const b of grants(other)) {
      if (!overlap(a.source, b.source)) continue;
      if (a.access === 'read-write' && b.access === 'read-write') {
        fail('MOUNT_CONFLICT', '不同 Agent 不能拥有重叠的可写目录');
      }
      // 整个根目录可以显式只读共享；子目录可能被拥有父目录写权限的 Agent 替换。
      if (path.relative(a.source, b.source) !== '' && (a.access === 'read-write' || b.access === 'read-write')) {
        fail('MOUNT_CONFLICT', '不能共享其他可写挂载的子目录或祖先；请共享独立目录根');
      }
    }
  }
}

export function filePath(name) {
  if (typeof name !== 'string' || name.length > 4096 || name.includes('\0') || name.includes('\\') || name.includes(':') || name.startsWith('/')) {
    fail('INVALID_PATH', '文件路径必须是使用 / 分隔的相对路径');
  }
  if (name.split('/').some((part) => part === '..')) fail('INVALID_PATH', '文件路径不能包含 ..');
  return name || '.';
}
