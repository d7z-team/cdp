package cdp_test

import (
	"context"
	"fmt"
	"net/http"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/mcpserver"
	"gopkg.d7z.net/cdp/snapshot"
)

func ExampleLaunch() {
	ctx := context.Background()
	browser, err := cdp.Launch(ctx, cdp.LaunchOptions{})
	if err != nil {
		panic(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx)
	if err != nil {
		panic(err)
	}
	if err = page.Navigate(ctx, "https://example.com", cdp.NavigateOptions{}); err != nil {
		panic(err)
	}
	document, err := page.Snapshot(ctx)
	if err != nil {
		panic(err)
	}
	rendered, err := snapshot.Render(document, snapshot.RenderOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(rendered.Markdown)
}

func ExampleConnect() {
	ctx := context.Background()
	browser, err := cdp.Connect(ctx, "http://127.0.0.1:9222", cdp.ConnectOptions{
		Initialize: func(init *cdp.Initializer) error {
			return init.RegisterBinding(cdp.Binding{Name: "report", World: cdp.WorldCore, Handle: func(ctx context.Context, call cdp.BindingCall) error {
				fmt.Println(call.Page.ID(), call.Payload)
				return nil
			}})
		},
	})
	if err != nil {
		panic(err)
	}
	defer browser.Close()
	server, err := mcpserver.New(browser, mcpserver.Options{MaxTabs: 10})
	if err != nil {
		panic(err)
	}
	defer server.Close()
	httpServer := &http.Server{Addr: "127.0.0.1:3000", Handler: server.Handler()}
	_ = httpServer // Caller owns ListenAndServe and Shutdown.
}

func ExampleBrowser_Diagnostics() {
	browser, err := cdp.Launch(context.Background(), cdp.LaunchOptions{Diagnostics: cdp.DiagnosticsRuntime})
	if err != nil {
		panic(err)
	}
	defer browser.Close()
	fmt.Println(browser.Diagnostics())
}
