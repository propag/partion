package main

import (
	"flag"
	"fmt"
	"github.com/propag/partion"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"strconv"
	"errors"
	"io/ioutil"
	"path"
	"regexp"
)

var out string

var urlStr string

var N int

func init() {
	flag.StringVar(&out, "o", "", "out file")
	flag.StringVar(&urlStr, "url", "", "url to fetch")
	flag.IntVar(&N, "n", 2, "the number of partitions")
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

const CWIDE = 80

const REALWIDE = CWIDE - 1

type MyRequestTripper struct {
	*partion.RequestTripper206N

	st    time.Time
	line  [REALWIDE]byte
	nsets int32

	amt                int64
	amtsec, amtseclast int64

	validN int32

	uMu, incomingMu sync.Mutex
}

func (o *MyRequestTripper) Set(offset int64, bar partion.Bar) {
	if o.ContentLength <= 0 {
		return
	}

	i := bar.X * int64(len(o.line)) / o.ContentLength
	j := offset * int64(len(o.line)) / o.ContentLength
	// fmt.Println(bar.X, len(o.line))
	// fmt.Println(i, j)

	var inc int32
	for ; i <= j; i++ {
		if o.line[i] != '|' {
			o.line[i] = '|'
			inc++
		}
	}

	if inc > 0 {
		atomic.AddInt32(&o.nsets, inc)
		// o.Update()
	}
}

// https://abcdefghijklmnopqrstuvwxyz.abcdefghijklmnopqrstuvwxyz/abcdefghijklmno...
// ////////////////////////////////////////////////////////////////////////////////
// ||||||||               ||||||||             ||||||||              ||||||||||||||
// ////////////////////////////////////////////////////////////////////////////////
// Whole: 1209323323 bytes
// Completion: 64% (28420232 bytes)
// Speed: 203212 bytes/sec
// Partitions: 4
// Time: 34s
// ...
// Done
//
// ...
// (error message)

func Clear() error {
	cmd := exec.Command("cmd", "/c", "cls")
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func (o *MyRequestTripper) Update() {
	o.uMu.Lock()

	urlStr := urlStr
	if len(urlStr) > REALWIDE {
		urlStr = urlStr[:len(urlStr)-3]
		urlStr += "..."
	}

	sep := strings.Repeat("/", REALWIDE)
	var lineStr string
	for _, u := range o.line {
		lineStr += string(u)
	}

	Clear()
	fmt.Println(urlStr)
	fmt.Println(sep)
	fmt.Println(lineStr)
	fmt.Println(sep)
	if o.ContentLength == -1 {
		fmt.Println("Whole:", "N/A")
		fmt.Println("Completion:", "N/A",
			"("+strconv.FormatInt(o.amt, 10)+" bytes)")
	} else {
		completion := int(o.nsets) * 100 / len(o.line)
		fmt.Println("Whole:", o.ContentLength, "bytes")
		fmt.Println("Completion:", strconv.Itoa(completion)+"%",
			"("+strconv.FormatInt(o.amt, 10)+" bytes)")
	}

	if o.amtseclast == 0 {
		fmt.Println("Speed:", o.amtsec, "bytes/sec")
	} else {
		fmt.Println("Speed:", o.amtseclast, "bytes/sec")
	}
	fmt.Println("Partitions:", o.validN)
	fmt.Println("Time:", time.Since(o.st))

	o.uMu.Unlock()
}

func (o *MyRequestTripper) Adjust(resp *http.Response) (partion.Bar, error) {
	bar, err := o.RequestTripper206N.Adjust(resp)
	if err == nil {
		atomic.AddInt32(&o.validN, 1)
	}

	return bar, err
}

func (o *MyRequestTripper) Incoming(buf []byte, offset int64, bar partion.Bar) {
	o.RequestTripper206N.Incoming(buf, offset, bar)
	o.incomingMu.Lock()
	o.Set(offset, bar)

	o.amt += int64(len(buf))
	o.amtsec += int64(len(buf))
	o.incomingMu.Unlock()
}

// func (screen *Screen) PerSec() string {
// 	n := screen.persec
// 	u := "b"

// 	if n >= 1024 {
// 		n /= 1024
// 		u = "kb"
// 	}

// 	if n >= 1024 {
// 		n /= 1024
// 		u = "mb"
// 	}

// 	if n >= 1024 {
// 		n /= 1024
// 		u = "gb"
// 	}

// 	return strconv.FormatInt(n, 10) + u + "/sec"
// }

type Ticker struct {
	C       <-chan time.Time
	stopsig chan bool
	ticker  *time.Ticker
}

func NewTicker(d time.Duration) *Ticker {
	timec, stopsig := make(chan time.Time, 1), make(chan bool, 1)
	ticker := &Ticker{
		C:       timec,
		stopsig: stopsig,
		ticker:  time.NewTicker(d),
	}
	timec <- time.Now()

	go func() {
		var curr time.Time
		var C chan time.Time

		for {
			select {
			case now := <-ticker.ticker.C:
				if C == nil {
					C = timec
					curr = now
				}
			case C <- curr:
				C = nil
			case <-stopsig:
				ticker.ticker.Stop()
				return
			}
		}
	}()

	return ticker
}

func (ticker *Ticker) Stop() {
	ticker.stopsig <- true
}

func main() {
	flag.Parse()

	if len(os.Args) == 1 {
		fmt.Println("Fetch newest go source...")
		var err error
		urlStr, err = NewestRelease()
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
		if len(urlStr) == 0 || len(out) == 0 {
			fmt.Println(os.Args[0], "-o=[out]", "-url=[url]", "-n=[n]")
			return
		}
	}

	w, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	tri206, err := partion.New206(urlStr, w, N)
	if err != nil {
		log.Fatal(err)
	}

	tri := &MyRequestTripper{
		RequestTripper206N: tri206,
	}

	for i := 0; i < len(tri.line); i++ {
		tri.line[i] = ' '
	}

	reactor := partion.NewReactor()
	reactor.SetRequestTripper(tri)
	reactor.SetPracticer(partion.NewPackagePracticer())

	ticker := NewTicker(time.Second)

	errc := make(chan error)
	tri.st = time.Now()
	go func() {
		errc <- reactor.Wait()
	}()

	for {
		select {
		case err := <-errc:
			tri.Update()
			fmt.Println()
			fmt.Println("...")
			if err == nil {
				fmt.Println("Done")
			} else {
				fmt.Println(err)
			}
			return
		case <-ticker.C:
			tri.incomingMu.Lock()
			tri.amtseclast = tri.amtsec
			tri.amtsec = 0
			tri.Update()
			tri.incomingMu.Unlock()
		}
	}
}
