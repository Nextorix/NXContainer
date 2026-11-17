/*
 * NXContainer File Worker
 *
 * 在沙箱内执行受挂载根约束的文件读取、写入与目录操作。
 *
 * Author:
 * MoyuZJ <moyuzj@nextorix.dev> @Nextorix
 * Made in China with ♥
 *
 * Copyright (C) 2026 Nextorix LLC.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package sandbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"sort"
)

type WorkerRequest struct {
	Root string      `json:"root"`
	File FileRequest `json:"file"`
}

type FileEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink"`
	Size      int64  `json:"size"`
}

type FileResult struct {
	Data    string      `json:"data"`
	Entries []FileEntry `json:"entries"`
}

type FileResponse struct {
	Result *FileResult `json:"result,omitempty"`
	Error  *Error      `json:"error,omitempty"`
}

func validateFile(file FileRequest) error {
	if !relativeFile(file.Path) {
		return problem("INVALID_PATH", "文件路径不能越界")
	}
	switch file.Operation {
	case "list", "read", "write", "mkdir", "remove":
	default:
		return problem("INVALID_CONFIG", "未知文件操作")
	}
	if file.Operation != "list" && (file.Path == "" || path.Clean(file.Path) == ".") {
		return problem("INVALID_PATH", "不能操作挂载根目录本身")
	}
	if file.Operation == "write" {
		data, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil || len(data) > MaxFileBytes {
			return problem("FILE_TOO_LARGE", "内容不是合法 Base64 或超过 1 MiB")
		}
	} else if file.Data != "" {
		return problem("INVALID_CONFIG", "此文件操作不接受内容")
	}
	return nil
}

func prepareFile(c Config, file FileRequest) (WorkerRequest, error) {
	if err := validateFile(file); err != nil {
		return WorkerRequest{}, err
	}
	for _, mount := range c.Mounts {
		if mount.Name != file.Mount {
			continue
		}
		if (file.Operation == "write" || file.Operation == "remove" || file.Operation == "mkdir") && mount.Access != "read-write" {
			return WorkerRequest{}, problem("PERMISSION_DENIED", "该挂载只读")
		}
		return WorkerRequest{Root: mountPath(c, mount.Name), File: file}, nil
	}
	return WorkerRequest{}, problem("PERMISSION_DENIED", "未授权的挂载")
}

// 只能由已进入隔离环境的执行器调用；所有文件读写都发生在沙箱内部。
// os.Root 对相对链接执行根目录约束，避免先 realpath 再打开的时间差。
func performFile(request WorkerRequest) (FileResult, error) {
	file := request.File
	if err := validateFile(file); err != nil {
		return FileResult{}, err
	}
	root, err := os.OpenRoot(request.Root)
	if err != nil {
		return FileResult{}, err
	}
	defer root.Close()
	name := file.Path
	if name == "" {
		name = "."
	}
	switch file.Operation {
	case "read":
		f, err := root.Open(name)
		if err != nil {
			return FileResult{}, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return FileResult{}, err
		}
		if !info.Mode().IsRegular() {
			return FileResult{}, problem("INVALID_PATH", "只能读取普通文件")
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		if err != nil {
			return FileResult{}, err
		}
		if len(data) > MaxFileBytes {
			return FileResult{}, problem("FILE_TOO_LARGE", "文件接口单次最多读取 1 MiB")
		}
		return FileResult{Data: base64.StdEncoding.EncodeToString(data)}, nil
	case "write":
		data, _ := base64.StdEncoding.DecodeString(file.Data)
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return FileResult{}, err
		}
		temporary := path.Join(path.Dir(name), ".nx-"+hex.EncodeToString(random[:]))
		f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return FileResult{}, err
		}
		defer root.Remove(temporary)
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return FileResult{}, writeErr
		}
		if closeErr != nil {
			return FileResult{}, closeErr
		}
		// 原子替换不会沿目标文件的符号链接或硬链接写入其他位置。
		return FileResult{}, root.Rename(temporary, name)
	case "mkdir":
		return FileResult{}, root.MkdirAll(name, 0700)
	case "remove":
		// 不支持递归删除；只能删除单个文件、链接或空目录。
		return FileResult{}, root.Remove(name)
	case "list":
		f, err := root.Open(name)
		if err != nil {
			return FileResult{}, err
		}
		defer f.Close()
		entries, err := f.ReadDir(1001)
		if err != nil && err != io.EOF {
			return FileResult{}, err
		}
		if len(entries) > 1000 {
			return FileResult{}, problem("FILE_TOO_LARGE", "目录超过 1000 项，请使用沙箱命令处理")
		}
		result := FileResult{Entries: []FileEntry{}}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return FileResult{}, err
			}
			result.Entries = append(result.Entries, FileEntry{entry.Name(), entry.IsDir(), entry.Type()&os.ModeSymlink != 0, info.Size()})
		}
		sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Name < result.Entries[j].Name })
		return result, nil
	}
	return FileResult{}, problem("INVALID_CONFIG", "未知文件操作")
}

func FileWorker(input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(io.LimitReader(input, MaxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request WorkerRequest
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return problem("INVALID_CONFIG", "文件请求包含多余内容")
	}
	result, err := performFile(request)
	response := FileResponse{Result: &result}
	if err != nil {
		response.Result = nil
		if typed, ok := err.(*Error); ok {
			response.Error = typed
		} else {
			response.Error = problem("FILE_OPERATION_FAILED", err.Error())
		}
	}
	return json.NewEncoder(output).Encode(response)
}
