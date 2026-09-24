package cdp

import (
	"context"
	"encoding/json"
	"time"
)

type Cookie struct {
	Name, Value, Domain, Path, SameSite, Priority, SourceScheme string
	URL                                                         string
	Size                                                        int
	Secure, HTTPOnly, Session, SameParty                        bool
	Expires                                                     time.Time
	SourcePort                                                  int
	PartitionKey                                                json.RawMessage
	PartitionKeyOpaque                                          bool
}

func (p *Page) Cookies(ctx context.Context) ([]Cookie, error) {
	var r struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := p.Session().Call(ctx, "Network.getCookies", nil, &r); err != nil {
		return nil, err
	}
	out := make([]Cookie, 0, len(r.Cookies))
	for _, raw := range r.Cookies {
		var v struct {
			Name, Value, Domain, Path, SameSite, Priority, SourceScheme string
			URL                                                         string
			Size                                                        int
			Secure, HTTPOnly, Session, SameParty                        bool
			Expires                                                     float64
			SourcePort                                                  int
			PartitionKey                                                json.RawMessage
			PartitionKeyOpaque                                          bool
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		c := Cookie{Name: v.Name, Value: v.Value, Domain: v.Domain, Path: v.Path, SameSite: v.SameSite, Priority: v.Priority, SourceScheme: v.SourceScheme, Secure: v.Secure, HTTPOnly: v.HTTPOnly, Session: v.Session, SameParty: v.SameParty, SourcePort: v.SourcePort, PartitionKey: v.PartitionKey, PartitionKeyOpaque: v.PartitionKeyOpaque, Size: v.Size}
		if v.Expires > 0 {
			c.Expires = time.UnixMilli(int64(v.Expires * 1000))
		}
		out = append(out, c)
	}
	return out, nil
}
func (p *Page) SetCookies(ctx context.Context, cookies []Cookie) error {
	raw := make([]map[string]any, 0, len(cookies))
	for _, v := range cookies {
		c := map[string]any{"name": v.Name, "value": v.Value, "domain": v.Domain, "path": v.Path, "secure": v.Secure, "httpOnly": v.HTTPOnly}
		if v.URL != "" {
			c["url"] = v.URL
			if v.Domain == "" {
				delete(c, "domain")
			}
			if v.Path == "" {
				delete(c, "path")
			}
		}
		if !v.Session && !v.Expires.IsZero() {
			c["expires"] = float64(v.Expires.UnixMilli()) / 1000
		}
		if v.SameSite != "" {
			c["sameSite"] = v.SameSite
		}
		if v.Priority != "" {
			c["priority"] = v.Priority
		}
		if v.SourceScheme != "" {
			c["sourceScheme"] = v.SourceScheme
		}
		if v.SourcePort != 0 {
			c["sourcePort"] = v.SourcePort
		}
		if len(v.PartitionKey) > 0 {
			c["partitionKey"] = v.PartitionKey
		}
		if v.SameParty {
			c["sameParty"] = true
		}
		raw = append(raw, c)
	}
	return p.Session().Call(ctx, "Network.setCookies", map[string]any{"cookies": raw}, nil)
}
func (b *Browser) ClearCookies(ctx context.Context) error {
	pages, err := b.Pages(ctx)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		p, err := b.NewPage(ctx)
		if err != nil {
			return err
		}
		defer p.Close(context.WithoutCancel(ctx))
		pages = append(pages, p)
	}
	return pages[0].Session().Call(ctx, "Network.clearBrowserCookies", nil, nil)
}
func (p *Page) ClearStorage(ctx context.Context, origin string) error {
	return p.Session().Call(ctx, "Storage.clearDataForOrigin", map[string]any{"origin": origin, "storageTypes": "all"}, nil)
}
