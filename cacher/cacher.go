package cacher

type Cacher struct {
	cache      map[string]Entry
	RqCh       chan EntryRq
	AddCh      chan Entry
	CacheMissF func(string) Entry
}

type Entry struct {
	ID   string
	Load interface{}
}

type EntryRq struct {
	ID       string
	Response chan Entry
}

func New(f func(string) Entry) Cacher {
	cacher := Cacher{
		cache: make(map[string]Entry),
		RqCh: make(chan EntryRq),
		AddCh: make(chan Entry),
		CacheMissF: f,
	}
	go func() {
		for {
			select {
			case entry := <-cacher.AddCh:
				cacher.cache[entry.ID] = entry
			case rq := <-cacher.RqCh:
				resp, ok := cacher.cache[rq.ID]
				if ok {
					rq.Response <- resp
				} else {
					nentry := cacher.CacheMissF(rq.ID)
					cacher.cache[nentry.ID] = nentry
					rq.Response <- nentry
				}
			}
		}
	}()
	return cacher
}
