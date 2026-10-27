package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://webservice.fanart.tv/v3/"
const assetHost = "https://assets.fanart.tv/fanart/"
const maxResponse = 4 << 20

var imdbID = regexp.MustCompile(`^tt[0-9]{5,10}$`)

// assetPath matches a decoded asset path below /fanart/: current flat names
// ("the-matrix-53b1a283180a1.jpg", "B_81189 (1).png") and the older
// "movies/603/movieposter/<name>" layout.
var assetPath = regexp.MustCompile(`^([a-z]{2,10}/[0-9]{1,12}/[a-z]{3,20}/)?[A-Za-z0-9_()-][A-Za-z0-9._() -]{0,199}\.(jpg|jpeg|png)$`)

// itemKinds are the movie and series image kinds, in the order they are returned.
var itemKinds = []string{"poster", "backdrop", "logo", "clearart", "banner", "thumb", "disc"}
var allKinds = append(append([]string{}, itemKinds...), "season")

type fanartKind struct {
	Kind string
	HD   bool
}

// Fanart.tv response keys mapped to Silo image kinds. HD variants of logos and
// clear art rank ahead of the older low-resolution ones.
var movieKinds = map[string]fanartKind{
	"movieposter":     {"poster", false},
	"moviebackground": {"backdrop", false},
	"hdmovielogo":     {"logo", true},
	"movielogo":       {"logo", false},
	"hdmovieclearart": {"clearart", true},
	"movieart":        {"clearart", false},
	"moviebanner":     {"banner", false},
	"moviethumb":      {"thumb", false},
	"moviedisc":       {"disc", false},
}
var tvKinds = map[string]fanartKind{
	"tvposter":       {"poster", false},
	"showbackground": {"backdrop", false},
	"hdtvlogo":       {"logo", true},
	"clearlogo":      {"logo", false},
	"hdclearart":     {"clearart", true},
	"clearart":       {"clearart", false},
	"tvbanner":       {"banner", false},
	"tvthumb":        {"thumb", false},
	"seasonposter":   {"season", false},
}

type settings struct {
	APIKey, ClientKey, Selection string
	Types                        map[string]bool
	CacheHours                   int
}

func (s settings) ttl() time.Duration {
	if s.CacheHours <= 0 {
		return 0
	}
	return time.Duration(s.CacheHours) * time.Hour
}

func defaultSettings() settings {
	types := map[string]bool{}
	for _, k := range allKinds {
		types[k] = true
	}
	return settings{Selection: "BestOnly", Types: types, CacheHours: 24}
}

func parseSettings(values map[string]any) (settings, error) {
	s := defaultSettings()
	s.APIKey, _ = values["api_key"].(string)
	s.APIKey = strings.TrimSpace(s.APIKey)
	if s.APIKey == "" {
		return settings{}, errors.New("api_key is required")
	}
	s.ClientKey, _ = values["client_key"].(string)
	s.ClientKey = strings.TrimSpace(s.ClientKey)
	if v, exists := values["image_selection"]; exists && v != "" {
		if mode, ok := v.(string); !ok || (mode != "BestOnly" && mode != "All") {
			return settings{}, errors.New("image_selection must be BestOnly or All")
		}
		s.Selection = v.(string)
	}
	if v, exists := values["artwork_types"]; exists && v != nil {
		list, ok := v.([]any)
		if !ok {
			return settings{}, errors.New("artwork_types must be a list")
		}
		s.Types = map[string]bool{}
		for _, x := range list {
			kind, _ := x.(string)
			if !validChoice(kind, allKinds...) {
				return settings{}, fmt.Errorf("unknown artwork type %q", kind)
			}
			s.Types[kind] = true
		}
	}
	if v, exists := values["cache_hours"]; exists && v != nil {
		hours, ok := v.(float64)
		if !ok || hours < 0 || hours > 168 || hours != float64(int(hours)) {
			return settings{}, errors.New("cache_hours must be an integer from 0 through 168")
		}
		s.CacheHours = int(hours)
	}
	return s, nil
}

