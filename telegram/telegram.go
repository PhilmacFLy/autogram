package telegram

import (
	"autogram-next/misc"
	"github.com/go-telegram-bot-api/telegram-bot-api"
)

type Helper struct {
	telegram *tgbotapi.BotAPI
}

func NewHelper(telegram *tgbotapi.BotAPI) Helper {
	return Helper{telegram: telegram}
}

func (h *Helper) DownloadFileByID(id string) (*misc.File, error) {
	url, err := h.telegram.GetFileDirectURL(id)
	if err != nil {
		return nil, err
	}
	content, err := misc.DownloadFile(url)
	if err != nil {
		return nil, err
	}
	return misc.NewFile(id, content), nil
}

func (h *Helper) ExtractResourceID(msg tgbotapi.Message) (string, bool) {
	var (
		file *misc.File
		err error
	)
	switch {
	case msg.Photo != nil:
		maxphoto := (*msg.Photo)[0]
		for _, photo := range (*msg.Photo) {
			if maxphoto.FileSize < photo.FileSize {
				maxphoto = photo
			}
		}
		file, err = h.DownloadFileByID(maxphoto.FileID)
	case msg.Video != nil:
		file, err = h.DownloadFileByID((msg.Video).FileID)
	case msg.Sticker != nil:
		file, err = h.DownloadFileByID((msg.Sticker).FileID)
	case msg.Document != nil:
		file, err = h.DownloadFileByID((msg.Document).FileID)
	default:
		return "", false
	}
	if err != nil {
		panic(err)
	}
	return file.ID(), true
}