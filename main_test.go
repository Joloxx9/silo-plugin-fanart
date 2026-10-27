package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

const movieJSON = `{
 "name": "The Matrix", "tmdb_id": "603", "imdb_id": "tt0133093",
 "movieposter": [
  {"id": "1", "url": "https://assets.fanart.tv/fanart/the-matrix-en-low.jpg", "lang": "en", "likes": "1"},
  {"id": "2", "url": "https://assets.fanart.tv/fanart/the-matrix-en-high.jpg", "lang": "en", "likes": "9"},
  {"id": "3", "url": "https://assets.fanart.tv/fanart/the-matrix-pl.jpg", "lang": "pl", "likes": "0"},
  {"id": "4", "url": "https://assets.fanart.tv/fanart/movies/603/movieposter/plain.jpg", "lang": "00", "likes": "50"}
 ],
 "moviebackground": [
  {"id": "5", "url": "https://assets.fanart.tv/fanart/movies/603/moviebackground/text.jpg", "lang": "en", "likes": "20"},
  {"id": "6", "url": "https://assets.fanart.tv/fanart/movies/603/moviebackground/clean.jpg", "lang": "", "likes": 2}
 ],
 "movielogo": [{"id": "7", "url": "https://assets.fanart.tv/fanart/movies/603/movielogo/old.png", "lang": "en", "likes": "30"}],
 "hdmovielogo": [{"id": "8", "url": "https://assets.fanart.tv/fanart/the-matrix-hd.png", "lang": "en", "likes": "3"}],
 "moviedisc": [{"id": "9", "url": "https://assets.fanart.tv/fanart/movies/603/moviedisc/disc.png", "lang": "en", "likes": "1", "disc": "1", "disc_type": "bluray"}],
 "moviethumb": [{"id": "10", "url": "https://evil.example/fanart/movies/603/moviethumb/x.jpg", "lang": "en", "likes": "1"}]
}`

const tvJSON = `{
 "name": "Breaking Bad", "thetvdb_id": "81189",
 "tvposter": [{"id": "1", "url": "https://assets.fanart.tv/fanart/tv/81189/tvposter/p.jpg", "lang": "en", "likes": "4"}],
 "seasonposter": [
  {"id": "2", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/s1.jpg", "lang": "en", "likes": "1", "season": "1"},
  {"id": "3", "url": "https://assets.fanart.tv/fanart/B_81189 (1).jpg", "lang": "en", "likes": "7", "season": "1"},
  {"id": "4", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/s0.jpg", "lang": "en", "likes": "1", "season": "0"},
  {"id": "5", "url": "https://assets.fanart.tv/fanart/tv/81189/seasonposter/all.jpg", "lang": "en", "likes": "1", "season": "all"}
 ]
}`

func testServer(t *testing.T, calls *atomic.Int32) *server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("api_key") != "mykey" || r.URL.Query().Has("client_key") {
			t.Errorf("missing keys in %s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/movies/603", "/movies/tt0133093":
			_, _ = w.Write([]byte(movieJSON))
		case "/tv/81189":
			_, _ = w.Write([]byte(tvJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error message":"Not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	s := &server{p: newProvider()}
	s.p.endpoint = srv.URL + "/"
	configure(t, s, map[string]any{"api_key": "mykey"})
	return s
}

func configure(t *testing.T, s *server, values map[string]any) {
	t.Helper()
	cfg, err := structpb.NewStruct(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: cfg}}}); err != nil {
		t.Fatal(err)
	}
}

