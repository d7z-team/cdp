package main

import (
	"strings"
	"testing"
)

func TestParseConfigPortAndAddress(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		port    int
		addr    string
		origins []string
		err     bool
	}{
		{name: "default", port: 3000, addr: "127.0.0.1:3000"},
		{name: "custom", args: []string{"--host", "0.0.0.0", "--port", "4317"}, port: 4317, addr: "0.0.0.0:4317"},
		{name: "maximum", args: []string{"--port", "65535"}, port: 65535, addr: "127.0.0.1:65535"},
		{name: "ipv6", args: []string{"--host", "::1", "--port", "3100"}, port: 3100, addr: "[::1]:3100"},
		{name: "negative", args: []string{"--port", "-1"}, err: true},
		{name: "zero", args: []string{"--port", "0"}, err: true},
		{name: "too large", args: []string{"--port", "65536"}, err: true},
		{name: "invalid tabs", args: []string{"--max-tabs", "0"}, err: true},
		{name: "cors origins", args: []string{"--cors-origin", "HTTPS://EXAMPLE.COM:443", "--cors-origin", "http://localhost:5173", "--cors-origin", "https://example.com"}, port: 3000, addr: "127.0.0.1:3000", origins: []string{"https://example.com", "http://localhost:5173"}},
		{name: "cors wildcard", args: []string{"--cors-origin", "*"}, port: 3000, addr: "127.0.0.1:3000", origins: []string{"*"}},
		{name: "cors wildcard mixed", args: []string{"--cors-origin", "*", "--cors-origin", "https://example.com"}, err: true},
		{name: "cors null", args: []string{"--cors-origin", "null"}, err: true},
		{name: "cors path", args: []string{"--cors-origin", "https://example.com/"}, err: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := parseConfig(test.args)
			if test.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", config)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.port != test.port || config.listenAddress() != test.addr {
				t.Fatalf("config=%+v address=%q", config, config.listenAddress())
			}
			if strings.Join(config.httpOptions.AllowedOrigins, "|") != strings.Join(test.origins, "|") {
				t.Fatalf("origins=%v want=%v", config.httpOptions.AllowedOrigins, test.origins)
			}
		})
	}
}

func TestParseScreenConfiguration(t *testing.T) {
	config, err := parseConfig([]string{"-window-size", "1440,960", "-screen-size", "1920,1080", "-screen-scale", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if config.windowSize.Width != 1440 || config.windowSize.Height != 960 || config.screen.Width != 1920 || config.screen.Height != 1080 || config.screen.ScaleFactor != 2 {
		t.Fatalf("config=%+v", config)
	}
	for _, value := range []string{"800", "800,600extra", "0,600", "800,-1", "800,600,400"} {
		if _, err := parseConfig([]string{"-screen-size", value}); err == nil {
			t.Errorf("accepted invalid screen %q", value)
		}
	}
}
