/*
 * NXContainer Runtime Model Tests
 *
 * 验证配置、命令、文件权限与符号链接越界防护。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{AgentID: "agent-a", Network: "none", Experimental: true,
		Mounts: []Mount{{Name: "workspace", Source: t.TempDir(), Access: "read-write"}},
		Limits: Limits{TimeoutMs: 1000, MaxOutputBytes: 1024},
	}
}

func TestValidationStatus(t *testing.T) {
	for _, tc := range []struct{ platform, architecture, want string }{
		{"windows", "amd64", "partial"}, {"windows", "arm64", "pending"},
		{"linux", "amd64", "pending"}, {"linux", "arm64", "pending"},
		{"windows", "386", "unsupported"}, {"darwin", "arm64", "unsupported"},
	} {
		if got := validationStatus(tc.platform, tc.architecture); got != tc.want {
			t.Fatalf("%s/%s：得到 %s，预期 %s", tc.platform, tc.architecture, got, tc.want)
		}
	}
}

func TestConfig(t *testing.T) {
	c := testConfig(t)
	if err := ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	internet := c
	internet.Network = "internet"
	if err := ValidateConfig(internet); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"身份越界", func(c *Config) { c.AgentID = "../other" }},
		{"网络提权", func(c *Config) { c.Network = "host" }},
		{"桌面未支持", func(c *Config) { c.Desktop = true }},
		{"没有工作区", func(c *Config) { c.Mounts = nil }},
		{"别名重复", func(c *Config) {
			c.Mounts = append(c.Mounts, Mount{Name: "WORKSPACE", Source: t.TempDir(), Access: "read-only"})
		}},
		{"源目录重叠", func(c *Config) {
			c.Mounts = append(c.Mounts, Mount{Name: "alias", Source: c.Mounts[0].Source, Access: "read-only"})
		}},
		{"没有权限类型", func(c *Config) { c.Mounts[0].Access = "" }},
		{"目录不存在", func(c *Config) { c.Mounts[0].Source = filepath.Join(c.Mounts[0].Source, "missing") }},
		{"无效超时", func(c *Config) { c.Limits.TimeoutMs = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := c
			candidate.Mounts = append([]Mount(nil), c.Mounts...)
			tc.change(&candidate)
			if ValidateConfig(candidate) == nil {
				t.Fatal("应当拒绝配置")
			}
		})
	}
}

func TestCommand(t *testing.T) {
	c := testConfig(t)
	valid := Command{Argv: []string{"/bin/sh", "-c", "printf ok"}, Cwd: "workspace", TimeoutMs: 1000}
	if err := ValidateCommand(c, valid); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{"/etc", "workspace/../other", "other", "workspace\\a", "workspace/a:b"} {
		command := valid
		command.Cwd = cwd
		if ValidateCommand(c, command) == nil {
			t.Fatalf("错误接受 cwd %q", cwd)
		}
	}
	command := valid
	command.Env = map[string]string{"HOME": "/host"}
	if ValidateCommand(c, command) == nil {
		t.Fatal("不能覆盖受控环境")
	}
	command = valid
	command.TimeoutMs++
	if ValidateCommand(c, command) == nil {
		t.Fatal("不能提升超时")
	}
}

func TestReadOnlyWorkspace(t *testing.T) {
	c := testConfig(t)
	c.Mounts[0].Access = "read-only"
	if err := ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"write", "remove", "mkdir"} {
		if _, err := prepareFile(c, FileRequest{Operation: operation, Mount: "workspace", Path: "a"}); err == nil {
			t.Fatalf("只读工作区接受了 %s", operation)
		}
	}
	for _, operation := range []string{"read", "list"} {
		if _, err := prepareFile(c, FileRequest{Operation: operation, Mount: "workspace", Path: "a"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFilePermissions(t *testing.T) {
	c := testConfig(t)
	c.Mounts = append(c.Mounts, Mount{Name: "peer", Source: t.TempDir(), Access: "read-only"})
	for _, operation := range []string{"write", "remove", "mkdir"} {
		_, err := prepareFile(c, FileRequest{Operation: operation, Mount: "peer", Path: "a"})
		if err == nil {
			t.Fatalf("只读目录接受了 %s", operation)
		}
	}
	if _, err := prepareFile(c, FileRequest{Operation: "read", Mount: "peer", Path: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareFile(c, FileRequest{Operation: "read", Mount: "unknown", Path: "a"}); err == nil {
		t.Fatal("未授权目录可读")
	}
}

func TestFileOperations(t *testing.T) {
	root := t.TempDir()
	perform := func(operation, name, data string) (FileResult, error) {
		return performFile(WorkerRequest{Root: root, File: FileRequest{Operation: operation, Path: name, Data: data}})
	}
	if _, err := perform("mkdir", "docs", ""); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("中文内容"))
	if _, err := perform("write", "docs/a.txt", encoded); err != nil {
		t.Fatal(err)
	}
	if value, err := perform("read", "docs/a.txt", ""); err != nil || value.Data != encoded {
		t.Fatalf("读取失败：%+v %v", value, err)
	}
	if value, err := perform("list", "docs", ""); err != nil || len(value.Entries) != 1 {
		t.Fatalf("列出失败：%+v %v", value, err)
	}
	if _, err := perform("remove", "docs/a.txt", ""); err != nil {
		t.Fatal(err)
	}
	if value, err := perform("list", "docs", ""); err != nil || len(value.Entries) != 0 {
		t.Fatal("空目录应返回空列表")
	}
	for _, name := range []string{"../escape", "/absolute", "a/../../b", "C:/Windows", "a\\b"} {
		if _, err := perform("write", name, encoded); err == nil {
			t.Fatalf("越界路径 %q 被接受", name)
		}
	}
}

func TestFileLinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("不应读取"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("系统不允许创建测试符号链接：", err)
	}
	for _, op := range []string{"read", "write", "remove", "mkdir"} {
		_, err := performFile(WorkerRequest{Root: root, File: FileRequest{Operation: op, Path: "escape/secret"}})
		if err == nil {
			t.Fatalf("链接越界操作 %s 被接受", op)
		}
	}
	data, err := os.ReadFile(filepath.Join(outside, "secret"))
	if err != nil || string(data) != "不应读取" {
		t.Fatal("沙箱外文件发生改变")
	}
}
