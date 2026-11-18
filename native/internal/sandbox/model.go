/*
 * NXContainer Runtime Model
 *
 * 定义请求协议、沙箱策略、能力报告与原生权限校验。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package sandbox 实现受控请求、权限校验和平台隔离后端。
package sandbox

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const ProtocolVersion = 1
const MaxFileBytes = 1024 * 1024
const MaxRequestBytes = 2 * 1024 * 1024

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string            { return e.Message }
func problem(code, message string) *Error { return &Error{code, message} }

type Mount struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Access string `json:"access"`
	Alias  string `json:"alias,omitempty"`
}

type Limits struct {
	TimeoutMs      int    `json:"timeoutMs"`
	MaxOutputBytes int    `json:"maxOutputBytes"`
	MemoryBytes    uint64 `json:"memoryBytes,omitempty"`
	MaxProcesses   uint32 `json:"maxProcesses,omitempty"`
	CPUPercent     uint32 `json:"cpuPercent,omitempty"`
}

type Config struct {
	AgentID      string  `json:"agentId"`
	Mounts       []Mount `json:"mounts"`
	Network      string  `json:"network"`
	Desktop      bool    `json:"desktop"`
	Experimental bool    `json:"experimental"`
	Limits       Limits  `json:"limits"`
}

type Command struct {
	Argv      []string          `json:"argv"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	Stdin     string            `json:"stdin"`
	TimeoutMs int               `json:"timeoutMs"`
}

type FileRequest struct {
	Operation string `json:"operation"`
	Mount     string `json:"mount"`
	Path      string `json:"path"`
	Data      string `json:"data,omitempty"`
}

type Request struct {
	Version   int         `json:"version"`
	Operation string      `json:"operation"`
	Sandbox   Config      `json:"sandbox"`
	Command   Command     `json:"command"`
	File      FileRequest `json:"file"`
}

type Capabilities struct {
	Platform          string `json:"platform"`
	Architecture      string `json:"architecture"`
	Backend           string `json:"backend"`
	Status            string `json:"status"`
	Validation        string `json:"validation"`
	Exec              bool   `json:"exec"`
	Files             bool   `json:"files"`
	ReadOnlyMounts    bool   `json:"readOnlyMounts"`
	PrivateFilesystem bool   `json:"privateFilesystem"`
	NetworkNone       bool   `json:"networkNone"`
	Desktop           bool   `json:"desktop"`
	ResourceQuotas    bool   `json:"resourceQuotas"`
	Reason            string `json:"reason,omitempty"`
	ProcessQuotas     bool   `json:"processQuotas"`
	DesktopIsolation  bool   `json:"desktopIsolation"`
}

// 描述本项目的验证进度，不把某次能力自检成功等同于完成平台验收。
func validationStatus(platform, architecture string) string {
	if architecture != "amd64" && architecture != "arm64" {
		return "unsupported"
	}
	if platform == "windows" && architecture == "amd64" {
		return "partial"
	}
	if platform == "windows" || platform == "linux" {
		return "pending"
	}
	return "unsupported"
}

type Prepared struct {
	AgentID   string            `json:"agentId"`
	Paths     map[string]string `json:"paths"`
	Ephemeral bool              `json:"ephemeral"`
}

type Result struct {
	ExitCode   int   `json:"exitCode"`
	DurationMs int64 `json:"durationMs"`
}

func contained(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// 不做 chmod，不授予当前系统用户本来没有的权限。
// 目录祖先属于可信主程序；此检查不是对恶意宿主的竞态防御。
func validateDirectory(source string) error {
	if !filepath.IsAbs(source) || strings.ContainsRune(source, 0) || filepath.Dir(source) == source {
		return problem("INVALID_CONFIG", "挂载源必须是绝对目录，且不能是磁盘根目录")
	}
	for current := source; filepath.Dir(current) != current; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return problem("INVALID_CONFIG", "挂载目录不存在或无法访问")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return problem("UNSAFE_PATH", "挂载目录及祖先不能是符号链接")
		}
		if !info.IsDir() {
			return problem("INVALID_CONFIG", "挂载源及祖先必须是目录")
		}
	}
	return nil
}

func ValidateConfig(c Config) error {
	if !idPattern.MatchString(c.AgentID) {
		return problem("INVALID_CONFIG", "agentId 不合法")
	}
	if c.Network != "none" || c.Desktop {
		return problem("UNSUPPORTED_CAPABILITY", "只支持断网的非图形执行环境")
	}
	if len(c.Mounts) < 1 || len(c.Mounts) > 32 {
		return problem("INVALID_CONFIG", "挂载数量必须在 1 到 32 之间")
	}
	if c.Limits.TimeoutMs < 1 || c.Limits.TimeoutMs > 3600000 || c.Limits.MaxOutputBytes < 1024 || c.Limits.MaxOutputBytes > 16*1024*1024 {
		return problem("INVALID_CONFIG", "执行时间或输出限制不合法")
	}
	if (c.Limits.MemoryBytes != 0 && (c.Limits.MemoryBytes < 64*1024*1024 || c.Limits.MemoryBytes > 8*1024*1024*1024)) || c.Limits.MaxProcesses > 256 || c.Limits.CPUPercent > 100 {
		return problem("INVALID_CONFIG", "内存、进程数或 CPU 限制不合法")
	}
	names := map[string]bool{}
	workspace := false
	for i, m := range c.Mounts {
		key := strings.ToLower(m.Name)
		if !idPattern.MatchString(m.Name) || names[key] {
			return problem("INVALID_CONFIG", "挂载名不合法或重复")
		}
		names[key] = true
		if m.Access != "read-only" && m.Access != "read-write" {
			return problem("INVALID_CONFIG", "挂载权限不合法")
		}
		if m.Name == "workspace" {
			workspace = true
		}
		if filepath.Clean(m.Source) != m.Source {
			return problem("INVALID_CONFIG", "挂载源必须是规范路径")
		}
		if err := validateDirectory(m.Source); err != nil {
			return err
		}
		if m.Alias != "" {
			if runtime.GOOS != "windows" || !filepath.IsAbs(m.Alias) || filepath.Clean(m.Alias) != m.Alias {
				return problem("INVALID_CONFIG", "显示联接只支持 Windows 绝对规范路径")
			}
			parent := filepath.Dir(m.Alias)
			if err := validateDirectory(parent); err != nil {
				return err
			}
			if contained(parent, m.Source) || contained(m.Source, parent) {
				return problem("MOUNT_CONFLICT", "联接父目录与挂载源不能重叠")
			}
			_, err := os.Readlink(m.Alias)
			if err != nil {
				return problem("UNSAFE_PATH", "显示路径必须是由可信宿主创建的目录联接")
			}
			target, err := os.Stat(m.Alias)
			sourceInfo, sourceErr := os.Stat(m.Source)
			if err != nil || sourceErr != nil || !os.SameFile(target, sourceInfo) {
				return problem("UNSAFE_PATH", "显示联接必须指向当前挂载源")
			}
		}
		for _, previous := range c.Mounts[:i] {
			if contained(previous.Source, m.Source) || contained(m.Source, previous.Source) {
				return problem("MOUNT_CONFLICT", "挂载源不能重叠")
			}
		}
	}
	for _, m := range c.Mounts {
		if m.Alias == "" {
			continue
		}
		for _, other := range c.Mounts {
			if contained(filepath.Dir(m.Alias), other.Source) || contained(other.Source, filepath.Dir(m.Alias)) {
				return problem("MOUNT_CONFLICT", "显示联接父目录不能位于挂载源内")
			}
		}
	}
	if !workspace {
		return problem("INVALID_CONFIG", "缺少 workspace 挂载（可读写或只读）")
	}
	return nil
}

func relativeFile(name string) bool {
	if len(name) > 4096 || strings.ContainsAny(name, "\x00\\:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return false
		}
	}
	return true
}

func mountPath(c Config, name string) string {
	if runtime.GOOS == "windows" {
		for _, mount := range c.Mounts {
			if mount.Name == name {
				if mount.Alias != "" {
					return mount.Alias
				}
				return mount.Source
			}
		}
		return ""
	}
	return path.Join("/Agents", c.AgentID, name)
}

func Paths(c Config) Prepared {
	paths := map[string]string{}
	for _, m := range c.Mounts {
		paths[m.Name] = mountPath(c, m.Name)
	}
	return Prepared{c.AgentID, paths, true}
}

func resolveCwd(c Config, cwd string) (string, error) {
	if !relativeFile(cwd) {
		return "", problem("INVALID_PATH", "cwd 必须是以挂载名开头的相对路径")
	}
	parts := strings.SplitN(cwd, "/", 2)
	for _, m := range c.Mounts {
		if m.Name == parts[0] {
			if runtime.GOOS == "windows" {
				if len(parts) == 1 {
					return mountPath(c, m.Name), nil
				}
				return filepath.Join(mountPath(c, m.Name), filepath.FromSlash(parts[1])), nil
			}
			return path.Join("/Agents", c.AgentID, cwd), nil
		}
	}
	return "", problem("PERMISSION_DENIED", "cwd 不属于已授权挂载")
}

func ValidateCommand(c Config, command Command) error {
	if len(command.Argv) == 0 || len(command.Argv) > 256 || command.Argv[0] == "" {
		return problem("INVALID_COMMAND", "argv 必须包含要执行的程序")
	}
	bytes := 0
	for _, arg := range command.Argv {
		if strings.ContainsRune(arg, 0) {
			return problem("INVALID_COMMAND", "命令参数不能包含空字符")
		}
		bytes += len(arg)
	}
	if bytes > 128*1024 || len(command.Stdin) > 256*1024 || len(command.Env) > 64 {
		return problem("INVALID_COMMAND", "命令、输入或环境变量过大")
	}
	if command.TimeoutMs < 1 || command.TimeoutMs > c.Limits.TimeoutMs {
		return problem("INVALID_COMMAND", "命令不能提高沙箱超时限制")
	}
	seenEnv := map[string]bool{}
	for key, value := range command.Env {
		if !envPattern.MatchString(key) || strings.ContainsRune(value, 0) || len(value) > 32768 {
			return problem("INVALID_COMMAND", "环境变量不合法")
		}
		policyKey := key
		if runtime.GOOS == "windows" {
			policyKey = strings.ToUpper(key)
		}
		if seenEnv[policyKey] {
			return problem("INVALID_COMMAND", "环境变量存在大小写重复")
		}
		seenEnv[policyKey] = true
		if strings.HasPrefix(policyKey, "NXCONTAINER_") {
			return problem("INVALID_COMMAND", "不能覆盖运行时环境变量")
		}
		switch policyKey {
		case "HOME", "TMPDIR", "WORKSPACE", "USER", "LOGNAME", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP":
			return problem("INVALID_COMMAND", fmt.Sprintf("不能覆盖 %s", key))
		}
	}
	_, err := resolveCwd(c, command.Cwd)
	return err
}
