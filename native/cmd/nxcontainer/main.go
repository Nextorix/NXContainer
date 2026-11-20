/*
 * NXContainer Native Entry Point
 *
 * 处理可信父进程的管理请求、控制管道与文件执行器入口。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// NXContainer 执行器：只接受来自可信父进程的一次管理请求。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"nxcontainer/internal/sandbox"
)

type event struct {
	Version int            `json:"version"`
	Type    string         `json:"type"`
	Data    string         `json:"data,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *sandbox.Error `json:"error,omitempty"`
}

func readRequest(reader *bufio.Reader) (sandbox.Request, error) {
	var input []byte
	for {
		line, prefix, err := reader.ReadLine()
		if err != nil {
			return sandbox.Request{}, err
		}
		input = append(input, line...)
		if len(input) > sandbox.MaxRequestBytes {
			return sandbox.Request{}, fmt.Errorf("请求超过大小限制")
		}
		if !prefix {
			break
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var request sandbox.Request
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return request, fmt.Errorf("请求包含多余内容")
	}
	if request.Version != sandbox.ProtocolVersion {
		return request, fmt.Errorf("不支持的协议版本")
	}
	return request, nil
}

func main() {
	// 联网沙箱内的网络中继：其后的参数是要执行的用户命令。
	if len(os.Args) > 2 && os.Args[1] == "--net-relay" {
		os.Exit(sandbox.NetRelay(os.Args[2:]))
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法：nxcontainer --capabilities | --stdio")
		os.Exit(2)
	}
	if os.Args[1] == "--file-worker" {
		if err := sandbox.FileWorker(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "文件请求失败：", err)
			os.Exit(2)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Args[1] == "--capabilities" {
		_ = json.NewEncoder(os.Stdout).Encode(sandbox.Probe(ctx))
		return
	}
	if os.Args[1] != "--stdio" {
		fmt.Fprintln(os.Stderr, "未知启动参数")
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	var mu sync.Mutex
	emit := func(e event) error {
		mu.Lock()
		defer mu.Unlock()
		e.Version = sandbox.ProtocolVersion
		return encoder.Encode(e)
	}
	failure := func(err error) {
		var typed *sandbox.Error
		if !errors.As(err, &typed) {
			typed = &sandbox.Error{Code: "INVALID_REQUEST", Message: err.Error()}
		}
		_ = emit(event{Type: "error", Error: typed})
	}
	reader := bufio.NewReader(os.Stdin)
	request, err := readRequest(reader)
	if err != nil {
		failure(err)
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 父进程必须保持控制管道打开。EOF（包括父进程崩溃）或第二个请求均撤销本次执行。
	go func() { _, _ = reader.ReadByte(); cancel() }()
	var result any
	switch request.Operation {
	case "capabilities":
		result = sandbox.Probe(ctx)
	case "prepare":
		result, err = sandbox.Prepare(ctx, request.Sandbox)
	case "exec", "file":
		result, err = sandbox.Run(ctx, request, func(stream string, data []byte) error {
			return emit(event{Type: stream, Data: base64.StdEncoding.EncodeToString(data)})
		})
	default:
		err = &sandbox.Error{Code: "INVALID_REQUEST", Message: "未知操作"}
	}
	if err != nil {
		failure(err)
		return
	}
	_ = emit(event{Type: "result", Result: result})
}
