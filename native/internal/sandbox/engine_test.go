/*
 * NXContainer Execution Engine Tests
 *
 * 验证多条输出流共用预算、分块传输与超限取消。
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
	"context"
	"testing"
)

func TestOutputBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var size, chunks int
	budget := &outputBudget{remaining: 20000, cancel: cancel, emit: func(_ string, data []byte) error {
		if len(data) > 16*1024 {
			t.Fatal("单事件过大")
		}
		size += len(data)
		chunks++
		return nil
	}}
	out, errout := streamWriter{budget, "stdout"}, streamWriter{budget, "stderr"}
	if _, err := out.Write(make([]byte, 18000)); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("未超限时不应取消")
	}
	if _, err := errout.Write(make([]byte, 3000)); err != nil {
		t.Fatal(err)
	}
	if size != 20000 || chunks != 3 || !budget.exceeded || ctx.Err() == nil {
		t.Fatal("共享输出预算未生效")
	}
	_, _ = out.Write([]byte("后续输出必须丢弃"))
	if size != 20000 {
		t.Fatal("超限后仍在输出")
	}
}
