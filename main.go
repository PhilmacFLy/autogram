package main

import (
	"autogram-next/set"
	"cacher"
	"crypto/tls"
	"encoding/json"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/gorilla/mux"
	"github.com/op/go-logging"
	"github.com/quiteawful/qairc"
	"github.com/urfave/cli"
	"io/ioutil"
	"net/http"
	"os"
	"time"
)

var (
	telegram    *tgbotapi.BotAPI
	irc         *qairc.Engine
	confpath    string
	config      Settings
	subscribers set.I64
	log         logging.Logger
)

type Settings struct {
	ApiKey           string
	IrcServer        string
	IrcTLS           bool
	IrcChannel       string
	IrcNickname      string
	IrcRealname      string
	HttpServerString string
	HttpListen       string
	Subscribers      []int64
}

func (s *Settings) GetFromFile(path string) {
	confb, err := ioutil.ReadFile(path)
	panicOnError(err)
	err = json.Unmarshal(confb, &s)
	panicOnError(err)
}

func (s *Settings) SaveToJSONFile(path string) {
	confb, err := json.Marshal(s)
	panicOnError(err)
	err = ioutil.WriteFile(path, confb, os.FileMode(0600))
}

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

func downloadFile(url string) ([]byte, error) {
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

func getFileByID(id string) (*File, error) {
	url, err := telegram.GetFileDirectURL(id)
	if err != nil {
		return nil, err
	}
	content, err := downloadFile(url)
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
	var file *File
	var err error
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
	panicOnError(err)
	return file.id, true
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
		edt = " (*edit*)"

	}

	if update.Message != nil && (msg.Text == "/start" || msg.Text == "/stop") {
		switch {
		case msg.Text == "/start":
			subscribers.Put(int64(msg.Chat.ID))
		case msg.Text == "/stop":
			subscribers.Remove(int64(msg.Chat.ID))
		}
		config.Subscribers = subscribers.Get()
		config.SaveToJSONFile(confpath)
		return
	}

	id, ok := processResource(msg)

	if ok {
		irc.Privmsg(config.IrcChannel, msg.From.UserName+": "+edt+config.HttpServerString+id)
	} else {
		irc.Privmsg(config.IrcChannel, msg.From.UserName+": "+edt+msg.Text)
	}

	for _, subid := range subscribers.Get() {
		if subid != int64(msg.From.ID) {
			_, err := telegram.Send(
				tgbotapi.NewForward(
					subid,
					int64(msg.Chat.ID),
					msg.MessageID,
				),
			)
			if err != nil {
				reflectid := int64(msg.Chat.ID)
				telegram.Send(
					tgbotapi.NewMessage(
						int64(reflectid),
						"Error: "+err.Error(),
					),
				)
			}
		}
	}
}

func panicOnError(err error) {
	if err != nil {
		panic(err)
	}
}

func tgannounce(msg string) {
	for _, subid := range subscribers.Get() {
		telegram.Send(tgbotapi.NewMessage(subid, msg))
	}
}

func bot(c *cli.Context) error {
	c
	config.GetFromFile(confpath)
	subscribers = set.I64{}
	for _, sub := range config.Subscribers {
		subscribers.Put(sub)
	}

	var err error
	telegram, err = tgbotapi.NewBotAPI(config.ApiKey)
	panicOnError(err)

	updatecfg := tgbotapi.UpdateConfig{}
	updatecfg.Timeout = 60
	tgch, err := telegram.GetUpdatesChan(updatecfg)
	panicOnError(err)

	irc = qairc.QAIrc(config.IrcNickname, config.IrcRealname)
	irc.Address = config.IrcServer
	irc.UseTLS = config.IrcTLS
	irc.TLSCfg = &tls.Config{InsecureSkipVerify: true}
	err = irc.Run()
	panicOnError(err)

	cache := cacher.New(100*1024*1024, func(id string) (cacher.Entry, bool) {
		log.Info("Cache miss:", "item id", id)
		npic, err := getFileByID(id)
		if err != nil {
			log.Warning("Cache miss:", "item id", id, "backend retrieval unsuccessful")
			data, _ := ioutil.ReadFile("giphy.gif")
			return File{id: id, size: len(data), data: data}, false
		}
		log.Info("Cache miss:", "item id", id, "backend retrieval successful")
		return *npic, true
	})

	r := mux.NewRouter()
	r.HandleFunc("/autogramimg/{id}", func(rw http.ResponseWriter, rq *http.Request) {

		log.Info("Http request:", rq.RemoteAddr, rq.RequestURI)
		vars := mux.Vars(rq)

		if vars["id"] == "favicon.ico" {
			return
		}
		log.Info("Cache request:", "item id", vars["id"])
		cacherq := cacher.EntryRq{
			vars["id"],
			make(chan cacher.Entry),
		}
		cache.RqCh <- cacherq
		rw.Write((<-cacherq.Response).(File).data)
	})

	go func() {
		err = http.ListenAndServe(config.HttpListen, r)
		if err != nil {
			log.Error("Http error:", err.Error())
		}
	}()

	tgannounce("PSA: " + time.Now().String())
	tgannounce("PSA: Autogram aktiv!")

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
				for _, subid := range subscribers.Get() {
					msgtext, _ := msg.GetPrivmsg()
					_, err := telegram.Send(
						tgbotapi.NewMessage(int64(subid), msg.Sender.Nick+": "+msgtext),
					)
					if err != nil {
						irc.Privmsg(config.IrcChannel, "Error: "+err.Error())
					}
				}
			}

		case tgin := <-tgch:
			processTGMsg(tgin)
		}
	}
}

func main() {
	format := logging.MustStringFormatter(
		`%{color}%{time:15:04:05.000} %{shortfunc} %{level:.4s} %{id:05x}%{color:reset} %{message}`,
	)
	logging.SetFormatter(format)
	log := logging.MustGetLogger("log")
	log.Info("*** Autogram Release 8 ***")
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
