package handlers

import (
	"fmt"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// 列表面板统一参数;关键词类列表全部按此渲染
const (
	listPageSize = 12 // 每页按钮数,搭配 2 列 = 6 行,一屏内可见
	listCols     = 2  // 每行按钮列数
)

// clampPage 收敛 page 到 [1, totalPages] 区间,同时返回 totalPages
//
// total <= 0 时 totalPages 至少为 1
func clampPage(total, page, pageSize int) (curPage, totalPages int) {
	if pageSize <= 0 {
		pageSize = 1
	}
	if total <= 0 {
		return 1, 1
	}
	totalPages = (total + pageSize - 1) / pageSize
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	return page, totalPages
}

// chunkButtonsToRows 把按钮按 cols 列拆成多行;cols<=0 时返回 nil
func chunkButtonsToRows(buttons []telego.InlineKeyboardButton, cols int) [][]telego.InlineKeyboardButton {
	if len(buttons) == 0 || cols <= 0 {
		return nil
	}
	rows := make([][]telego.InlineKeyboardButton, 0, (len(buttons)+cols-1)/cols)
	for i := 0; i < len(buttons); i += cols {
		j := i + cols
		if j > len(buttons) {
			j = len(buttons)
		}
		rows = append(rows, buttons[i:j])
	}
	return rows
}

// pageNavRow 构造翻页导航行;只有一页时返回 nil
//
// callbackPrefix 例如 "kw_list_name" / "nlw"; 翻页 callback 形如 "<prefix>:page:<N>"
func pageNavRow(page, totalPages int, callbackPrefix string) []telego.InlineKeyboardButton {
	if totalPages <= 1 {
		return nil
	}
	row := make([]telego.InlineKeyboardButton, 0, 3)
	if page > 1 {
		row = append(row, tu.InlineKeyboardButton("⬅️ 上一页").WithCallbackData(fmt.Sprintf("%s:page:%d", callbackPrefix, page-1)))
	}
	row = append(row, tu.InlineKeyboardButton(fmt.Sprintf("📄 %d/%d", page, totalPages)).WithCallbackData("noop"))
	if page < totalPages {
		row = append(row, tu.InlineKeyboardButton("下一页 ➡️").WithCallbackData(fmt.Sprintf("%s:page:%d", callbackPrefix, page+1)))
	}
	return row
}

// closeRow 统一的"关闭"按钮行;点击由 close 回调删除消息
func closeRow() []telego.InlineKeyboardButton {
	return []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("✖️ 关闭").WithCallbackData("close"),
	}
}
