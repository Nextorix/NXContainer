/*
 * NXContainer Execution Engine
 *
 * 管理隔离任务、输出预算、执行超时与取消生命周期。
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
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

func Prepare(ctx context.Context, c Config) (Prepared, error) {
	if err := ValidateConfig(c); err != nil {
		return Prepared{}, err
	}
	if err := validatePlatform(c); err != nil {
		return Prepared{}, err
	}
	capabilities := Probe(ctx)
	if !capabilities.Exec {
		return Prepared{}, problem("BACKEND_UNAVAILABLE", capabilities.Reason)
	}
	if !c.Experimental {
		return Prepared{}, problem("EXPERIMENTAL_REQUIRED", "此平台后端尚未完成安全验收，需要管理方显式开启实验选项")
	}
	return Paths(c), nil
}

// 两条输出流共用预算；超限时终止整个隔离环境而不是无限丢弃输出。
type outputBudget struct {
	mu        sync.Mutex
	remaining int
	exceeded  bool
	cancel    context.CancelFunc
	emit      func(string, []byte) error
}

type streamWriter struct {
	budget *outputBudget
	name   string
}

func (w streamWriter) Write(p []byte) (int, error) {
	b := w.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	size := len(p)
	if b.exceeded {
		return size, nil
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
		b.exceeded = true
		b.cancel()
	}
	b.remaining -= len(p)
	for len(p) > 0 {
		n := min(len(p), 16*1024)
		if err := b.emit(w.name, p[:n]); err != nil {
			b.cancel()
			return 0, err
		}
		p = p[n:]
	}
	return size, nil
}

func Run(ctx context.Context, request Request, emit func(string, []byte) error) (Result, error) {
	c := request.Sandbox
	if err := ValidateConfig(c); err != nil {
		return Result{}, err
	}
	if !c.Experimental {
		return Result{}, problem("EXPERIMENTAL_REQUIRED", "实验后端未获管理方授权")
	}
	command := request.Command
	outputLimit := c.Limits.MaxOutputBytes
	if request.Operation == "file" {
		worker, err := prepareFile(c, request.File)
		if err != nil {
			return Result{}, err
		}
		data, _ := json.Marshal(worker)
		command = Command{Argv: []string{"/nx/worker", "--file-worker"}, Cwd: "workspace", Stdin: string(data), TimeoutMs: c.Limits.TimeoutMs}
		outputLimit = 2 * MaxFileBytes
	} else if err := ValidateCommand(c, command); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(command.TimeoutMs)*time.Millisecond)
	defer cancel()
	budget := &outputBudget{remaining: outputLimit, cancel: cancel, emit: emit}
	start := time.Now()
	exitCode, err := runPlatform(ctx, c, command, streamWriter{budget, "stdout"}, streamWriter{budget, "stderr"})
	result := Result{ExitCode: exitCode, DurationMs: time.Since(start).Milliseconds()}
	var structured *Error
	errors.As(err, &structured)
	if structured != nil && structured.Code == "CLEANUP_FAILED" {
		return result, structured
	}
	if budget.exceeded {
		return result, problem("OUTPUT_LIMIT", "输出超过限制，进程树已停止")
	}
	if ctx.Err() == context.DeadlineExceeded {
		return result, problem("TIMEOUT", "执行超时，进程树已停止")
	}
	if ctx.Err() != nil {
		return result, problem("ABORTED", "操作已取消")
	}
	if err != nil {
		if structured != nil {
			return result, structured
		}
		return result, problem("BACKEND_FAILED", "无法启动隔离进程："+err.Error())
	}
	return result, nil
}
