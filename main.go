package main

import (
	"crypto/tls"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/gorilla/mux"
	"github.com/op/go-logging"
	"github.com/philmacfly/autogram/cacher"
	"github.com/philmacfly/autogram/misc"
	"github.com/philmacfly/autogram/protocolator"
	"github.com/philmacfly/autogram/set"
	"github.com/philmacfly/autogram/telegram"
	"github.com/quiteawful/qairc"
	"github.com/urfave/cli"
)

var (
	tg          *tgbotapi.BotAPI
	helper      telegram.Helper
	irc         *qairc.Engine
	cache       cacher.Cacher
	confpath    string
	config      misc.Settings
	subscribers set.I64
	log         logging.Logger
	prot        *protocolator.Protocolator
)

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

func getUrlForID(id string) string {
	return config.HttpServerString + id
}

func processTGMsg(update tgbotapi.Update) {
	var (
		msg    tgbotapi.Message
		prefix string
	)

	if update.Message != nil && strings.HasPrefix(msg.Text, "/") {
		processTGCmdMsg(*update.Message)
		return
	}

	send := func(p string, m tgbotapi.Message) {
		if len(prefix) > 0 && !strings.HasSuffix(p, " ") {
			p = p + " "
		}
		id, ok := helper.ExtractResourceID(m)
		if ok {
			prot.Log("TG", m.From.UserName, ":", getUrlForID(id))
			irc.Privmsg(config.IrcChannel, p+m.From.UserName+": "+getUrlForID(id))
		} else {
			prot.Log("TG", m.From.UserName, ":", m.Text)
			for _, singlemsg := range strings.Split(m.Text, "\n") {
				irc.Privmsg(config.IrcChannel, p+m.From.UserName+": "+singlemsg)
			}
		}

		for _, subid := range subscribers.Filtered(func(id int64) bool {
			return id != int64(m.From.ID) && id != int64(m.Chat.ID)
		}) {
			_, err := tg.Send(
				tgbotapi.NewForward(subid, int64(m.Chat.ID), m.MessageID),
			)
			warning(err, func() {
				reflectid := int64(m.Chat.ID)
				tg.Send(
					tgbotapi.NewMessage(int64(reflectid), "Error: "+err.Error()),
				)
			})
		}
	}

	switch {
	case (update.Message) != nil:
		msg = *update.Message
		prefix = ""
	case (update.EditedMessage) != nil:
		msg = *update.EditedMessage
		prefix = "(*edit*) "
	}

	if msg.ReplyToMessage != nil {
		send("> ", *msg.ReplyToMessage)
	}

	send(prefix, msg)
}

func tgAnnounceLn(msgs ...string) {
	for _, subid := range subscribers.Get() {
		for _, msg := range msgs {
			msgconf := tgbotapi.NewMessage(subid, msg)
			msgconf.DisableNotification = true
			tg.Send(msgconf)
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
	tg, err = tgbotapi.NewBotAPI(config.ApiKey)
	fatal(err)

	updatecfg := tgbotapi.UpdateConfig{}
	updatecfg.Timeout = 60
	tgch, err := tg.GetUpdatesChan(updatecfg)
	fatal(err)

	helper = telegram.NewHelper(tg)

	irc = qairc.QAIrc(config.IrcNickname, config.IrcRealname)
	irc.Address = config.IrcServer
	irc.UseTLS = config.IrcTLS
	irc.TLSCfg = &tls.Config{InsecureSkipVerify: true}
	err = irc.Run()
	fatal(err)

	cache = cacher.New(100*1024*1024, func(id string) (cacher.Entry, bool) {
		log.Info("Cache miss:", "item id", id)
		npic, err := helper.DownloadFileByID(id)
		if err != nil {
			log.Info("Cache miss:", "item id", id, "backend retrieval unsuccessful")
			data, _ := ioutil.ReadFile("giphy.gif")
			return misc.NewFile(id, data), false
		}
		log.Info("Cache miss:", "item id", id, "backend retrieval successful")
		return *npic, true
	})
	cache.Run()

	prot, _ = protocolator.New("stat.log")

	r := mux.NewRouter()
	r.HandleFunc("/autogramimg/{id}", func(rw http.ResponseWriter, rq *http.Request) {
		log.Info("Http request:", rq.RemoteAddr, rq.RequestURI)
		vars := mux.Vars(rq)
		if vars["id"] == "favicon.ico" {
			return
		}
		log.Info("Cache request:", "item id", vars["id"])
		item := <-cache.Request(vars["id"])
		rw.Write(item.(misc.File).Data())
	})

	go func() {
		err = http.ListenAndServe(config.HttpListen, r)
		if err != nil {
			log.Error("Http error:", err.Error())
		}
	}()

	tgAnnounceLn("🤖 "+time.Now().String(), "🤖 Autogram aktiv!")
	tgAnnounceLn("🤖 " + "CALLS MAY BE RECORDED FOR TRAINING AND QUALITY PURPOSES")

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
				prot.Log("IRC", msg.Sender.Nick, ":", text)
				for _, subid := range subscribers.Get() {
					_, err := tg.Send(
						tgbotapi.NewMessage(int64(subid), msg.Sender.Nick+": "+text),
					)
					warning(err, func() {
						irc.Privmsg(config.IrcChannel, "Error: "+err.Error())
					})
				}
			case msg.Type == "JOIN":
				tgAnnounceLn("🤖 " + msg.Sender.Nick + " has joined")
			case msg.Type == "PART":
				tgAnnounceLn("🤖 " + msg.Sender.Nick + " has left")
			case msg.Type == "QUIT":
				tgAnnounceLn("🤖 " + msg.Sender.Nick + " has quit")
			case msg.IsCTCP():
				ctcptext := strings.Trim(msg.Args[len(msg.Args)-1], "\x01\r\n")
				if strings.HasPrefix(ctcptext, "ACTION") {
					for _, subid := range subscribers.Get() {
						_, err := tg.Send(
							tgbotapi.NewMessage(int64(subid), "* "+msg.Sender.Nick+" "+ctcptext[7:]),
						)
						warning(err, func() {
							irc.Privmsg(config.IrcChannel, "Error: "+err.Error())
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
	log.Info("*** Autogram Release 15 ***")
	log.Info("Running...")

	app := cli.NewApp()
	app.Name = "Autogram"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "config, c",
			Usage:       "Work with configuration from `FILE`",
			Destination: &confpath,
		},
	}
	app.Action = bot
	app.Run(os.Args)
}
