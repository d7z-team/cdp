// Package mcpserver exposes the browser automation MCP HTTP service.
// A Server borrows its Browser. Closing it never closes the browser or its tabs.
package mcpserver

import (
	"errors"
	"net/http"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/internal/mcpapp"
)

type Options struct {
	AllowedOrigins []string
	MaxTabs        int
	EnableDebug    bool
}
type Server struct{ app *mcpapp.App }

func New(browser *cdp.Browser, opts Options) (*Server, error) {
	if browser == nil || !browser.Alive() {
		return nil, errors.New("MCP requires a connected browser")
	}
	if opts.MaxTabs < 0 {
		return nil, errors.New("negative tab limit")
	}
	app, err := mcpapp.New(browser, mcpapp.Config{AllowedOrigins: append([]string(nil), opts.AllowedOrigins...), MaxTabs: opts.MaxTabs, EnableDebug: opts.EnableDebug})
	if err != nil {
		return nil, err
	}
	return &Server{app: app}, nil
}
func (s *Server) Handler() http.Handler { return s.app }
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	return s.app.Close()
}
