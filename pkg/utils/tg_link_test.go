package utils

import "testing"

func TestExtractTGChatLinks(t *testing.T) {
	text := "进群 https://t.me/+BUsIgTIVWgRiZWJl 和 t.me/joinchat/BUsIgTIVWgRiZWJl " +
		"以及 tg://join?invite=BUsIgTIVWgRiZWJl " +
		"公开 https://t.me/SomeGroup/123 " +
		"预览 https://t.me/s/NewsChannel " +
		"分享 https://t.me/share/url?url=1 " +
		"私链 https://t.me/c/123/456 " +
		"零宽 https://t.me/\u200b+AbcdEfgh12345678。"
	got := ExtractTGChatLinks(text)
	want := []TGChatLink{
		{Kind: TGLinkInvite, Key: "BUsIgTIVWgRiZWJl"},
		{Kind: TGLinkUser, Key: "somegroup"},
		{Kind: TGLinkUser, Key: "newschannel"},
		{Kind: TGLinkInvite, Key: "AbcdEfgh12345678"},
	}
	if len(got) != len(want) {
		t.Fatalf("links = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("links[%d] = %#v, 期望 %#v", i, got[i], want[i])
		}
	}
}

func TestExtractTGChatLinksSkipsPlainText(t *testing.T) {
	if got := ExtractTGChatLinks("今天没有链接"); got != nil {
		t.Fatalf("got %#v", got)
	}
	if got := ExtractTGChatLinks("xxxt.me/notalink"); got != nil {
		t.Fatalf("got %#v", got)
	}
}
