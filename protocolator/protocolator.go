package protocolator

import (
	"time"
	"fmt"
	"gopkg.in/bufio.v1"
	"os"
	"strings"
)

type Protocolator struct {
	path      string
	incoming  chan message
	bufwriter *bufio.Writer
}

type message struct {
	time time.Time
	msg  string
}

func New(path string) (*Protocolator, error) {
	f, err := os.OpenFile(path, os.O_CREATE | os.O_APPEND | os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	p := &Protocolator{
		path: path,
		incoming: make(chan message, 10),
		bufwriter: bufio.NewWriter(f),
	}

	go p.run()

	return p, nil
}

func (p *Protocolator) run() {
	flusher := time.After(5 * time.Minute)
	for {
		select {
		case msg := <-p.incoming:
			t, m := msg.time.UTC(), msg.msg
			if strings.HasSuffix(m, "\n") {
				p.bufwriter.WriteString(fmt.Sprint(t, m))
			} else {
				p.bufwriter.WriteString(fmt.Sprintln(t, m))
			}
		case <-flusher:
			p.bufwriter.Flush()
			flusher = time.After(time.Millisecond)
		}
	}
}

func (p *Protocolator) Log(s ...string) {
	p.incoming <- message{
		time: time.Now(),
		msg: strings.Join(s, " "),
	}
}
