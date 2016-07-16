package main

import (
	telegram "github.com/go-telegram-bot-api/telegram-bot-api"
	irc "github.com/quiteawful/qairc"
	"fmt"
	"crypto/tls"
	"net/http"
	"github.com/gorilla/mux"
	"io/ioutil"
	"github.com/urfave/cli"
	"encoding/json"
	"os"
	"autogram-next/set"
	"autogram-next/cacher"
)

var confpath string
var config Settings
var subscribers set.I64

type Settings struct {
	ApiKey           string
	IrcServer        string
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

func getPhoto(tg telegram.BotAPI, photos []telegram.PhotoSize) (*File, error) {
	maxphoto := photos[0]
	for _, photo := range photos {
		if maxphoto.FileSize < photo.FileSize {
			maxphoto = photo
		}
	}
	return getFileByID(tg, maxphoto.FileID)
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

func getFileByID(tg telegram.BotAPI, id string) (*File, error) {
	url, err := tg.GetFileDirectURL(id)
	if err != nil {
		return nil, err
	}
	content, err := downloadFile(url)
	if err != nil {
		return nil, err
	}
	return &File{id, len(content), content}, nil
}

func processResource(tg telegram.BotAPI, inch chan cacher.Entry, update telegram.Update) (string, bool) {
	var file *File
	var err error
	switch {
	case update.Message.Photo != nil:
		file, err = getPhoto(tg, *update.Message.Photo)
	case update.Message.Video != nil:
		file, err = getFileByID(tg, (*update.Message.Video).FileID)
	case update.Message.Sticker != nil:
		file, err = getFileByID(tg, (*update.Message.Sticker).FileID)
	case update.Message.Document != nil:
		file, err = getFileByID(tg, (*update.Message.Document).FileID)
	default:
		return "", false
	}
	panicOnError(err)
	inch <- cacher.Entry{
		ID: file.id,
		Load: *file,
	}
	return file.id, true
}

func ProcessIRCMsg(tg *telegram.BotAPI, irc *irc.Engine, msg irc.Message) {
	switch {
	case msg.Type == "001":
		irc.Join(config.IrcChannel)
	case msg.IsPrivmsg() && msg.GetChannel() == config.IrcChannel:
		for _, subid := range subscribers.Get() {
			msgtext, _ := msg.GetPrivmsg()
			_, err := tg.Send(
				telegram.NewMessage(int64(subid), msg.Sender.Nick + ": " + msgtext),
			)
			if err != nil {
				irc.Privmsg(config.IrcChannel, "Error: " + err.Error())
			}
		}
	}
}

func ProcessTGMsg(tg *telegram.BotAPI, irc *irc.Engine, update telegram.Update, inch chan cacher.Entry) {
	msg := update.Message
	switch {
	case msg.Text == "/start":
		switch {
		case msg.Chat.IsPrivate():
			subscribers.Put(int64(msg.From.ID))
		case msg.Chat.IsGroup():
			subscribers.Put(int64(msg.Chat.ID))
		}
		config.Subscribers = subscribers.Get()
		config.SaveToJSONFile(confpath)
		return
	case msg.Text == "/stop":
		switch {
		case msg.Chat.IsPrivate():
			subscribers.Remove(int64(msg.From.ID))
		case msg.Chat.IsGroup():
			subscribers.Remove(int64(msg.Chat.ID))
		}
		config.Subscribers = subscribers.Get()
		config.SaveToJSONFile(confpath)
		return
	}

	id, ok := processResource(*tg, inch, update)
	if ok {
		irc.Privmsg(config.IrcChannel, msg.From.UserName + ": " + config.HttpServerString + id)
	} else {
		irc.Privmsg(config.IrcChannel, msg.From.UserName + ": " + msg.Text)
	}

	for _, subid := range subscribers.Get() {
		if subid != int64(msg.From.ID) && subid != int64(msg.Chat.ID) {
			_, err := tg.Send(
				telegram.NewForward(
					subid,
					int64(msg.From.ID),
					msg.MessageID,
				),
			)
			if err != nil {
				reflectid := int64(msg.From.ID)
				if msg.Chat.IsGroup() {
					reflectid = int64(msg.Chat.ID)
				}
				tg.Send(
					telegram.NewMessage(
						int64(reflectid),
						"Error: " + err.Error(),
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

func main() {
	fmt.Println("Running...")
	fmt.Println("R04")

	app := cli.NewApp()
	app.Name = "Autogram"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "config, c",
			Usage:       "Work with configuration from `FILE`",
			Destination: &confpath,
		},
	}

	app.Action = func(c *cli.Context) error {
		config.GetFromFile(confpath)
		subscribers = set.I64{}
		for _, sub := range config.Subscribers {
			subscribers.Put(sub)
		}

		bot, err := telegram.NewBotAPI(config.ApiKey)
		panicOnError(err)
		bot.Debug = true

		updatecfg := telegram.UpdateConfig{}
		updatecfg.Timeout = 60
		tgch, err := bot.GetUpdatesChan(updatecfg)
		panicOnError(err)

		irc := irc.QAIrc(config.IrcNickname, config.IrcRealname)
		irc.Address = config.IrcServer
		irc.UseTLS = true
		irc.TLSCfg = &tls.Config{InsecureSkipVerify: true}

		err = irc.Run()
		panicOnError(err)

		cache := cacher.New(func(id string) cacher.Entry {
			npic, err := getFileByID(*bot, id)
			panicOnError(err)
			return cacher.Entry{id, *npic}
		})

		r := mux.NewRouter()
		r.HandleFunc("/autogramimg/{id}", func(rw http.ResponseWriter, rq *http.Request) {
			vars := mux.Vars(rq)

			if vars["id"] == "favicon.ico" {
				return
			}
			cacherq := cacher.EntryRq{
				vars["id"],
				make(chan cacher.Entry),
			}
			cache.RqCh <- cacherq
			rw.Write((<-cacherq.Response).Load.(File).data)
		})

		go func() {
			err = http.ListenAndServe(config.HttpListen, r)
			if err != nil {
				fmt.Println(err.Error())
			}
		}()

		for {
			select {
			case msg, state := <-irc.Out:
				if !state {
					fmt.Println("Reconnect")
					irc.Reconnect()
				}
				ProcessIRCMsg(bot, irc, msg)

			case tgin := <-tgch:
				ProcessTGMsg(bot, irc, tgin, cache.AddCh)
			}
		}
	}
	app.Run(os.Args)
}
