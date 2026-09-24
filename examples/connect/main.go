// Command connect lists pages in an existing browser without taking process ownership.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"gopkg.d7z.net/cdp"
)

func main() {
	endpoint := flag.String("endpoint", "http://127.0.0.1:9222", "existing CDP endpoint")
	flag.Parse()
	if err := run(context.Background(), *endpoint); err != nil {
		log.Fatal(err)
	}
}
func run(ctx context.Context, endpoint string) error {
	browser, err := cdp.Connect(ctx, endpoint, cdp.ConnectOptions{})
	if err != nil {
		return err
	}
	defer browser.Close()
	pages, err := browser.Pages(ctx)
	if err != nil {
		return err
	}
	for _, page := range pages {
		info, err := page.Info(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", page.ID(), info.Title, info.URL)
	}
	return nil
}