func validChoice(value string, choices ...string) bool {
	for _, c := range choices {
		if value == c {
			return true
		}
	}
	return false
}

type artwork struct {
	Path, Kind, Language string
	Season, Likes        int
	HD                   bool
}
type bundle struct{ Art []artwork }
type cacheEntry struct {
	b     bundle
	until time.Time
}
type call struct {
	done chan struct{}
	b    bundle
	err  error
}
type provider struct {
	mu         sync.Mutex
	cfg        settings
	cache      map[string]cacheEntry
	flight     map[string]*call
	generation uint64
	endpoint   string
	client     *http.Client
	sem        chan struct{}
}

func newProvider() *provider {
	return &provider{cfg: defaultSettings(), cache: map[string]cacheEntry{}, flight: map[string]*call{}, sem: make(chan struct{}, 4), endpoint: apiBase, client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (p *provider) configure(cfg settings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	p.cache = map[string]cacheEntry{}
	p.generation++
}

func (p *provider) snapshot() settings { p.mu.Lock(); defer p.mu.Unlock(); return p.cfg }

// get coalesces duplicate in-flight lookups and caches results per configuration generation.
func (p *provider) get(ctx context.Context, typ, id string) (bundle, error) {
	p.mu.Lock()
	key := fmt.Sprintf("%d:%s:%s", p.generation, typ, id)
	if e, ok := p.cache[key]; ok && time.Now().Before(e.until) {
		p.mu.Unlock()
		return e.b, nil
	}
	if c := p.flight[key]; c != nil {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.b, c.err
		case <-ctx.Done():
			return bundle{}, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	gen := p.generation
	p.flight[key] = c
	p.mu.Unlock()
	b, err := p.fetch(ctx, typ, id)
	p.mu.Lock()
	if err == nil && gen == p.generation && p.cfg.CacheHours != 0 {
		if len(p.cache) >= 256 {
			var oldest string
			var until time.Time
			for k, v := range p.cache {
				if oldest == "" || v.until.Before(until) {
					oldest = k
					until = v.until
				}
			}
			delete(p.cache, oldest)
		}
		ttl := p.cfg.ttl()
		if len(b.Art) == 0 && ttl > 5*time.Minute {
			ttl = 5 * time.Minute
		}
		p.cache[key] = cacheEntry{b, time.Now().Add(ttl)}
	}
	delete(p.flight, key)
	c.b, c.err = b, err
	close(c.done)
	p.mu.Unlock()
	return b, err
}

func (p *provider) fetch(ctx context.Context, typ, id string) (bundle, error) {
	cfg := p.snapshot()
	if cfg.APIKey == "" {
		return bundle{}, errors.New("Fanart.tv API key is not configured")
	}
	section := "movies/"
	if typ == "series" {
		section = "tv/"
	}
	q := url.Values{"api_key": {cfg.APIKey}}
	if cfg.ClientKey != "" {
		q.Set("client_key", cfg.ClientKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+section+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		return bundle{}, errors.New("Fanart.tv request could not be built")
	}
	req.Header.Set("Accept", "application/json")
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return bundle{}, ctx.Err()
	}
	defer func() { <-p.sem }()
	res, err := p.client.Do(req)
	if err != nil {
		// url.Error repeats the request URL, which carries the API keys.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return bundle{}, fmt.Errorf("Fanart.tv request failed: %s", redact(err.Error(), cfg))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return bundle{}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return bundle{}, fmt.Errorf("Fanart.tv response could not be read: %s", redact(err.Error(), cfg))
	}
	if len(raw) > maxResponse {
		return bundle{}, errors.New("Fanart.tv response exceeds size limit")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return bundle{}, fmt.Errorf("Fanart.tv API status %d", res.StatusCode)
	}
	return parseBundle(raw, typ)
}

func redact(s string, cfg settings) string {
	for _, secret := range []string{cfg.APIKey, cfg.ClientKey} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
			s = strings.ReplaceAll(s, url.QueryEscape(secret), "[redacted]")
		}
	}
	return s
}

// flexString accepts Fanart.tv fields that are sometimes strings and sometimes numbers.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return nil
	}
	*f = flexString(n.String())
	return nil
}

