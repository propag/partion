package main

import (
	"net/http"
	"net/url"
	"github.com/propag/pariton"
	"log"
	"fmt"
	"flag"
	"os"
)

import (
	"errors"
	"regexp"
	"io/ioutil"
	"path"
)

var out string

var urlStr string

var N int

func init() {
	flag.StringVar(&name, "o", "", "out file")
	flag.StringVar(&urlStr, "url", "", "url to fetch")
	flag.IntVar(&N, "n", "the number of partitions")
}

// NewestRelease takes newest go source to you
func NewestRelease() (string, error) {
	resp, err := http.Get("https://golang.org/dl/")
	if err != nil {
		return "", err
	}

	rawdata, err := ioutil.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		return "", err
	}

	if err != nil {
		return "", err
	}

	re, err := regexp.Compile(`https://storage\.googleapis\.com/golang/[0-9a-zA-Z.]+`)
	if err != nil {
		return "", err
	}

	url := re.Find(rawdata)
	if len(url) == 0 {
		return "", errors.New("newest go source cannot be taken")
	}

	return string(url), nil
}

//	go1.4.2.src.tar.gz

const CWide = 80

type MyRequestTripper struct {
	*partion.RequestTripper206N

	urlStr string
	line [CWide - 1]byte
	nsets int

	accumulated int64
}

type Screen struct {
	url  string
	line [Wide - 1]byte
	nset int
	reqs int
	errs []*Err

	perbytes int64

	newbytes int64

	bytes int64

	timer time.Duration

	persec int64
}

func NewScreen() *Screen {
	screen := new(Screen)
	for i := 0; i < Wide-1; i++ {
		screen.line[i] = ' '
	}
	return screen
}

func (screen *Screen) Set(val int) {
	if 0 <= val && val <= 100 {
		i := val * (Wide - 2) / 100
		if screen.line[i] == ' ' {
			screen.line[i] = '|'
			screen.nset += 1
			screen.Update()
		}
	}
}

func (screen *Screen) Update() {
	sep := strings.Repeat("/", Wide-1)

	var prog string
	for _, r := range screen.line {
		prog += string(r)
	}

	url := screen.url
	if len(url) > Wide-1 {
		url = url[:Wide-1]
	}

	Clear()
	fmt.Println(url)
	fmt.Println(
		screen.Percent(),
		",",
		screen.reqs,
		screen.newbytes,
		"/",
		screen.Bytes(),
		screen.Timer(),
		screen.PerSec(),
	)
	fmt.Println(sep)
	fmt.Println(prog)
	fmt.Println(sep)

	for i, err := range screen.errs {
		fmt.Println(i+1, ".", err.err)
	}
}

func (screen *Screen) Bytes() string {
	if screen.bytes < 0 {
		return "N/A"
	}

	return strconv.FormatInt(screen.bytes, 10)
}

func (screen *Screen) Timer() string {
	if screen.timer > 0 {
		return screen.timer.String()
	}

	return "N/A"
}

func (screen *Screen) Percent() int {
	return screen.nset * 100 / (Wide - 1)
}

func (screen *Screen) PerSec() string {
	n := screen.persec
	u := "b"

	if n >= 1024 {
		n /= 1024
		u = "kb"
	}

	if n >= 1024 {
		n /= 1024
		u = "mb"
	}

	if n >= 1024 {
		n /= 1024
		u = "gb"
	}

	return strconv.FormatInt(n, 10) + u + "/sec"
}

func main() {
	flags.Parse()

	if len(os.Args) == 1 {
		fmt.Println("Fetch newest go source...")
		urlStr, err := NewestRelease()
		if err != nil {
			log.Fatal(err)
		}

		urlo, err := url.Parse(urlStr)
		if err != nil {
			log.Fatal(err)
		}

		out = path.Base(urlo.Path)
		if len(out) == 0 {
			out = "go.src.tar.gz"
		}

		N = 2
	} else {
		if len(url) == 0 || len(out) == 0 {
			fmt.Println(os.Args[0], "-o=[out]", "-url=[url]", "-n=[n]")
			return
		}
	}

	w, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}

	tripper := &MyRequestTripper{
		RequestTripper206N: partion.New206(urlStr, w, N),
	}

	reactor := partion.NewReactor()
	reactor.NewRequestTripper(partion.New206(urlStr))
	reactor.NewPracticer(reactor.NewPackagePracticer())
}
