// Command automation captures a page using the public CDP library.
package main

import (
	"context"
	"errors"
	"flag"
	"image/png"
	"log"
	"os"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/snapshot"
)

func main() {
	url := flag.String("url", "https://example.com", "page URL")
	output := flag.String("out", "screenshot.png", "PNG output path")
	flag.Parse()
	if err := run(context.Background(), *url, *output); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, url, output string) (err error) {
	browser, err := cdp.Launch(ctx, cdp.LaunchOptions{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, browser.Close()) }()
	page, err := browser.NewPage(ctx)
	if err != nil {
		return err
	}
	if err = page.Navigate(ctx, url, cdp.NavigateOptions{}); err != nil {
		return err
	}
	document, err := page.Snapshot(ctx)
	if err != nil {
		return err
	}
	rendered, err := snapshot.Render(document, snapshot.RenderOptions{})
	if err != nil {
		return err
	}
	log.Print(rendered.Markdown)
	img, err := page.Screenshot(ctx)
	if err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(file, img), file.Close())
}
