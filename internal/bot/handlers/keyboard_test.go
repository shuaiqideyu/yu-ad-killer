package handlers

import (
	"testing"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

func TestClampPage(t *testing.T) {
	cases := []struct {
		name      string
		total     int
		page      int
		pageSize  int
		wantPage  int
		wantTotal int
	}{
		{"空列表", 0, 1, 12, 1, 1},
		{"页内", 30, 2, 12, 2, 3},
		{"超过末页", 30, 99, 12, 3, 3},
		{"小于 1", 30, 0, 12, 1, 3},
		{"正好整除", 24, 2, 12, 2, 2},
		{"剩 1 个一页", 13, 2, 12, 2, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPage, gotTotal := clampPage(c.total, c.page, c.pageSize)
			if gotPage != c.wantPage || gotTotal != c.wantTotal {
				t.Fatalf("clampPage(%d,%d,%d) = (%d,%d), 期望 (%d,%d)",
					c.total, c.page, c.pageSize, gotPage, gotTotal, c.wantPage, c.wantTotal)
			}
		})
	}
}

func TestChunkButtonsToRows(t *testing.T) {
	mk := func(n int) []telego.InlineKeyboardButton {
		out := make([]telego.InlineKeyboardButton, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, tu.InlineKeyboardButton("x").WithCallbackData("noop"))
		}
		return out
	}
	cases := []struct {
		name     string
		n, cols  int
		wantRows int
	}{
		{"空", 0, 2, 0},
		{"满 2 列", 4, 2, 2},
		{"不足填满末行", 5, 2, 3},
		{"单列", 3, 1, 3},
		{"cols 非法", 3, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := chunkButtonsToRows(mk(c.n), c.cols)
			if len(rows) != c.wantRows {
				t.Fatalf("rows=%d, 期望 %d", len(rows), c.wantRows)
			}
		})
	}
}

func TestPageNavRow(t *testing.T) {
	if got := pageNavRow(1, 1, "x"); got != nil {
		t.Fatalf("totalPages=1 应不显示导航行,得到 %v", got)
	}
	if got := pageNavRow(1, 3, "x"); len(got) != 2 {
		// 第 1 页: 没有"上一页",只有 [N/M] [下一页]
		t.Fatalf("第 1/3 页应有 2 个按钮,得到 %d", len(got))
	}
	if got := pageNavRow(2, 3, "x"); len(got) != 3 {
		t.Fatalf("中间页应有 3 个按钮,得到 %d", len(got))
	}
	if got := pageNavRow(3, 3, "x"); len(got) != 2 {
		t.Fatalf("末页应有 2 个按钮(无'下一页'),得到 %d", len(got))
	}
}