type rawImage struct {
	URL    string     `json:"url"`
	Lang   flexString `json:"lang"`
	Likes  flexString `json:"likes"`
	Season flexString `json:"season"`
}

func parseBundle(raw []byte, typ string) (bundle, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return bundle{}, errors.New("Fanart.tv response is not valid JSON")
	}
	kinds := movieKinds
	if typ == "series" {
		kinds = tvKinds
	}
	b := bundle{}
	seen := map[string]bool{}
	for key, kind := range kinds {
		var images []rawImage
		if json.Unmarshal(root[key], &images) != nil {
			continue
		}
		for _, img := range images {
			path, ok := assetPathFromURL(img.URL)
			if !ok || seen[path] {
				continue
			}
			a := artwork{Path: path, Kind: kind.Kind, HD: kind.HD, Language: normalizeLanguage(string(img.Lang))}
			a.Likes, _ = strconv.Atoi(string(img.Likes))
			if kind.Kind == "season" {
				n, err := strconv.Atoi(string(img.Season))
				if err != nil || n < 0 {
					continue // "all" or malformed seasons cannot be attributed.
				}
				a.Season = n
			}
			seen[path] = true
			b.Art = append(b.Art, a)
		}
	}
	sort.Slice(b.Art, func(i, j int) bool { return b.Art[i].Path < b.Art[j].Path })
	return b, nil
}

func assetPathFromURL(raw string) (string, bool) {
	// Some current asset names contain unescaped spaces.
	u, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(raw), " ", "%20"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != "assets.fanart.tv" {
		return "", false
	}
	path, ok := strings.CutPrefix(u.Path, "/fanart/")
	if !ok || !assetPath.MatchString(path) {
		return "", false
	}
	return path, true
}

// normalizeLanguage maps Fanart.tv codes to Silo's: "00", "xx" and "" mean
// no text, and Fanart.tv's "cz" and "ua" are Czech and Ukrainian.
func normalizeLanguage(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if base, _, ok := strings.Cut(code, "-"); ok {
		code = base
	}
	switch code {
	case "cz":
		return "cs"
	case "ua":
		return "uk"
	}
	if len(code) != 2 || code == "00" || code == "xx" {
		return ""
	}
	for _, r := range code {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return code
}

// languageTier orders artwork by language. Posters prefer the requested
// language, then English, then other text, then textless art. Backgrounds
// prefer textless art. Logos and other text art prefer textless over a
// foreign language.
func languageTier(kind, lang, requested string) int {
	switch {
	case kind == "backdrop":
		if lang == "" {
			return 0
		}
		if lang == requested {
			return 1
		}
		if lang == "en" {
			return 2
		}
		return 3
	case lang != "" && lang == requested:
		return 0
	case lang == "en":
		return 1
	case kind == "poster" || kind == "season":
		if lang != "" {
			return 2
		}
		return 3
	case lang == "":
		return 2
	default:
		return 3
	}
}

// rank returns every image of one kind (and season), best first: language
// tier, then HD, then Fanart.tv likes.
func rank(b bundle, kind string, season int, requested string) []artwork {
	requested = normalizeLanguage(requested)
	var out []artwork
	for _, a := range b.Art {
		if a.Kind == kind && (kind != "season" || a.Season == season) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := languageTier(kind, out[i].Language, requested), languageTier(kind, out[j].Language, requested)
		if ti != tj {
			return ti < tj
		}
		if out[i].HD != out[j].HD {
			return out[i].HD
		}
		if out[i].Likes != out[j].Likes {
			return out[i].Likes > out[j].Likes
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// escapePath percent-encodes each segment of a decoded asset path.
func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func canonical(a artwork) string { return "fanart://" + escapePath(a.Path) }

// resolve maps fanart://<path> to the fixed Fanart.tv asset host.
func resolve(path string) string {
	rest, ok := strings.CutPrefix(path, "fanart://")
	if !ok {
		return ""
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil || !assetPath.MatchString(decoded) {
		return ""
	}
	return assetHost + escapePath(decoded)
}
