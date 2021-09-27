package httpcli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func Test_Error(t *testing.T) {
	var buf bytes.Buffer

	buf.WriteString("not found")

	h := make(http.Header)
	h.Set("content-type", "1")

	err := fmt.Errorf("do:%w", ErrWithResponseData{ErrBadStatus, 404, h, &buf})
	err = fmt.Errorf("test:%w", err)

	if err.Error() != "test:do:bad status code:404" {
		t.Errorf("bad err message: %s", err)
		t.FailNow()
	}

	if !errors.Is(err, ErrBadStatus) {
		t.Error("err should be ErrBadStatus")
		t.FailNow()
	}

	var data ErrWithResponseData

	if !errors.As(err, &data) {
		t.Error("err should be as ErrWithResponseData")
		t.FailNow()
	}

	if data.Code() != 404 {
		t.Error("err code should be 404:", data.Code())
		t.FailNow()
	}

	if string(data.ByteBody()) != "not found" {
		t.Error("bad err payload:", string(data.ByteBody()))
		t.FailNow()
	}

	if data.StrBody() != "not found" {
		t.Error("bad err payload:", data.StrBody())
		t.FailNow()
	}

	payload, err := ioutil.ReadAll(data.BodyReader())
	if err != nil {
		t.Error(err)
		t.FailNow()
	}

	if string(payload) != "not found" {
		t.Error("bad err payload:", string(payload))
		t.FailNow()
	}

	if data.Headers().Get("Content-Type") != "1" {
		t.Error("bad headers:", data.Headers())
	}
}

