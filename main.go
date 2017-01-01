package main

import (
	"autogram-next/set"
	"cacher"
	"crypto/tls"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/gorilla/mux"
	"github.com/op/go-logging"
	"github.com/quiteawful/qairc"
	"github.com/urfave/cli"
	"io/ioutil"
	"net/http"
	"os"
	"time"
	"strings"
	"autogram-next/misc"
)

var (
	telegram    *tgbotapi.BotAPI
	irc         *qairc.Engine
	confpath string
	config misc.Settings
	subscribers set.I64
	log logging.Logger
)

type File struct {
	id   string
	size int
	data []byte
}

func (f File) ID() string {
	return f.id
}

func (f File) Score() int {
	return len(f.data)
}

func getPhoto(photos []tgbotapi.PhotoSize) (*File, error) {
	maxphoto := photos[0]
	for _, photo := range photos {
		if maxphoto.FileSize < photo.FileSize {
			maxphoto = photo
		}
	}
	return getFileByID(maxphoto.FileID)
}

func getFileByID(id string) (*File, error) {
	url, err := telegram.GetFileDirectURL(id)
	if err != nil {
		return nil, err
	}
	content, err := misc.DownloadFile(url)
	if err != nil {
		return nil, err
	}
	return &File{
		id:   id,
		size: len(content),
		data: content,
	}, nil
}

func processResource(msg tgbotapi.Message) (string, bool) {
	var (
		file *File
		err error
	)
	switch {
	case msg.Photo != nil:
		file, err = getPhoto(*msg.Photo)
	case msg.Video != nil:
		file, err = getFileByID((msg.Video).FileID)
	case msg.Sticker != nil:
		file, err = getFileByID((msg.Sticker).FileID)
	case msg.Document != nil:
		file, err = getFileByID((msg.Document).FileID)
	default:
		return "", false
	}
	fatal(err)
	return file.id, true
}

func processTGCmdMsg(cmd tgbotapi.Message) {
	switch {
	case cmd.Text == "/start":
		log.Info("Subscription starting ", cmd.Chat.ID)
		subscribers.Put(int64(cmd.Chat.ID))
	case cmd.Text == "/stop":
		log.Info("Subscription ending ", cmd.Chat.ID)
		subscribers.Remove(int64(cmd.Chat.ID))
	}
	config.Subscribers = subscribers.Get()
	config.SaveToJSONFile(confpath)
}

func processTGMsg(update tgbotapi.Update) {
	var (
		msg tgbotapi.Message
		edt string
	)

	switch {
	case (update.Message) != nil:
		msg = *update.Message
		edt = ""
	case (update.EditedMessage) != nil:
		msg = *update.EditedMessage
		edt = "(*edit*) "
	}

	if update.Message != nil && strings.HasPrefix(msg.Text, "/") {
		processTGCmdMsg(*update.Message)
		return
	}

	id, ok := processResource(msg)
	if ok {
		irc.Privmsg(config.IrcChannel, msg.From.UserName + ": " + edt + config.HttpServerString + id)
	} else {

		for _, singlemsg := range strings.Split(msg.Text, "\n") {
			irc.Privmsg(config.IrcChannel, msg.From.UserName + ": " + edt + singlemsg)
		}
	}

	for _, subid := range subscribers.Filtered(func(id int64) bool {
		return id != int64(msg.From.ID) && id != int64(msg.Chat.ID)
	}) {
		_, err := telegram.Send(
			tgbotapi.NewForward(subid, int64(msg.Chat.ID), msg.MessageID),
		)
		warning(err, func() {
			reflectid := int64(msg.Chat.ID)
			telegram.Send(
				tgbotapi.NewMessage(int64(reflectid), "Error: " + err.Error()),
			)
		})
	}
}

func tgAnnounceLn(msgs ...string) {
	for _, subid := range subscribers.Get() {
		for _, msg := range msgs {
			telegram.Send(tgbotapi.NewMessage(subid, msg))
		}
	}
}