func providerIDs(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	st, err := structpb.NewStruct(values)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func urls(images []*pluginv1.ImageRecord) map[string][]string {
	out := map[string][]string{}
	for _, img := range images {
		out[img.GetKind()] = append(out[img.GetKind()], strings.TrimPrefix(img.GetUrl(), "fanart://"))
	}
	return out
}

func TestManifestLoads(t *testing.T) {
	m, err := manifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if m.GetPluginId() != "community.fanart" || len(m.GetCapabilities()) != 2 {
		t.Fatalf("manifest = %v", m)
	}
}

func TestConfigureValidation(t *testing.T) {
	for _, bad := range []map[string]any{
		{},
		{"api_key": " "},
		{"api_key": "x", "image_selection": "Some"},
		{"api_key": "x", "artwork_types": []any{"poster", "characterart"}},
		{"api_key": "x", "cache_hours": 1.5},
		{"api_key": "x", "cache_hours": 500},
		{"api_key": "x", "language": "polish"},
		{"api_key": "x", "language": "00"},
		{"api_key": "x", "fallback_language": "PL"},
	} {
		if _, err := parseSettings(structFromMap(bad).AsMap()); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if cfg, err := parseSettings(map[string]any{"client_key": " old "}); err != nil || cfg.APIKey != "old" {
		t.Fatalf("legacy client_key: %+v, %v", cfg, err)
	}
	cfg, err := parseSettings(map[string]any{"api_key": "x", "artwork_types": []any{"logo"}, "cache_hours": float64(0)})
	if err != nil || !cfg.Types["logo"] || cfg.Types["poster"] || cfg.CacheHours != 0 || cfg.Selection != "BestOnly" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}

func TestMovieImagesBestOnly(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	configure(t, s, map[string]any{"api_key": "mykey", "language": "pl"})
	r := &pluginv1.GetImagesRequest{ItemType: "movie", Language: "de", ProviderIds: providerIDs(t, map[string]any{"tmdb": "603"})}
	got, err := s.GetImages(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	u := urls(got.GetImages())
	want := map[string]string{
		"poster":   "the-matrix-pl.jpg",
		"backdrop": "movies/603/moviebackground/clean.jpg",
		"logo":     "the-matrix-hd.png",
		"disc":     "movies/603/moviedisc/disc.png",
	}
	for kind, path := range want {
		if len(u[kind]) != 1 || u[kind][0] != path {
			t.Errorf("%s = %v, want %s", kind, u[kind], path)
		}
	}
	if len(u["thumb"]) != 0 {
		t.Errorf("foreign asset host accepted: %v", u["thumb"])
	}
	if _, err := s.GetImages(context.Background(), r); err != nil || calls.Load() != 1 {
		t.Fatalf("cache missed: calls=%d err=%v", calls.Load(), err)
	}
}

func TestMovieGalleryAndLanguageOrder(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery"))
	got, err := s.GetImages(ctx, &pluginv1.GetImagesRequest{ItemType: "movie", Language: "de", ProviderIds: providerIDs(t, map[string]any{"imdb": "tt0133093"})})
	if err != nil {
		t.Fatal(err)
	}
	posters := urls(got.GetImages())["poster"]
	// Default language and fallback are English; Polish is never supplied.
	want := []string{"the-matrix-en-high.jpg", "the-matrix-en-low.jpg", "movies/603/movieposter/plain.jpg"}
	if strings.Join(posters, ",") != strings.Join(want, ",") {
		t.Fatalf("posters = %v", posters)
	}
	for _, img := range got.GetImages() {
		if img.GetLanguage() != "" && !img.GetMetadata().GetFields()["includes_text"].GetBoolValue() {
			t.Errorf("text flag missing on %s", img.GetUrl())
		}
	}
}

func TestArtworkTypesFilter(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	configure(t, s, map[string]any{"api_key": "mykey", "artwork_types": []any{"logo"}})
	got, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: providerIDs(t, map[string]any{"tmdb": "603"})})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetImages()) != 1 || got.GetImages()[0].GetKind() != "logo" {
		t.Fatalf("images = %v", got.GetImages())
	}
}

func TestSeasonsAndSeasonImages(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	ids := providerIDs(t, map[string]any{"tvdb": "81189", "tmdb": "1396"})
	seasons, err := s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{ProviderIds: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons.GetSeasons()) != 2 || seasons.GetSeasons()[0].GetSeasonNumber() != 0 || seasons.GetSeasons()[1].GetPosterPath() != "fanart://B_81189%20%281%29.jpg" {
		t.Fatalf("seasons = %v", seasons.GetSeasons())
	}
	one := int32(1)
	got, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", SeasonNumber: &one, ProviderIds: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetImages()) != 1 || got.GetImages()[0].GetKind() != "poster" || got.GetImages()[0].GetSeasonNumber() != 1 || got.GetImages()[0].SeasonNumber == nil {
		t.Fatalf("season images = %v", got.GetImages())
	}
	episodes, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{ProviderIds: ids, SeasonNumber: 1})
	if err != nil || len(episodes.GetEpisodes()) != 0 {
		t.Fatalf("episodes = %v, %v", episodes, err)
	}
}