func Test_buildRequestBody(t *testing.T) {
	type args struct {
		enc     Encoder
		payload interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			"01",
			args{
				enc: JSONEncoder,
				payload: struct {
					A string `json:"a"`
				}{A: "1"},
			},
			`{"a":"1"}`,
			false,
		},
		{
			"02",
			args{
				enc:     BytesEncoder,
				payload: []byte(`{"a":"1"}`),
			},
			`{"a":"1"}`,
			false,
		},
		{
			"03",
			args{
				enc: FormURLEncodedEncoder,
				payload: func() url.Values {
					val, _ := url.ParseQuery("username=login&password=test")
					return val
				}(),
			},
			`password=test&username=login`,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildRequestBody(tt.args.enc, tt.args.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildRequestBody() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got.String() != tt.want && got.String() != tt.want+"\n" {
				t.Errorf("buildRequestBody() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func Test_parseResponse(t *testing.T) {
	type tp byte

	const (
		_ tp = iota
		byts
		jsonTp
		form
		empty
	)

	type args struct {
		body       string
		resultType tp
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"01",
			args{
				body:       `{"a":1}`,
				resultType: byts,
			},
			false,
		},
		{
			"02",
			args{
				body:       `{"a":1}`,
				resultType: jsonTp,
			},
			false,
		},
		{
			"03",
			args{
				body:       `{"a":1`,
				resultType: jsonTp,
			},
			true,
		},
		{
			"04",
			args{
				body:       `username=login&password=test`,
				resultType: form,
			},
			false,
		},
		{
			"05",
			args{
				body:       `username=login&password=fsd;f;fsdfs`,
				resultType: form,
			},
			true,
		},
		{
			"06",
			args{
				body:       ``,
				resultType: empty,
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error

			var buf bytes.Buffer

			buf.WriteString(tt.args.body)

			switch tt.args.resultType {
			case byts:
				var result []byte
				err = parseResponseBody(BytesDecoder, &buf, &result)

				if (err != nil) != tt.wantErr {
					t.Errorf("parseResponseBody() error = %v, wantErr %v", err, tt.wantErr)
					t.FailNow()
				}

				if string(result) != tt.args.body {
					t.Errorf("parseResponseBody() result = %v, body %v", result, tt.args.body)
				}
			case jsonTp:
				type resultTp struct {
					A int
				}

				var result resultTp

				err = parseResponseBody(JSONDecoder, &buf, &result)

				if tt.wantErr {
					if err == nil {
						t.Errorf("parseResponseBody() wantErr %v but error empty", tt.wantErr)
						t.FailNow()
					}

					return
				}

				var expectedR resultTp

				err = json.Unmarshal(stringToBytes(tt.args.body), &expectedR)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				if !reflect.DeepEqual(result, expectedR) {
					t.Errorf("parseResponseBody() result = %v, expected %v", result, expectedR)
				}
			case form:
				var result url.Values

				err = parseResponseBody(FormURLEncodedDecoder, &buf, &result)
				if tt.wantErr {
					if err == nil {
						t.Errorf("parseResponseBody() wantErr %v but error empty", tt.wantErr)
						t.FailNow()
					}

					return
				}

				expected, err := url.ParseQuery(tt.args.body)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				if !reflect.DeepEqual(result, expected) {
					t.Errorf("parseResponseBody() result = %v, expected %v", result, expected)
				}
			case empty:
				err = parseResponseBody(FormURLEncodedDecoder, &buf, nil)
				if tt.wantErr {
					if err == nil {
						t.Errorf("parseResponseBody() wantErr %v but error empty", tt.wantErr)
						t.FailNow()
					}
				}
			}
		})
	}
}

func checkFuncName(v interface{}, name string) bool {
	return strings.HasSuffix(runtime.FuncForPC(reflect.ValueOf(v).Pointer()).Name(), name)
}

func Test_requestOptions(t *testing.T) {
	cli := New("test", WithDoer(nil))
	rmt := cli.defaultRequestMeta

	if rmt.headers.Get("content-type") != "application/json" {
		t.Errorf(`rmt.headers.Get("content-type") != "application/json"`)
		t.FailNow()
	}

	if rmt.headers.Get("user-agent") != "test" {
		t.Errorf(`rmt.headers.Get("user-agent") != "test"`)
		t.FailNow()
	}

	if rmt.doer != nil {
		t.Errorf(`rmt.doer != nil`)
		t.FailNow()
	}

	if !checkFuncName(rmt.dec, "JSONDecoder") {
		t.Errorf(`!checkFuncName(rmt.dec, "JSONDecoder")`)
		t.FailNow()
	}

	if !checkFuncName(rmt.enc, "JSONEncoder") {
		t.Errorf(`!checkFuncName(rmt.enc, "JSONEncoder")`)
		t.FailNow()
	}

	if !reflect.DeepEqual(rmt.okCodes, defaultOkCodes) {
		t.Errorf(`!reflect.DeepEqual(rmt.okCodes, defaultOkCodes)`)
		t.FailNow()
	}

	opt := []RequestOption{WithDecoder(BytesDecoder), WithEncoder(BytesEncoder),
		SetHeaders("content-type", "test"), AddHeaders("content-type", "test2"),
		RemoveHeaders("user-agent"), ExpectedCodes(400)}

	for _, o := range opt {
		o(&rmt)
	}

	if rmt.headers.Values("content-type")[0] != "test" && rmt.headers.Values("content-type")[1] != "test2" {
		t.Errorf(`rmt.headers.Values("content-type")[0] != "test" && rmt.headers.Values("content-type")[1] != "test2"`)
		t.FailNow()
	}

	if rmt.headers.Get("user-agent") != "" {
		t.Errorf(`rmt.headers.Get("user-agent") != ""`)
		t.FailNow()
	}

	if !checkFuncName(rmt.dec, "BytesDecoder") {
		t.Errorf(`!checkFuncName(rmt.dec, "BytesDecoder")`)
		t.FailNow()
	}

	if !checkFuncName(rmt.enc, "BytesEncoder") {
		t.Errorf(`!checkFuncName(rmt.enc, "BytesEncoder")`)
		t.FailNow()
	}

	if !reflect.DeepEqual(rmt.okCodes, codeSet{400: true}) {
		t.Log(rmt.okCodes)
		t.Errorf(`!reflect.DeepEqual(rmt.okCodes, codeSet{203: true})`)
		t.FailNow()
	}
}

func TestClient_request(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("err") != "1" {
			rw.WriteHeader(http.StatusOK)
		} else {
			rw.WriteHeader(http.StatusBadRequest)
		}

		resp := map[string]interface{}{
			"headers": req.Header.Clone(),
			"url":     req.URL.String(),
		}

		data, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(err)
		}

		if len(data) != 0 {
			resp["body"] = base64.StdEncoding.EncodeToString(data)
		}

		rData, err := json.Marshal(resp)
		if err != nil {
			panic(err)
		}

		_, err = rw.Write(rData)
		if err != nil {
			panic(err)
		}
	}))

	cli := server.Client()

	defer server.Close()

	type args struct {
		needErr bool
		method  string
		payload interface{}
		opt     []RequestOption
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantResult interface{}
	}{
		{
			"01",
			args{
				method: "GET",
			},
			false,
			map[string]interface{}{"url": "/",
				"headers": http.Header{
					"Accept-Encoding": []string{"gzip"},
					"Content-Type":    []string{"application/json"},
					"User-Agent":      []string{"test"}}},
		},
		{
			"02",
			args{
				method:  "POST",
				payload: map[string]interface{}{"test": 1},
			},
			false,
			map[string]interface{}{"url": "/",
				"body": base64.StdEncoding.EncodeToString(
					func() []byte {
						v, _ := json.Marshal(map[string]interface{}{"test": 1})
						return append(v, '\n')
					}()),
				"headers": http.Header{
					"Accept-Encoding": []string{"gzip"},
					"Content-Length":  []string{"11"},
					"Content-Type":    []string{"application/json"},
					"User-Agent":      []string{"test"}}},
		},
		{
			"03",
			args{
				method: "GET",
				opt:    []RequestOption{SetHeaders("test", "1")},
			},
			false,
			map[string]interface{}{"url": "/",
				"headers": http.Header{
					"Test":            []string{"1"},
					"Accept-Encoding": []string{"gzip"},
					"Content-Type":    []string{"application/json"},
					"User-Agent":      []string{"test"}}},
		},
		{
			"04",
			args{
				method:  "GET",
				needErr: true,
			},
			true,
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New("test", WithDoer(cli))

			uri := server.URL
			if tt.args.needErr {
				uri += "?err=1"
			}

			var result map[string]interface{}

			err := c.request(context.Background(), uri, tt.args.method, tt.args.payload,
				&result, tt.args.opt)

			if tt.wantErr {
				if err == nil {
					t.Error("request() want error but its nil")
				}

				var gotErr ErrWithResponseData

				if !errors.As(err, &gotErr) {
					t.Errorf("request() error should be ErrWithResponseData")
					t.FailNow()
				}
			} else if fmt.Sprintln(result) != fmt.Sprintln(tt.wantResult) {
				t.Errorf("request() result = %v, wantResult %v", result, tt.wantResult)
			}
		})
	}
}
