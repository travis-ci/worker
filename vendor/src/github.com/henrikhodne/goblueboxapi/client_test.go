package goblueboxapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestUrlForPath(t *testing.T) {
	client := NewClient("customer_id", "api_key")
	u := client.urlForPath("/foo")
	want, _ := url.Parse("https://boxpanel.bluebox.net/api/foo.json")

	if !reflect.DeepEqual(u, want) {
		t.Errorf("UrlForPath returned %+v, want %+v", u, want)
	}
}

func TestNewRequest(t *testing.T) {
	client := NewClient("customer_id", "api_key")

	req, _ := client.newRequest("GET", "/foo", nil)

	if req.URL.Path != "/api/foo.json" {
		t.Errorf("NewRequest(/foo) URL = %q, want /api/foo.json", req.URL.Path)
	}

	authHeader, _ := base64.StdEncoding.DecodeString(req.Header.Get("Authorization")[6:])
	if !bytes.Equal(authHeader, []byte("customer_id:api_key")) {
		t.Errorf("NewRequest() authorization = %q, want \"Basic customer_id:api_key\"", authHeader)
	}
}

func TestDo(t *testing.T) {
	setup()
	defer teardown()

	type foo struct {
		Bar string
	}

	mux.HandleFunc("/api/foo.json", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		fmt.Fprintf(w, `{"Bar":"bar"}`)
	})

	req, _ := client.newRequest("GET", "/foo", nil)
	body := new(foo)
	client.do(req, body)

	want := &foo{"bar"}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("Response body = %v, want %v", body, want)
	}
}

func TestDo_httpError(t *testing.T) {
	setup()
	defer teardown()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
	})

	req, _ := client.newRequest("GET", "/", nil)
	err := client.do(req, nil)

	if err == nil {
		t.Error("Expected HTTP 400 error.")
	}
}

func TestDo_redirectLoop(t *testing.T) {
	setup()
	defer teardown()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})

	req, _ := client.newRequest("GET", "/", nil)
	err := client.do(req, nil)

	if err == nil {
		t.Error("Expected error to be returned.")
	}

	if err, ok := err.(*url.Error); !ok {
		t.Errorf("Expected a URL error; got %#v.", err)
	}
}