func TestNotFoundIsEmpty(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	got, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: providerIDs(t, map[string]any{"tmdb": "1"})})
	if err != nil || len(got.GetImages()) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestIdentity(t *testing.T) {
	cases := []struct {
		typ, fallback string
		ids           map[string]any
		want          string
	}{
		{"movie", "", map[string]any{"tmdb": "603", "imdb": "tt0133093"}, "603"},
		{"movie", "", map[string]any{"imdb": "tt0133093"}, "tt0133093"},
		{"movie", "", map[string]any{"tmdb": "../x"}, ""},
		{"movie", "movie:42", map[string]any{"tmdb": "603"}, "42"},
		{"movie", "", map[string]any{"fanart": "movie:tt0133093", "tmdb": "603"}, "tt0133093"},
		{"movie", "", map[string]any{"fanart": "series:5", "tmdb": "603"}, ""},
		{"series", "", map[string]any{"tmdb": "1396"}, ""},
		{"series", "", map[string]any{"tvdb": "81189"}, "81189"},
		{"series", "", map[string]any{"tvdb": "tt1"}, ""},
		{"episode", "", map[string]any{"tvdb": "1"}, ""},
	}
	for _, c := range cases {
		if _, got := identity(c.typ, providerIDs(t, c.ids), c.fallback); got != c.want {
			t.Errorf("identity(%s, %v, %q) = %q, want %q", c.typ, c.ids, c.fallback, got, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	if got := resolve("fanart://movies/603/hdmovielogo/the-matrix-5.png"); got != "https://assets.fanart.tv/fanart/movies/603/hdmovielogo/the-matrix-5.png" {
		t.Fatalf("resolve = %q", got)
	}
	if got := resolve("fanart://B_81189%20%281%29.png"); got != "https://assets.fanart.tv/fanart/B_81189%20%281%29.png" {
		t.Fatalf("resolve flat = %q", got)
	}
	for _, bad := range []string{"https://evil.example/x.jpg", "fanart://movies/603/../../x.jpg", "fanart://music/1/x/a.jpg", "fanart://movies/603/movieposter/a.gif", "fanart://..%2F..%2Fx.jpg", "fanart://.hidden.jpg", "mediux://x"} {
		if got := resolve(bad); got != "" {
			t.Errorf("resolve(%q) = %q", bad, got)
		}
	}
}

func TestErrorsNeverLeakKeys(t *testing.T) {
	p := newProvider()
	p.endpoint = "http://127.0.0.1:1/"
	p.configure(settings{APIKey: "my-secret", Types: defaultSettings().Types})
	_, err := p.fetch(context.Background(), "movie", "603")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "127.0.0.1:1/movies") {
		t.Fatalf("err = %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	p.endpoint = srv.URL + "/"
	if _, err := p.fetch(context.Background(), "movie", "603"); err == nil || err.Error() != "Fanart.tv API status 401" {
		t.Fatalf("err = %v", err)
	}
}

func TestLanguageNormalization(t *testing.T) {
	for in, want := range map[string]string{"00": "", "": "", "xx": "", "EN": "en", "pt-BR": "pt", "cz": "cs", "ua": "uk", "xyz": "", "1a": ""} {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanguageWithoutFallback(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, &calls)
	configure(t, s, map[string]any{"api_key": "mykey", "language": "pl", "fallback_language": "none", "image_selection": "All"})
	got, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: providerIDs(t, map[string]any{"tmdb": "603"})})
	if err != nil {
		t.Fatal(err)
	}
	u := urls(got.GetImages())
	if strings.Join(u["poster"], ",") != "the-matrix-pl.jpg,movies/603/movieposter/plain.jpg" || len(u["logo"]) != 0 || len(u["backdrop"]) != 1 {
		t.Fatalf("images = %v", u)
	}
	cfg := s.p.snapshot()
	if cfg.Language != "pl" || cfg.Fallback != "pl" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
