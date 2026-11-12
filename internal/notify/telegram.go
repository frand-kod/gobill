package notify

import (
	"context"
	"net/url"
)

// Telegram sends text to telegram_target_id (sendTelegram).
func (n *Notifier) Telegram(ctx context.Context, text string) error {
	if n.get("telegram_bot") == "" || text == "" {
		return nil
	}
	return n.logged("telegram", n.get("telegram_target_id"), "", text, n.getURL(ctx, n.TelegramAPI+"/bot"+n.get("telegram_bot")+"/sendMessage?chat_id="+
		url.QueryEscape(n.get("telegram_target_id"))+"&text="+url.QueryEscape(text)))
}
