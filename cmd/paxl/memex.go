package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/urfave/cli/v3"
)

const memexRenderReadHeaderTimeout = 5 * time.Second

var listenMemexHTML = func(ctx context.Context, network string, address string) (net.Listener, error) {
	listenConfig := &net.ListenConfig{}
	return listenConfig.Listen(ctx, network, address)
}

func newMemexCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "memex",
		Usage: "Inspect local memex artifacts",
		Commands: []*cli.Command{
			{
				Name:  "render",
				Usage: "Render local memex artifacts",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "html", Usage: "Serve a local HTML view"},
					&cli.StringFlag{
						Name:  "wiki-root",
						Value: ".",
						Usage: "Project root or wiki directory",
					},
					&cli.StringFlag{
						Name:  "host",
						Value: "127.0.0.1",
						Usage: "Host for the local HTML server",
					},
					&cli.IntFlag{
						Name:  "port",
						Value: 0,
						Usage: "Port for the local HTML server; 0 picks a free port",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return memexRender(ctx, cmd, stdout, stderr)
				},
			},
		},
	}
}

func memexRender(
	ctx context.Context,
	cmd *cli.Command,
	stdout io.Writer,
	stderr io.Writer,
) error {
	req, err := parseRenderMemexRequest(cmd)
	if err != nil {
		return fmt.Errorf("parse memex render request: %w", err)
	}
	resp, err := facade.NewMemexFacade().Render(ctx, req, facade.WithVerboseWriter(stderr))
	if err != nil {
		return fmt.Errorf("render memex: %w", err)
	}
	if err := serveMemexHTML(ctx, stdout, cmd.String("host"), cmd.Int("port"), resp); err != nil {
		return fmt.Errorf("serve memex html: %w", err)
	}
	return nil
}

func parseRenderMemexRequest(cmd *cli.Command) (*facade.RenderMemexRequest, error) {
	if !cmd.Bool("html") {
		return nil, fmt.Errorf("--html is required")
	}
	return &facade.RenderMemexRequest{
		WikiRoot: cmd.String("wiki-root"),
		Format:   facade.MemexRenderFormatHTML,
	}, nil
}

func serveMemexHTML(
	ctx context.Context,
	stdout io.Writer,
	host string,
	port int,
	resp *facade.RenderMemexResponse,
) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	listener, err := listenMemexHTML(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen on %s:%d: %w", host, port, err)
	}
	server := &http.Server{
		Handler:           newMemexRenderHandler(resp),
		ReadHeaderTimeout: memexRenderReadHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if _, err := fmt.Fprintf(
		stdout,
		"Serving memex HTML at http://%s/\n",
		listener.Addr().String(),
	); err != nil {
		_ = listener.Close()
		return fmt.Errorf("write memex render address: %w", err)
	}
	err = server.Serve(listener)
	if errors.Is(err, net.ErrClosed) && ctx.Err() != nil {
		err = nil
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newMemexRenderHandler(resp *facade.RenderMemexResponse) http.Handler {
	mux := http.NewServeMux()
	htmlBody := ""
	assets := make(map[string]*facade.MemexRenderAsset)
	if resp != nil {
		htmlBody = resp.HTML
		for _, asset := range resp.Assets {
			if asset == nil || asset.URLPath == "" || asset.FilePath == "" {
				continue
			}
			urlPath := asset.URLPath
			if !strings.HasPrefix(urlPath, "/") {
				urlPath = "/" + urlPath
			}
			copied := *asset
			copied.URLPath = urlPath
			assets[urlPath] = &copied
		}
	}
	mux.HandleFunc("/", func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(writer, req)
			return
		}
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(writer, htmlBody)
	})
	for urlPath, asset := range assets {
		asset := asset
		mux.HandleFunc(urlPath, func(writer http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				writer.Header().Set("Allow", "GET, HEAD")
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if asset.ContentType != "" {
				writer.Header().Set("Content-Type", asset.ContentType)
			}
			http.ServeFile(writer, req, asset.FilePath)
		})
	}
	return mux
}
