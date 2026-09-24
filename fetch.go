package cdp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type FetchRequest struct {
	URL, Method string
	Headers     http.Header
	Body        []byte
}
type FetchResponse struct {
	URL        string
	Status     int
	StatusText string
	Headers    http.Header
	Body       []byte
}

func (r FetchResponse) JSON(result any) error { return json.Unmarshal(r.Body, result) }
func (p *Page) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	c, cancel, err := p.operation(ctx, p.timeouts().Read)
	if err != nil {
		return FetchResponse{}, err
	}
	defer cancel()
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return FetchResponse{}, err
	}
	key := "fetch_" + hex.EncodeToString(id)
	headers := [][2]string{}
	for k, vs := range req.Headers {
		for _, v := range vs {
			headers = append(headers, [2]string{k, v})
		}
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	payload, _ := json.Marshal(map[string]any{"url": req.URL, "method": req.Method, "headers": headers, "body": base64.StdEncoding.EncodeToString(req.Body)})
	deadline, _ := c.Deadline()
	script := fmt.Sprintf(`const key=%s, req=%s; const controller=new AbortController();Object.defineProperty(window,key,{value:controller,configurable:true});const timer=setTimeout(()=>controller.abort(),%d);try{const init={method:req.method,headers:req.headers,credentials:'include',signal:controller.signal};if(req.body)init.body=Uint8Array.from(atob(req.body),c=>c.charCodeAt(0));const response=await fetch(req.url,init);const bytes=new Uint8Array(await response.arrayBuffer());let binary='';for(let i=0;i<bytes.length;i+=32768)binary+=String.fromCharCode(...bytes.subarray(i,i+32768));return {url:response.url,status:response.status,statusText:response.statusText,headers:Array.from(response.headers.entries()),body:btoa(binary)};}finally{clearTimeout(timer);delete window[key];}`, quote(key), payload, max(1, time.Until(deadline).Milliseconds()))
	var result struct {
		URL        string
		Status     int
		StatusText string
		Headers    [][2]string
		Body       string
	}
	err = p.Eval(c, script, &result)
	if c.Err() != nil {
		cleanup, stop := context.WithTimeout(context.Background(), p.timeouts().Shutdown)
		defer stop()
		_ = p.Eval(cleanup, "window["+quote(key)+"]?.abort(); delete window["+quote(key)+"];", nil)
	}
	if err != nil {
		return FetchResponse{}, operationError("fetch", err)
	}
	body, err := base64.StdEncoding.DecodeString(result.Body)
	out := FetchResponse{URL: result.URL, Status: result.Status, StatusText: result.StatusText, Headers: make(http.Header), Body: body}
	for _, h := range result.Headers {
		out.Headers.Add(h[0], h[1])
	}
	return out, operationError("fetch.decode", err)
}
