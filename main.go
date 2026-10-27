package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"sort"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

var version string

const imageRequestPurposeHeader = "silo-image-request-purpose"

//go:embed manifest.json
var manifestJSON []byte

type server struct {
	runtimedefault.Server
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageResolverServer
	m *pluginv1.PluginManifest
	p *provider
}

func (s *server) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.m}, nil
}

func (s *server) Configure(_ context.Context, r *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	values := map[string]any{}
	for _, entry := range r.GetConfig() {
		if entry.GetKey() == "settings" && entry.GetValue() != nil {
			values = entry.GetValue().AsMap()
		}
	}
	cfg, err := parseSettings(values)
	if err != nil {
		return nil, err
	}
	s.p.configure(cfg)
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *server) Search(context.Context, *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	return &pluginv1.SearchMetadataResponse{}, nil
}

func ids(x *structpb.Struct) map[string]string {
	out := map[string]string{}
	if x != nil {
		for k, v := range x.AsMap() {
			if z, ok := v.(string); ok {
				out[k] = strings.TrimSpace(z)
			}
		}
	}
	return out
}

func structFromMap(values map[string]any) *structpb.Struct {
	value, err := structpb.NewStruct(values)
	if err != nil {
		panic(err)
	}
	return value
}

// identity returns the Fanart.tv lookup ID: a TMDB or IMDb ID for movies and
// a TVDB ID for series. Fanart.tv has no series lookup by TMDB ID.
func identity(typ string, x *structpb.Struct, fallback string) (string, string) {
	if typ != "series" && typ != "movie" {
		return typ, ""
	}
	m := ids(x)
	if own := m["fanart"]; own != "" {
		parts := strings.SplitN(own, ":", 2)
		if len(parts) == 2 && parts[0] == typ && validID(typ, parts[1]) {
			return typ, parts[1]
		}
		return typ, ""
	}
	if id, ok := strings.CutPrefix(fallback, typ+":"); ok && validID(typ, id) {
		return typ, id
	}
	keys := []string{"tvdb"}
	if typ == "movie" {
		keys = []string{"tmdb", "imdb"}
	}
	for _, key := range keys {
		if id := m[key]; validID(typ, id) {
			return typ, id
		}
	}
	return typ, ""
}

func validID(typ, id string) bool {
	if numericID(id) {
		return true
	}
	return typ == "movie" && imdbID.MatchString(id)
}

func numericID(s string) bool {
	if s == "" || len(s) > 12 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sourceKey(typ, id string) string {
	switch {
	case typ == "series":
		return "tvdb"
	case numericID(id):
		return "tmdb"
	default:
		return "imdb"
	}
}

func (s *server) GetMetadata(_ context.Context, r *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	typ, id := identity(r.GetItemType(), r.GetProviderIds(), r.GetProviderId())
	if id == "" || !s.p.snapshot().Media[typ] {
		return &pluginv1.GetMetadataResponse{}, nil
	}
	st := structFromMap(map[string]any{sourceKey(typ, id): id, "fanart": typ + ":" + id})
	return &pluginv1.GetMetadataResponse{Item: &pluginv1.MetadataItem{ProviderId: typ + ":" + id, ItemType: r.GetItemType(), ProviderIds: st}}, nil
}

func (s *server) GetImages(ctx context.Context, r *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	typ, id := identity(r.GetItemType(), r.GetProviderIds(), r.GetProviderId())
	if id == "" || !s.p.snapshot().Media[typ] {
		return &pluginv1.GetImagesResponse{}, nil
	}
	b, err := s.p.get(ctx, typ, id)
	if err != nil {
		return nil, err
	}
	cfg := s.p.snapshot()
	all := cfg.Selection == "All" || isGalleryImageRequest(ctx)
	kinds := itemKinds
	season := 0
	if r.SeasonNumber != nil {
		kinds = []string{"season"}
		season = int(r.GetSeasonNumber())
	}
	o := &pluginv1.GetImagesResponse{}
	for _, kind := range kinds {
		if !cfg.Types[kind] {
			continue
		}
		chosen := rank(b, kind, season, cfg)
		if !all && len(chosen) > 1 {
			chosen = chosen[:1]
		}
		for _, a := range chosen {
			o.Images = append(o.Images, imageRecord(a, r.SeasonNumber != nil))
		}
	}
	return o, nil
}

func imageRecord(a artwork, seasonImage bool) *pluginv1.ImageRecord {
	k := a.Kind
	if k == "season" {
		k = "poster"
	}
	record := &pluginv1.ImageRecord{Kind: k, Url: canonical(a), Language: a.Language}
	if a.Language != "" {
		record.Metadata = structFromMap(map[string]any{"includes_text": true})
	}
	if seasonImage {
		n := int32(a.Season)
		record.SeasonNumber = &n
	}
	return record
}

// isGalleryImageRequest recognizes the host's advisory picker-purpose header.
// Missing, malformed, or repeated values retain automatic selection behavior.
func isGalleryImageRequest(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(imageRequestPurposeHeader)
	return len(values) == 1 && values[0] == "gallery"
}

func (s *server) GetSeasons(ctx context.Context, r *pluginv1.GetSeasonsRequest) (*pluginv1.GetSeasonsResponse, error) {
	_, id := identity("series", r.GetProviderIds(), r.GetSeriesProviderId())
	cfg := s.p.snapshot()
	if id == "" || !cfg.Media["series"] || !cfg.Types["season"] {
		return &pluginv1.GetSeasonsResponse{}, nil
	}
	b, err := s.p.get(ctx, "series", id)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	o := &pluginv1.GetSeasonsResponse{}
	for _, a := range b.Art {
		if a.Kind != "season" || seen[a.Season] {
			continue
		}
		seen[a.Season] = true
		if chosen := rank(b, "season", a.Season, cfg); len(chosen) > 0 {
			o.Seasons = append(o.Seasons, &pluginv1.SeasonRecord{SeasonNumber: int32(a.Season), PosterPath: canonical(chosen[0])})
		}
	}
	sort.Slice(o.Seasons, func(i, j int) bool { return o.Seasons[i].SeasonNumber < o.Seasons[j].SeasonNumber })
	return o, nil
}

// GetEpisodes is empty: Fanart.tv has no episode artwork.
func (s *server) GetEpisodes(context.Context, *pluginv1.GetEpisodesRequest) (*pluginv1.GetEpisodesResponse, error) {
	return &pluginv1.GetEpisodesResponse{}, nil
}

func (s *server) ResolveImageURL(_ context.Context, r *pluginv1.ResolveImageURLRequest) (*pluginv1.ResolveImageURLResponse, error) {
	return &pluginv1.ResolveImageURLResponse{Url: resolve(r.GetPath())}, nil
}

func (s *server) ResolveImageURLs(_ context.Context, r *pluginv1.ResolveImageURLsRequest) (*pluginv1.ResolveImageURLsResponse, error) {
	o := map[string]string{}
	for _, p := range r.GetPaths() {
		o[p] = resolve(p)
	}
	return &pluginv1.ResolveImageURLsResponse{Urls: o}, nil
}

func main() {
	m, err := manifest.Load(manifestJSON)
	if err != nil {
		panic(err)
	}
	if version != "" {
		m.Version = version
	}
	if path, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			h := sha256.Sum256(b)
			m.Checksum = hex.EncodeToString(h[:])
		}
	}
	s := &server{m: m, p: newProvider()}
	runtime.Serve(runtime.ServeConfig{Servers: runtime.CapabilityServers{Runtime: s, MetadataProvider: s, ImageResolver: s}})
}
