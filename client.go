package httpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"unsafe"
)

// ErrBadStatus indicates bad status code error
var ErrBadStatus = errors.New("bad status code")

// ErrWithResponseData represent http error with body, status code and headers
type ErrWithResponseData struct {
	base    error
	code    int
	headers http.Header
	body    *bytes.Buffer
}

func (e ErrWithResponseData) Error() string {
	return fmt.Sprintf("%s:%d", e.base, e.code)
}

func (e ErrWithResponseData) Unwrap() error {
	return e.base
}

// Code returns status code
func (e ErrWithResponseData) Code() int {
	return e.code
}

// BodyReader returns body reader
func (e ErrWithResponseData) BodyReader() io.Reader {
	return e.body
}

// StrBody returns body as string
func (e ErrWithResponseData) StrBody() string {
	return e.body.String()
}

// ByteBody returns body as byte slice
func (e ErrWithResponseData) ByteBody() []byte {
	return e.body.Bytes()
}

// Headers returns http headers
func (e ErrWithResponseData) Headers() http.Header {
	return e.headers
}

type (
	// Decoder represent decoder function
	Decoder func(io.Reader, interface{}) error
	// Encoder represent encoder function
	Encoder func(io.Writer, interface{}) error
)

// JSONDecoder decode data from r to v as json (used by default)
func JSONDecoder(r io.Reader, v interface{}) error {
	err := json.NewDecoder(r).Decode(v)
	if err != nil {
		return fmt.Errorf("httpcli:JSONDecoder:%w", err)
	}

	return nil
}

// JSONEncoder encode data from v to w as json (used by default)
func JSONEncoder(w io.Writer, v interface{}) error {
	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		return fmt.Errorf("httpcli:JSONEncoder:%w", err)
	}

	return nil
}

// FormURLEncodedDecoder decode data from r to v as x-www-form-urlencoded
func FormURLEncodedDecoder(r io.Reader, v interface{}) error {
	tmp := v.(*url.Values)

	var b bytes.Buffer

	_, err := io.Copy(&b, r)
	if err != nil {
		return fmt.Errorf("httpcli:FormURLEncodedDecoder:read:%w", err)
	}

	val, err := url.ParseQuery(b.String())
	if err != nil {
		return fmt.Errorf("httpcli:FormURLEncodedDecoder:parse:%w", err)
	}

	*tmp = val

	return nil
}

func stringToBytes(s string) []byte {
	return *(*[]byte)(unsafe.Pointer(
		&struct {
			string
			Cap int
		}{s, len(s)},
	))
}

// FormURLEncodedEncoder encode data from v to w as x-www-form-urlencoded
func FormURLEncodedEncoder(w io.Writer, v interface{}) error {
	val := v.(url.Values)

	_, err := w.Write(stringToBytes(val.Encode()))
	if err != nil {
		return fmt.Errorf("httpcli:FormURLEncodedEncoder:%w", err)
	}

	return nil
}

// BytesDecoder no actually do any decode operation instead it just copied body from reader r to byte slice v
func BytesDecoder(reader io.Reader, v interface{}) error {
	r := v.(*[]byte)

	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("httpcli:BytesDecoder:%w", err)
	}

	*r = data

	return nil
}

// BytesEncoder no actually do any encode operation instead it just writes byte slice v to writer w
func BytesEncoder(w io.Writer, v interface{}) error {
	if _, err := w.Write(v.([]byte)); err != nil {
		return fmt.Errorf("httpcli:BytesEncoder:%w", err)
	}

	return nil
}

type (
	// Client represent http client
	Client struct {
		name               string
		defaultRequestMeta requestMeta
	}

	// RequestOption represent function to override request options
	RequestOption func(*requestMeta)

	// Doer represent interface to custom Do operation
	Doer interface {
		Do(*http.Request) (*http.Response, error)
	}
)

// WithDoer request option to change default Doer
func WithDoer(d Doer) RequestOption {
	return func(meta *requestMeta) {
		meta.doer = d
	}
}

func doWithHeaders(set bool, h []string) RequestOption {
	if len(h)%2 != 0 {
		panic("number of arguments should be even")
	}

	return func(meta *requestMeta) {
		if meta.headers == nil {
			meta.headers = make(http.Header, len(h)/2)
		}

		op := meta.headers.Add
		if set {
			op = meta.headers.Set
		}

		for i := 0; i < len(h)-1; i += 2 {
			op(h[i], h[i+1])
		}
	}
}

// SetHeaders request option to set custom headers
func SetHeaders(h ...string) RequestOption {
	return doWithHeaders(true, h)
}

// AddHeaders request option to add custom headers
func AddHeaders(h ...string) RequestOption {
	return doWithHeaders(false, h)
}

// RemoveHeaders request option to remove custom headers
func RemoveHeaders(h ...string) RequestOption {
	return func(meta *requestMeta) {
		for _, header := range h {
			meta.headers.Del(header)
		}
	}
}

