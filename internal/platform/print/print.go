// Package print renders documents to PDF via Gotenberg.
//
// Renderer is the interface so we can swap in a different backend later.
// GotenbergRenderer POSTs an HTML body to Gotenberg's /forms/chromium/convert/html
// and streams back the PDF bytes.
package print

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type Renderer interface {
	RenderHTML(ctx context.Context, html []byte, opts Options) ([]byte, error)
}

type Options struct {
	PaperWidth  float64 // inches; defaults 8.27 (A4 portrait)
	PaperHeight float64 // inches; defaults 11.69
	MarginTop   float64
	MarginBottom float64
	MarginLeft  float64
	MarginRight float64
}

type GotenbergRenderer struct {
	BaseURL string
	Client  *http.Client
}

func NewGotenbergRenderer(baseURL string) *GotenbergRenderer {
	return &GotenbergRenderer{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (g *GotenbergRenderer) RenderHTML(ctx context.Context, html []byte, opts Options) ([]byte, error) {
	if opts.PaperWidth == 0 {
		opts.PaperWidth = 8.27
	}
	if opts.PaperHeight == 0 {
		opts.PaperHeight = 11.69
	}

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	// index.html — the file Gotenberg consumes.
	fw, err := w.CreateFormFile("files", "index.html")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(html); err != nil {
		return nil, err
	}

	// Form fields (paper, margins) — Gotenberg accepts these as form values.
	_ = w.WriteField("paperWidth",   fmt.Sprintf("%.4f", opts.PaperWidth))
	_ = w.WriteField("paperHeight",  fmt.Sprintf("%.4f", opts.PaperHeight))
	_ = w.WriteField("marginTop",    fmt.Sprintf("%.4f", opts.MarginTop))
	_ = w.WriteField("marginBottom", fmt.Sprintf("%.4f", opts.MarginBottom))
	_ = w.WriteField("marginLeft",   fmt.Sprintf("%.4f", opts.MarginLeft))
	_ = w.WriteField("marginRight",  fmt.Sprintf("%.4f", opts.MarginRight))

	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.BaseURL+"/forms/chromium/convert/html", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gotenberg: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gotenberg %d: %s", resp.StatusCode, string(errBody))
	}
	return io.ReadAll(resp.Body)
}
