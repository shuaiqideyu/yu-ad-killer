package service

import (
	"reflect"
	"testing"

	"github.com/mymmrac/telego"
)

func TestMergeChatIDs(t *testing.T) {
	got := mergeChatIDs(
		-1002,
		[]int64{-1001, -1002, 0},
		[]int64{-1002, -1003, -1001, -1004},
	)
	want := []int64{-1001, -1003, -1004}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeChatIDs() = %v, 期望 %v", got, want)
	}
}

func TestOpenChatPermissionsAllowsMessaging(t *testing.T) {
	p := OpenChatPermissions()
	assertTrue := func(name string, v *bool) {
		t.Helper()
		if v == nil || !*v {
			t.Fatalf("%s 应为 true", name)
		}
	}
	assertTrue("CanSendMessages", p.CanSendMessages)
	assertTrue("CanSendOtherMessages", p.CanSendOtherMessages)
	_ = telego.ChatPermissions{} // 锁定依赖 telego 权限结构
}
