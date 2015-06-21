package partion

import (
	"errors"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const END = -1

// Bar represents a whole or partial progress bar.
// 500 bytes: X = 0, Y = 500 (exclusive)
type Bar struct {
	X int64
	Y int64
}

func (bar Bar) Len() int64 {
	return bar.Y - bar.X
}

func (bar Bar) Zero() bool {
	return bar.Len() == 0
}

func NewBar(X, Y int64) Bar {
	return Bar{X, Y}
}

type Practicer interface {
	// Do requests the request. it's functionality is the same as
	// http.Client.Do since i imitate it. :)
	Do(*http.Request) (*http.Response, error)

	// CancelRequest cancel the request without regard to whether requesting or not.
	// CancelRequest no-op if given request is not appropriate.
	CancelRequest(*http.Request)
}

// Abstract interface.
type Runner interface {
	// Begin starts new request with Bar
	Begin(Bar) error

	// CancelAll cancels all requests now being processed.
	// error "request cancelled" is passed to Raise of RequestTripper
	CancelAll()
}

type RequestTripper interface {
	Setup(Runner)

	// you can call Begin
	Ready(*http.Response, Bar)

	// NewRequest returns new request object.
	NewRequest() (*http.Request, error)

	// Bounded turns the request into a fetch-bounded request that can
	// retrieve partial content of the data you request than complete
	// content.of that.
	Bounded(*http.Request, Bar) error

	// Partiable sees whether Partial Content is supported in the
	// server by inspecting the response. if the server supports, returns
	// true, otherwise false.
	Partiable(*http.Response) bool

	// Adjust adjust the requested bar.
	Adjust(*http.Response) (Bar, error)

	// Raise make a decision that the error raises to cancel all
	// requests being processed. if error occurs but you do not throw
	// the error while trying to recover the error e.g starting new
	// request for the missed bar, you might be able to recover the error.
	Raise(missed Bar, err error) error

	// Incoming handles sincoming data from the server.
	Incoming(data []byte, offset int64, bar Bar)
}

type Reactor interface {
	// RequestTripper sets Reactor's own RequestTripper
	SetRequestTripper(RequestTripper)

	// Practicer sets Reactor's own Practicer
	SetPracticer(Practicer)

	Runner

	// Wait waits for all requests to be done.
	Wait() error
}

type PackagePracticer struct {
	*http.Transport
	*http.Client
}

func NewPackagePracticer() *PackagePracticer {
	tr := new(http.Transport)
	return &PackagePracticer{
		tr,
		&http.Client{
			Transport: tr,
		},
	}
}

type RequestTripper206N struct {
	// Request
	// Only GET is allowed to request
	Request *http.Request

	// Data from server is written to
	Writer io.WriterAt

	// N specifies the number of partial request. moderate value be
	// better at performance; what is the moderate? i don't know, but
	// what about considering server, network, content size, and
	// etc...?
	N int

	Runner

	ContentLength int64
}

func (o *RequestTripper206N) Setup(runner Runner) {
	o.Runner = runner
}

// NewRequest copy Request property; URL, Header is copied into new
// request, others not.
func (o *RequestTripper206N) NewRequest() (*http.Request, error) {
	if o.Request.Method != "GET" {
		err := errors.New("Only GET is allowed to request: " + o.Request.Method)
		return nil, err
	}

	req, err := http.NewRequest(
		"GET",
		o.Request.URL.String(),
		nil,
	)
	if err != nil {
		return nil, err
	}

	for key, val := range o.Request.Header {
		req.Header[key] = val
	}

	return req, nil
}

func HTTPBounded(req *http.Request, bar Bar) error {
	if bar.X == 0 && bar.Y == END {
		return nil
	}

	// bytes=X-Y"
	bar.Y -= 1
	val := "bytes=" + strconv.FormatInt(bar.X, 10) + "-"
	if bar.Y > END {
		val += strconv.FormatInt(bar.Y, 10)
	}

	req.Header.Set("Range", val)

	return nil
}

func (o *RequestTripper206N) Bounded(req *http.Request, bar Bar) error {
	return HTTPBounded(req, bar)
}

// http://www.w3.org/Protocols/rfc2616/rfc2616-sec10.html#sec10.4.17
// 10.4.17 416 Requested Range Not Satisfiable
// A server SHOULD return a response with this status code if a
// request included a Range request-header field (section 14.35), and
// none of the range-specifier values in this field overlap the
// current extent of the selected resource, and the request did not
// include an If-Range request-header field.

func HTTPPartiable(resp *http.Response) bool {
	// http://www.w3.org/Protocols/rfc2616/rfc2616-sec14.html#sec14.5
	if _, in := resp.Header["Accept-Ranges"]; in {
		// http://www.w3.org/Protocols/rfc2616/rfc2616-sec3.html#sec3.12
		return resp.Header.Get("Accept-Ranges") != "none"
	}

	if _, in := resp.Request.Header["Range"]; in {
		return resp.StatusCode != 206
	}

	if resp.StatusCode == 200 {
		return resp.ContentLength != -1
	}

	return true
}

func (o *RequestTripper206N) Partiable(resp *http.Response) bool {
	return HTTPPartiable(resp)
}

func ParseContentRangeHeader(val string) (unit string, x int64, y int64, leng int64, err error) {
	var i, j int

	seps := strings.Fields(val)
	if len(seps) != 2 {
		goto Invalid
	}

	unit = seps[0]
	i = strings.Index(seps[1], "/")
	if i < 0 {
		goto Invalid
	}

	leng, err = strconv.ParseInt(seps[1][i+1:], 10, 64)
	if err != nil {
		goto Invalid
	}

	j = strings.Index(seps[1][:i], "-")
	if j < 0 {
		goto Invalid
	}

	x, err = strconv.ParseInt(seps[1][:i][:j], 10, 64)
	if err != nil {
		goto Invalid
	}

	y, err = strconv.ParseInt(seps[1][:i][j+1:], 10, 64)
	if err != nil {
		goto Invalid
	}

	return

Invalid:
	errmsg := "invalid Content-Range: "
	if err != nil {
		errmsg += err.Error()
		errmsg += ": "
	}

	errmsg += val
	err = errors.New(errmsg)
	return
}

func PartitionZ(bar Bar, n int) []Bar {
	u := bar.Len() / int64(n)
	r := bar.Len() % int64(n)
	bars := make([]Bar, n)
	castn := int64(n)

	for i := int64(0); i < castn; i++ {
		bars[i].X = i * u
		bars[i].Y = i * (u + 1)
		if i <= r-1 {
			bars[i].X += 1
		}
		if 1 <= i && i <= r {
			bars[i].Y += 1
		}
	}

	return bars
}

func (o *RequestTripper206N) Ready(resp *http.Response, bar Bar) {
	bars := PartitionZ(bar, o.N)

	for _, bar := range bars[1:] {
		if err := o.Begin(NewBar(
			bar.X,
			END,
		)); err != nil {
			return
		}
	}
}

func (o *RequestTripper206N) SetContentLength(leng int64) error {
	if o.ContentLength == 0 {
		o.ContentLength = leng
	} else {
		if o.ContentLength != leng {
			err := errors.New("so strange, is it possible?: the length of content changed:")
			return err
		}
	}

	return nil
}

func (o *RequestTripper206N) Adjust(resp *http.Response) (Bar, error) {
	// http://www.w3.org/Protocols/rfc2616/rfc2616-sec10.html#sec10.4.
	// 17 A server SHOULD return a response with this status code if a
	// request included a Range request-header field (section 14.35),
	// and none of the range-specifier values in this field overlap
	// the current extent of the selected resource When this status
	// code is returned for a byte-range request, the response SHOULD
	// include a Content-Range entity-header field specifying the
	// current length of the selected resource - but some not include
	// Content-Range specifing the length of resource althogh returned
	// status code is 416
	if resp.StatusCode == 416 {
		if _, in := resp.Header["Content-Range"]; in {
			rang := resp.Header.Get("Content-Range")
			if leng, err := strconv.ParseInt(rang, 10, 64); err == nil {
				if err := o.SetContentLength(leng); err != nil {
					return Bar{}, err
				}
			}
		}

		return Bar{}, nil
	}

	if resp.StatusCode == 200 {
		leng := resp.ContentLength

		if err := o.SetContentLength(leng); err != nil {
			return Bar{}, err
		}

		return NewBar(0, leng), nil
	}

	unit, x, y, leng, err := ParseContentRangeHeader(resp.Header.Get("Content-Range"))
	if err != nil {
		return Bar{}, err
	}

	if unit != "bytes" {
		err := errors.New("bytes unit not indicates bytes: " + unit)
		return Bar{}, err
	}

	if err := o.SetContentLength(leng); err != nil {
		return Bar{}, err
	}

	return NewBar(x, y+1), nil
}

func (o *RequestTripper206N) Raise(bar Bar, err error) error {
	return err
}

func (o *RequestTripper206N) Incoming(data []byte, offset int64, bar Bar) {
	o.Writer.WriteAt(data, offset)
}

func New206(url string, w io.WriterAt, N int) (*RequestTripper206N, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	return &RequestTripper206N{
		Request: req,
		Writer:  w,
		N:       N,
	}, nil
}

type assignment struct {
	Bar

	sync.Mutex

	// 기본적으로 생성될 때, Bar.X와 같은 값을 가진다.
	offset int64
	src    io.Reader
	resp   *http.Response
	// 확장 가능도 나중에 추가
}

func min64(x, y int64) int64 {
	if x > y {
		return y
	}
	return x
}

// func max64(x, y int64) int64 {
// 	if x > y {
// 		return x
// 	}
// 	return y
// }

func (assign *assignment) Read(buf []byte) (int, error) {
	if assign.Y == END {
		return assign.src.Read(buf)
	}

	if assign.Y <= assign.offset {
		return 0, io.EOF
	}
	greatest := min64(
		assign.Y-assign.offset,
		int64(len(buf)),
	)
	n, err := assign.src.Read(buf[:greatest])
	assign.offset += int64(n)
	return n, err
}

type assignments []*assignment

func (assigns assignments) Len() int {
	return len(assigns)
}

func (assigns assignments) Less(i, j int) bool {
	return assigns[i].X < assigns[j].X
}

func (assigns assignments) Swap(i, j int) {
	assigns[i], assigns[j] = assigns[j], assigns[i]
}

type SuperReactor struct {
	RequestTripper
	Practicer

	canBegin       bool
	supportPartial bool

	errc chan error

	wg      sync.WaitGroup
	beginMu sync.Mutex

	assigns assignments
}

func (reactor *SuperReactor) SetRequestTripper(tri RequestTripper) {
	reactor.RequestTripper = tri
	reactor.Setup(reactor)
}

func (reactor *SuperReactor) SetPracticer(practicer Practicer) {
	reactor.Practicer = practicer
}

func (reactor *SuperReactor) between(i int64) (*assignment, error) {
	for _, assign := range reactor.assigns {
		if assign.X <= i && i <= assign.Y {
			return assign, nil
		}
	}

	errmsg := "out of range: " + strconv.FormatInt(i, 10)
	return nil, errors.New(errmsg)
}

func (reactor *SuperReactor) Begin(bar Bar) error {
	reactor.beginMu.Lock()
	defer reactor.beginMu.Unlock()

	if !reactor.canBegin {
		return errors.New("Begin not allowed")
	}

	if !reactor.supportPartial {
		return errors.New("partial request not supported")
	}

	if bar.Y != END {
		return errors.New("two partitioning not supported")
	}

	embrace, err := reactor.between(bar.X)
	if err != nil {
		return err
	}

	if embrace.offset > bar.X {
		return errors.New("violate consumed bar")
	}

	embrace.Lock()
	embrace.Y, bar.Y = bar.X, embrace.Y
	embrace.Unlock()
	assign := &assignment{
		Bar:    bar,
		offset: bar.X,
	}
	reactor.assigns = append(reactor.assigns, assign)

	// really assigns need to be sorted?
	sort.Sort(reactor.assigns)

	reactor.wg.Add(1)
	go func() {
		if reactor.open(assign) == nil {
			reactor.streaming(assign)
		}
		reactor.wg.Done()
	}()

	return nil
}

func (reactor *SuperReactor) asyncDo(req *http.Request) (chan *http.Response, chan error) {
	respc, errc := make(chan *http.Response, 1), make(chan error, 1)
	go func() {
		resp, err := reactor.Do(req)
		respc <- resp
		errc <- err
	}()

	return respc, errc
}

func (reactor *SuperReactor) raise(err error) {
	select {
	case reactor.errc <- err:
	default:
	}
}

func (reactor *SuperReactor) reraise(bar Bar, err error) error {
	if err = reactor.Raise(bar, err); err != nil {
		reactor.raise(err)
	}
	return err
}

func (reactor *SuperReactor) open(assign *assignment) error {
	req, err := reactor.NewRequest()
	if err != nil {
		reactor.raise(err)
		return err
	}

	if err = reactor.Bounded(req, assign.Bar); err != nil {
		reactor.raise(err)
		return err
	}

	respc, errc := reactor.asyncDo(req)

	select {
	case err := <-reactor.errc:
		reactor.CancelRequest(req)
		reactor.raise(err)
		return err

	case err := <-errc:
		resp := <-respc

		if err != nil {
			if err := reactor.reraise(assign.Bar, err); err != nil {
				return err
			}
		}

		supportPartial := reactor.Partiable(resp)
		reactor.beginMu.Lock()
		reactor.supportPartial = supportPartial
		reactor.beginMu.Unlock()

		assign.Lock()
		defer assign.Unlock()

		assign.resp = resp
		assign.src = resp.Body

		if supportPartial {
			bar, err := reactor.Adjust(resp)
			if err != nil {
				reactor.raise(err)
				return err
			}
			assign.Bar = bar
		}
	}

	return nil
}

func (reactor *SuperReactor) streaming(assign *assignment) {
	bufc, errc := make(chan []byte, 1), make(chan error, 1)
	offsetc, barc := make(chan int64, 1), make(chan Bar, 1)

	go func() {
		for {
			var buf [4096]byte
			runtime.Gosched()
			assign.Lock()
			offsetc <- assign.offset
			barc <- assign.Bar
			n, err := assign.Read(buf[:])
			assign.Unlock()

			bufc <- buf[:n]
			errc <- err
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case err := <-reactor.errc:
			reactor.CancelRequest(assign.resp.Request)
			reactor.raise(err)
			return

		case err := <-errc:
			buf, offset, bar := <-bufc, <-offsetc, <-barc
			if len(buf) > 0 {
				reactor.Incoming(buf, offset, bar)
			}
			if err != nil {
				reactor.CancelRequest(assign.resp.Request)
				if err != io.EOF {
					reactor.reraise(
						NewBar(
							assign.offset,
							assign.Y,
						),
						err,
					)
				}
				if err := assign.resp.Body.Close(); err != nil {
					reactor.reraise(
						NewBar(
							assign.offset,
							assign.Y,
						),
						err,
					)
				}
				return
			}
		}
	}
}

func (reactor *SuperReactor) CancelAll() {
	err := errors.New("operation cancelled")
	reactor.raise(err)
}

func (reactor *SuperReactor) Wait() error {
	reactor.bootstrap()
	reactor.wg.Wait()
	close(reactor.errc)

	return <-reactor.errc
}

func (reactor *SuperReactor) bootstrap() {
	reactor.supportPartial = true
	assign := &assignment{
		Bar: Bar{
			0,
			END,
		},
	}
	reactor.assigns = append(reactor.assigns, assign)

	reactor.wg.Add(1)
	go func() {
		if reactor.open(assign) == nil {
			reactor.beginMu.Lock()
			reactor.canBegin = true
			reactor.beginMu.Unlock()

			reactor.Ready(assign.resp, assign.Bar)
			reactor.streaming(assign)
		}
		reactor.wg.Done()
	}()
}

func NewReactor() Reactor {
	return &SuperReactor{
		errc: make(chan error, 1),
	}
}