// ExpectedCodes request options to specify expected status codes
func ExpectedCodes(codes ...int) RequestOption {
	if len(codes) == 0 {
		panic("at least one code required")
	}

	return func(rm *requestMeta) {
		if rm.okCodes == nil || !rm.customOkCodes {
			rm.okCodes = make(codeSet, len(codes))
			rm.customOkCodes = true
		}

		for _, c := range codes {
			rm.okCodes[c] = true
		}
	}
}

// WithDecoder request option to change request decoder
func WithDecoder(v Decoder) RequestOption {
	return func(meta *requestMeta) {
		meta.dec = v
	}
}

// WithEncoder request option to change request encoder
func WithEncoder(v Encoder) RequestOption {
	return func(meta *requestMeta) {
		meta.enc = v
	}
}

type requestMeta struct {
	doer          Doer
	headers       http.Header
	okCodes       codeSet
	customOkCodes bool
	enc           Encoder
	dec           Decoder
}

type codeSet map[int]bool

var defaultOkCodes = codeSet{
	http.StatusOK:        true,
	http.StatusCreated:   true,
	http.StatusAccepted:  true,
	http.StatusNoContent: true,
}

func (c codeSet) Clone() codeSet {
	if c == nil {
		return nil
	}

	newC := make(codeSet, len(c))

	for k, v := range c {
		newC[k] = v
	}

	return newC
}

// New create new http client
func New(name string, opt ...RequestOption) Client {
	var cli http.Client

	h := make(http.Header)
	h.Set("user-agent", name)
	h.Set("content-type", "application/json")

	c := Client{
		name: name,
		defaultRequestMeta: requestMeta{
			doer:    &cli,
			okCodes: defaultOkCodes.Clone(),
			enc:     JSONEncoder,
			dec:     JSONDecoder,
			headers: h,
		},
	}

	for _, o := range opt {
		o(&c.defaultRequestMeta)
	}

	return c
}

func (c Client) do(req *http.Request, rm requestMeta) (*http.Response, error) {
	resp, err := rm.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do:%w", err)
	}

	if rm.okCodes[resp.StatusCode] {
		return resp, nil
	}

	var buf bytes.Buffer

	baseErr := ErrBadStatus

	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		baseErr = fmt.Errorf("%w:copy body:%s", ErrBadStatus, err)
	}

	_ = resp.Body.Close()

	return nil, fmt.Errorf("do:%w",
		ErrWithResponseData{
			baseErr, resp.StatusCode,
			resp.Header.Clone(), &buf,
		})
}

func buildRequestBody(enc Encoder, payload interface{}) (*bytes.Buffer, error) {
	var buf bytes.Buffer

	if payload == nil {
		return &buf, nil
	}

	if err := enc(&buf, payload); err != nil {
		return nil, fmt.Errorf("encode_request:%w", err)
	}

	return &buf, nil
}

func parseResponseBody(dec Decoder, body io.Reader, result interface{}) error {
	if result == nil {
		return nil
	}

	if err := dec(body, result); err != nil {
		return fmt.Errorf("decode_response:%w", err)
	}

	return nil
}

func (c Client) request(ctx context.Context, url, method string, payload,
	result interface{}, opt []RequestOption) error {
	rm := c.defaultRequestMeta

	if len(opt) != 0 {
		rm = requestMeta{
			doer:    c.defaultRequestMeta.doer,
			headers: c.defaultRequestMeta.headers.Clone(),
			okCodes: c.defaultRequestMeta.okCodes.Clone(),
			enc:     c.defaultRequestMeta.enc,
			dec:     c.defaultRequestMeta.dec,
		}

		for _, o := range opt {
			o(&rm)
		}
	}

	buf, err := buildRequestBody(rm.enc, payload)
	if err != nil {
		return fmt.Errorf("%s:request:%w", c.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, buf)
	if err != nil {
		return fmt.Errorf("%s:request:%w", c.name, err)
	}

	req.Header = rm.headers

	resp, err := c.do(req, rm)
	if err != nil {
		return fmt.Errorf("%s:request:%w", c.name, err)
	}

	defer resp.Body.Close()

	err = parseResponseBody(rm.dec, resp.Body, result)
	if err != nil {
		return fmt.Errorf("%s:request:%w", c.name, err)
	}

	return nil
}

// Get send get request
func (c Client) Get(ctx context.Context, url string, result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, "GET", nil, result, opt)
}

// Post send post request
func (c Client) Post(ctx context.Context, url string, payload, result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, "POST", payload, result, opt)
}

// Put send put request
func (c Client) Put(ctx context.Context, url string, payload, result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, "PUT", payload, result, opt)
}

// Patch send patch request
func (c Client) Patch(ctx context.Context, url string, payload, result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, "PATCH", payload, result, opt)
}

// Delete send delete request
func (c Client) Delete(ctx context.Context, url string, result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, "DELETE", nil, result, opt)
}

// Head send head request
func (c Client) Head(ctx context.Context, url string, opt ...RequestOption) error {
	return c.request(ctx, url, "HEAD", nil, nil, opt)
}

// Request send request
func (c Client) Request(ctx context.Context, url, method string, payload,
	result interface{}, opt ...RequestOption) error {
	return c.request(ctx, url, strings.ToUpper(method), payload, result, opt)
}
