/*
 * NXContainer Error Types
 *
 * 定义 SDK 与原生执行器共用的结构化错误类型。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

export class NXContainerError extends Error {
  constructor(code, message, options) {
    super(message, options);
    this.name = 'NXContainerError';
    this.code = code;
  }
}

export function fail(code, message) {
  throw new NXContainerError(code, message);
}
