package main

import (
	"bytes"
	"context"
	"html/template"
	"sort"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

var statusPage = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fanart.tv status</title>
<style>
:root{color-scheme:light dark;--bg:#fff;--fg:#1a1a1a;--muted:#666;--ok:#1f7a3a;--bad:#b42318;--line:#ddd}
@media (prefers-color-scheme:dark){:root{--bg:#121212;--fg:#eee;--muted:#aaa;--ok:#5fd38a;--bad:#ff8a80;--line:#333}}
body{margin:0;padding:24px 16px;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif}
main{max-width:560px;margin:0 auto}h1{font-size:20px;margin:0 0 16px}
dl{display:grid;grid-template-columns:auto 1fr;gap:8px 16px;margin:0;padding:16px;border:1px solid var(--line);border-radius:8px}
dt{color:var(--muted)}dd{margin:0}.ok{color:var(--ok)}.bad{color:var(--bad)}p{color:var(--muted);font-size:13px}
</style></head><body><main>
<h1>Fanart.tv status</h1>
<dl>
<dt>API key</dt><dd>{{if .KeySaved}}<span class="ok">Saved</span>{{else}}<span class="bad">Not saved</span>{{end}}</dd>
<dt>Fanart.tv</dt><dd>{{if .CheckOK}}<span class="ok">Key accepted</span>{{else}}<span class="bad">{{.CheckError}}</span>{{end}}</dd>
<dt>Used for</dt><dd>{{.Media}}</dd>
<dt>Artwork types</dt><dd>{{.Types}}</dd>
</dl>
<p>To change the key, type a new one in the plugin settings. Leaving the field empty keeps the saved key.</p>
</main></body></html>`))

type statusView struct {
	KeySaved, CheckOK bool
	CheckError        string
	Media, Types      string
}

func joinKeys(m map[string]bool, labels map[string]string) string {
	var out []string
	for k, on := range m {
		if on {
			out = append(out, labels[k])
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "None"
	}
	return strings.Join(out, ", ")
}

var mediaLabels = map[string]string{"movie": "Movies", "series": "Series and seasons"}
var typeLabels = map[string]string{"poster": "Posters", "backdrop": "Backgrounds", "logo": "Logos", "clearart": "Clear art", "banner": "Banners", "thumb": "Thumbs", "disc": "Discs", "season": "Season posters"}

// checkKey asks Fanart.tv for a known title. It bypasses the cache so a
// rejected key shows up immediately.
func (p *provider) checkKey(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := p.fetch(ctx, "movie", "603")
	return err
}

func (s *server) Handle(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	respond := func(status int, body []byte, contentType string) *pluginv1.HandleHTTPResponse {
		return &pluginv1.HandleHTTPResponse{StatusCode: int32(status), Body: body, Headers: map[string]string{"Content-Type": contentType, "Cache-Control": "no-store"}}
	}
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return respond(403, []byte("Administrator access required"), "text/plain; charset=utf-8"), nil
	}
	if r.GetMethod() != "GET" || r.GetPath() != "/status" {
		return respond(404, []byte("Not found"), "text/plain; charset=utf-8"), nil
	}
	cfg := s.p.snapshot()
	view := statusView{KeySaved: cfg.APIKey != "", Media: joinKeys(cfg.Media, mediaLabels), Types: joinKeys(cfg.Types, typeLabels)}
	if !view.KeySaved {
		view.CheckError = "Not checked"
	} else if err := s.p.checkKey(ctx); err != nil {
		view.CheckError = err.Error()
	} else {
		view.CheckOK = true
	}
	var body bytes.Buffer
	if err := statusPage.Execute(&body, view); err != nil {
		return nil, err
	}
	return respond(200, body.Bytes(), "text/html; charset=utf-8"), nil
}