func bot(c *cli.Context) error {
	config.GetFromFile(confpath)
	subscribers = set.I64{}
	for _, sub := range config.Subscribers {
		subscribers.Put(sub)
	}

	var err error
	telegram, err = tgbotapi.NewBotAPI(config.ApiKey)
	fatal(err)

	updatecfg := tgbotapi.UpdateConfig{}
	updatecfg.Timeout = 60
	tgch, err := telegram.GetUpdatesChan(updatecfg)
	fatal(err)

	irc = qairc.QAIrc(config.IrcNickname, config.IrcRealname)
	irc.Address = config.IrcServer
	irc.UseTLS = config.IrcTLS
	irc.TLSCfg = &tls.Config{InsecureSkipVerify: true}
	err = irc.Run()
	fatal(err)

	cache := cacher.New(100 * 1024 * 1024, func(id string) (cacher.Entry, bool) {
		log.Info("Cache miss:", "item id", id)
		npic, err := getFileByID(id)
		if err != nil {
			log.Info("Cache miss:", "item id", id, "backend retrieval unsuccessful")
			data, _ := ioutil.ReadFile("giphy.gif")
			return File{id: id, size: len(data), data: data}, false
		}
		log.Info("Cache miss:", "item id", id, "backend retrieval successful")
		return *npic, true
	})
	cache.Run()

	r := mux.NewRouter()
	r.HandleFunc("/autogramimg/{id}", func(rw http.ResponseWriter, rq *http.Request) {
		log.Info("Http request:", rq.RemoteAddr, rq.RequestURI)
		vars := mux.Vars(rq)
		if vars["id"] == "favicon.ico" {
			return
		}
		log.Info("Cache request:", "item id", vars["id"])
		item := <-cache.Request(vars["id"])
		rw.Write(item.(File).data)
	})

	go func() {
		err = http.ListenAndServe(config.HttpListen, r)
		if err != nil {
			log.Error("Http error:", err.Error())
		}
	}()

	tgAnnounceLn("🤖 " + time.Now().String(), "🤖 Autogram aktiv!")

	for {
		select {
		case msg, state := <-irc.Out:
			if !state {
				log.Warning("IRC reconnect triggered")
				irc.Reconnect()
			}
			switch {
			case msg.Type == "001":
				log.Info("Joining " + config.IrcChannel + " on " + config.IrcServer)
				irc.Join(config.IrcChannel)
			case msg.IsPrivmsg() && msg.GetChannel() == config.IrcChannel:
				text, _ := msg.GetPrivmsg()
				for _, subid := range subscribers.Get() {
					_, err := telegram.Send(
						tgbotapi.NewMessage(int64(subid), msg.Sender.Nick + ": " + text),
					)
					warning(err, func() {
						irc.Privmsg(config.IrcChannel, "Error: " + err.Error())
					})
				}
			case msg.IsCTCP():
				ctcptext := strings.Trim(msg.Args[len(msg.Args) - 1], "\x01\r\n")
				if strings.HasPrefix(ctcptext, "ACTION") {
					for _, subid := range subscribers.Get() {
						_, err := telegram.Send(
							tgbotapi.NewMessage(int64(subid), "* " + msg.Sender.Nick + " " + ctcptext[7:]),
						)
						warning(err, func() {
							irc.Privmsg(config.IrcChannel, "Error: " + err.Error())
						})
					}
				}
			default:
				log.Info(msg.Raw)
			}
		case tgin := <-tgch:
			processTGMsg(tgin)
		}
	}
}

func fatal(err error) {
	if err != nil {
		log.Error(err.Error())
		panic(err)
	}
}

func warning(err error, f func()) {
	if err != nil {
		log.Warning(err.Error())
		f()
	}
}

func main() {
	format := logging.MustStringFormatter(
		`%{color}%{time:15:04:05.000} %{shortfunc} %{level:.4s} %{id:05x}%{color:reset} %{message}`,
	)
	logging.SetFormatter(format)
	log := logging.MustGetLogger("log")
	log.Info("*** Autogram Release 11 ***")
	log.Info("Running...")

	app := cli.NewApp()
	app.Name = "Autogram"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "config, c",
			Usage:       "Work with configuration from `FILE`",
			Destination: &confpath,
		},
	}
	app.Action = bot
	app.Run(os.Args)
}
