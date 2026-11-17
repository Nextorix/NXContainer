/*
 * NXContainer Licensing Metadata Tests
 *
 * 检查源码、文档和包元数据中的作者、版权与许可证声明。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));

async function* files(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (['bin', 'node_modules', '.git'].includes(entry.name)) continue;
    const name = path.join(directory, entry.name);
    if (entry.isDirectory()) yield* files(name);
    else if (entry.isFile()) yield name;
  }
}

test('源码和文档包含完整的作者与 GPL 文件头', async () => {
  let count = 0;
  for await (const name of files(root)) {
    if (!/\.(?:go|js|mjs|ts|md)$/.test(name) && !['go.mod', '.gitignore'].includes(path.basename(name))) continue;
    const prefix = (await readFile(name, 'utf8')).slice(0, 1500);
    for (const marker of [
      'NXContainer',
      'MoyuZJ <moyuzj@nextorix.dev> @Nextorix',
      'Made in China with ♥',
      'Copyright (C) 2026 Nextorix LLC.',
      'SPDX-License-Identifier: GPL-3.0-or-later',
    ]) assert.ok(prefix.includes(marker), `${path.relative(root, name)} 缺少 ${marker}`);
    count++;
  }
  assert.ok(count >= 25, '没有找到预期的源码和文档');
});

test('依赖库入口、类型和安装行为不耦合宿主应用', async () => {
  const manifest = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
  assert.equal(manifest.type, 'module');
  assert.equal(manifest.sideEffects, false);
  assert.equal(manifest.types, manifest.exports['.'].types);
  assert.deepEqual(Object.keys(manifest.exports), ['.']);
  assert.equal(Object.keys(manifest.dependencies ?? {}).length, 0);
  assert.equal(Object.keys(manifest.optionalDependencies ?? {}).length, 0);
  for (const lifecycle of ['preinstall', 'install', 'postinstall', 'prepare']) assert.equal(manifest.scripts[lifecycle], undefined);
  for (const entry of [manifest.types, manifest.exports['.'].import]) assert.ok((await readFile(path.join(root, entry), 'utf8')).length > 0);
});

test('包使用合法 JSON 元数据并随包分发 GPL 正文', async () => {
  const manifest = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
  assert.equal(manifest.author.name, 'MoyuZJ');
  assert.equal(manifest.author.email, 'moyuzj@nextorix.dev');
  assert.equal(manifest.license, 'GPL-3.0-or-later');
  assert.equal(manifest.copyright, 'Copyright (C) 2026 Nextorix LLC.');
  assert.ok(manifest.files.includes('LICENSE'));
  assert.ok(manifest.files.includes('licenses'));
  const thirdParty = await readFile(path.join(root, 'licenses/Go-BSD.LICENSE'), 'utf8');
  assert.match(thirdParty, /^Copyright 2009 The Go Authors\./);
  assert.match(thirdParty, /Redistribution and use in source and binary forms/);
  assert.doesNotMatch(thirdParty, /NXContainer|SPDX-License-Identifier: GPL/);
  const license = await readFile(path.join(root, 'LICENSE'), 'utf8');
  assert.match(license, /GNU GENERAL PUBLIC LICENSE/);
  assert.match(license, /Version 3, 29 June 2007/);
  assert.match(license, /Copyright \(C\) 2007 Free Software Foundation/);
});
