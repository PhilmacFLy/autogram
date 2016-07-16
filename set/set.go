package set

type I64 map[int64]bool

func (s *I64) Put(i int64) bool {
	_, found := (*s)[i]
	(*s)[i] = true
	return !found
}

func (s *I64) Contains(i int64) bool {
	_, found := (*s)[i]
	return found
}

func (s *I64) Remove(i int64) {
	delete(*s, i)
}

func (s *I64) Get() []int64 {
	keys := make([]int64, 0)
	for k := range *s {
		keys = append(keys, k)
	}
	return keys
}
