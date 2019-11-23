package cacher

import (
	"fmt"
	"time"
)

type Cacher struct {
	cache  map[string]Entry
	hits   map[string]int
	access map[string]int64
	weight int64
	limit  int64
	count  int64
	rqch   chan rq
	statch chan statrq
	missF  func(string) (Entry, bool)
	Debug  bool
}

type Stats struct {
	Weight int64
	Limit  int64
	Count  int64
}

type statrq struct {
	Response chan Stats
}

func (c *Cacher) Request(id string) chan Entry {
	ch := make(chan Entry)
	go func() {
		c.rqch <- rq{
			ID:       id,
			Response: ch,
		}
	}()
	return ch
}

func (c *Cacher) Stats() chan Stats {
	ch := make(chan Stats)
	c.statch <- statrq{ch}
	return ch
}

type Entry interface {
	ID() string
	Score() int
}

type rq struct {
	ID       string
	Response chan Entry
}

func cacheadd(c *Cacher, e Entry) {
	c.weight += int64(e.Score())
	c.cache[e.ID()] = e
	c.hits[e.ID()] = 1
	c.access[e.ID()] = time.Now().Unix()
	c.count += 1
}

func cachepurge(c *Cacher) {
	var (
		compare Entry
		target  Entry
	)

	for _, e := range c.cache {
		if compare == nil {
			compare = e
			break
		}
	}

	leasthits := c.hits[compare.ID()]
	oldest := time.Now().Unix()

	for k, _ := range c.cache {
		if c.hits[k] < leasthits {
			leasthits = c.hits[k]
		}
	}

	for k, _ := range c.cache {
		if c.hits[k] == leasthits && c.access[k] <= oldest {
			target = c.cache[k]
			oldest = c.access[k]
		}
	}

	c.weight -= int64(target.Score())
	delete(c.cache, target.ID())
	c.count -= 1
	if c.Debug {
		fmt.Print("DEL ", target.ID(), " ")
	}
}

func (c *Cacher) run(debug bool) {
	go func(debug bool) {
		for {
			select {
			case statrq := <-c.statch:
				statrq.Response <- Stats{
					Weight: c.weight,
					Limit:  c.limit,
					Count:  c.count,
				}
			case rq := <-c.rqch:
				if debug {
					fmt.Print(c.weight, "/", c.limit, ": ")
				}
				e, ok := c.cache[rq.ID]
				if ok {
					c.hits[rq.ID] += 1
					c.access[rq.ID] = time.Now().Unix()
					if debug {
						fmt.Print("GET ", rq.ID, " ")
					}
					rq.Response <- e

				} else {
					if debug {
						fmt.Print("MISS ", rq.ID, " ")
					}
					nentry, save := c.missF(rq.ID)
					if save {
						if c.weight >= c.limit {
							cachepurge(c)
						}
						if debug {
							fmt.Print("ADD ", rq.ID, " ")
						}
						cacheadd(c, nentry)
					}
					rq.Response <- nentry
				}
				if debug {
					fmt.Println()
				}
			}
		}
	}(debug)
}

func (c *Cacher) Run() {
	c.run(false)
}

func New(limit int64, f func(string) (Entry, bool)) Cacher {
	cacher := Cacher{
		cache:  make(map[string]Entry),
		hits:   make(map[string]int),
		access: make(map[string]int64),
		weight: 0,
		limit:  limit,
		rqch:   make(chan rq),
		missF:  f,
	}
	return cacher
}
