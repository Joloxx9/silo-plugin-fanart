// Native runtime smoke check. Uses a dummy key and makes no Fanart.tv requests.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/hashicorp/go-hclog"
	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/protobuf/types/known/structpb"
)

func check(binary, version string) error {
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: runtime.HandshakeConfig(), Plugins: runtime.DefaultPluginSet(runtime.CapabilityServers{}),
		Cmd: exec.Command(binary), AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger: hclog.NewNullLogger(), Stderr: io.Discard, SyncStdout: io.Discard, SyncStderr: io.Discard,
		StartTimeout: 15 * time.Second,
	})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		return fmt.Errorf("native handshake: %w", err)
	}
	raw, err := rpc.Dispense(runtime.PluginSetName)
	if err != nil {
		return err
	}
	typed, ok := raw.(*runtime.Client)
	if !ok {
		return fmt.Errorf("unexpected runtime client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manifest, err := typed.Runtime().GetManifest(ctx, &pluginv1.GetManifestRequest{})
	if err != nil || manifest.GetManifest().GetVersion() != version {
		return fmt.Errorf("runtime version mismatch")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if manifest.GetManifest().GetChecksum() != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("runtime checksum mismatch")
	}
	config, err := structpb.NewStruct(map[string]any{"api_key": "local-smoke-only", "artwork_types": []any{"logo", "clearart"}})
	if err != nil {
		return err
	}
	if _, err := typed.Runtime().Configure(ctx, &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: config}}}); err != nil {
		return err
	}
	image, err := typed.ImageResolver().ResolveImageURL(ctx, &pluginv1.ResolveImageURLRequest{Path: "the-matrix-5.png"})
	if err != nil || image.GetUrl() != "https://assets.fanart.tv/fanart/the-matrix-5.png" {
		return fmt.Errorf("native image resolution failed")
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/smoke <binary> <version>")
		os.Exit(1)
	}
	if err := check(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Native handshake, version, checksum, configuration and resolution passed")
}
