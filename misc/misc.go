package misc

import (
	"encoding/json"
	"os"
	"io/ioutil"
	"net/http"
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
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(confb, &s)
	if err != nil {
		panic(err)
	}
}

func (s *Settings) SaveToJSONFile(path string) {
	confb, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	err = ioutil.WriteFile(path, confb, os.FileMode(0600))
}

func NewFile(id string, data []byte) *File {
	return &File{
		id: id,
		data: data,
	}
}

type File struct {
	id   string
	data []byte
}

func (f File) ID() string {
	return f.id
}

func (f File) Score() int {
	return len(f.data)
}

func (f File) Data() []byte {
	return f.data
}

func DownloadFile(url string) ([]byte, error) {
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

