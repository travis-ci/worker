package backend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/context"

	"github.com/travis-ci/worker/config"
)

var (
	blueboxMux      *http.ServeMux
	blueboxProvider *BlueBoxProvider
	blueboxServer   *httptest.Server
)

func blueboxTestSetup(t *testing.T, cfg *config.ProviderConfig) {
	blueboxMux = http.NewServeMux()
	blueboxServer = httptest.NewServer(blueboxMux)
	blueboxProvider, _ = NewBlueBoxProvider(cfg)
	blueboxProvider.client.BaseURL, _ = url.Parse(blueboxServer.URL)

	now := time.Now()
	jsonNow, _ := now.MarshalText()
	output := `[
        {"id": "ruby-template-id", "description": "travis-ruby-2015-07-07-00-00-a0b1c2d", "public": false, "created": "%s"},
        {"id": "jvm-template-id", "description": "travis-jvm-2015-07-07-00-00-a0b1c2d", "public": false, "created": "%s"}
    ]`
	blueboxMux.HandleFunc("/api/block_templates.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, fmt.Sprintf(output, jsonNow, jsonNow))
	})

	blueboxMux.HandleFunc("/api/blocks.json", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("template") != "jvm-template-id" {
			t.Errorf("Expected 'jvm-template-id', got '%s'", r.FormValue("template"))
		}
		fmt.Fprintf(w, `{"id": "block-id", "hostname": "block-id.example.com", "ips":[{"address":"192.0.2.1"}], "status": "queued"}`)
	})

	blueboxMux.HandleFunc("/api/blocks/block-id.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id": "block-id", "hostname": "block-id.example.com", "ips":[{"address":"192.0.2.1"}], "status": "running"}`)
	})

}

func blueboxTestTeardown() {
	blueboxServer.Close()
	blueboxMux = nil
	blueboxServer = nil
	blueboxProvider = nil
}

func TestBlueBoxStart(t *testing.T) {
	blueboxTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"CUSTOMER_ID":          "customer_id",
		"API_KEY":              "api_key",
		"LOCATION_ID":          "location_id",
		"PRODUCT_ID":           "product_id",
		"IPV6_ONLY":            "true",
		"LANGUAGE_MAP_CLOJURE": "jvm",
	}))
	defer blueboxTestTeardown()

	instance, err := blueboxProvider.Start(context.TODO(), &StartAttributes{Language: "jvm", Group: ""})
	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "block-id" {
		t.Errorf("expected 'block-id', got '%s'", instance.ID())
	}
}

func TestBlueBoxStartWithMapping(t *testing.T) {
	blueboxTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"CUSTOMER_ID":          "customer_id",
		"API_KEY":              "api_key",
		"LOCATION_ID":          "location_id",
		"PRODUCT_ID":           "product_id",
		"IPV6_ONLY":            "true",
		"LANGUAGE_MAP_CLOJURE": "jvm",
	}))
	defer blueboxTestTeardown()

	instance, err := blueboxProvider.Start(context.TODO(), &StartAttributes{Language: "clojure", Group: ""})
	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "block-id" {
		t.Errorf("expected 'block-id', got '%s'", instance.ID())
	}
}

func TestBlueBoxStartWithInvalidGroup(t *testing.T) {
	blueboxTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"CUSTOMER_ID":          "customer_id",
		"API_KEY":              "api_key",
		"LOCATION_ID":          "location_id",
		"PRODUCT_ID":           "product_id",
		"IPV6_ONLY":            "true",
		"LANGUAGE_MAP_CLOJURE": "jvm",
	}))
	defer blueboxTestTeardown()

	instance, err := blueboxProvider.Start(context.TODO(), &StartAttributes{Language: "clojure", Group: "dev"})
	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "block-id" {
		t.Errorf("expected 'block-id', got '%s'", instance.ID())
	}
}
