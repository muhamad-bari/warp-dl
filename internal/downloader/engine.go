package downloader

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NewEngine creates a new download engine
func NewEngine(cfg Config) *Engine {
	client := &http.Client{
		Timeout: 0,
	}
	
	if cfg.UseDoH {
		client.Transport = NewDoHTransport()
	} else {
		// Even without DoH, we want to skip TLS verification as requested
		client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
			ForceAttemptHTTP2: false,
		}
	}

	return &Engine{
		Config: cfg,
		Stats:  &Stats{},
		Client: client,
	}
}

// Start initiates the download process
func (e *Engine) Start(ctx context.Context) error {
	// 1. Probe the URL (Try HEAD first, then GET)
	totalBytes, resumable, err := e.probeURL(ctx)
	if err != nil {
		return fmt.Errorf("failed to probe URL: %w", err)
	}

	e.Stats.TotalBytes = totalBytes
	e.IsResumable = resumable && e.Stats.TotalBytes > 0

	// Handle output filename
	if e.Config.OutputName == "" {
		e.Config.OutputName = filepath.Base(e.Config.URL)
	}

	// 2. Segmentation
	if e.IsResumable {
		e.calculateSegments()
	} else {
		// Fallback to single connection
		e.Parts = []*Part{{
			ID:       0,
			Start:    0,
			End:      e.Stats.TotalBytes - 1,
			TempPath: fmt.Sprintf("%s.part0", e.Config.OutputName),
		}}
	}

	// 3. Download Parts
	var wg sync.WaitGroup
	errChan := make(chan error, len(e.Parts))

	for _, part := range e.Parts {
		wg.Add(1)
		go func(p *Part) {
			defer wg.Done()
			if err := e.downloadPartWithRetry(ctx, p); err != nil {
				errChan <- err
			}
		}(part)
	}

	// Wait for all parts to finish
	wg.Wait()
	close(errChan)

	// Check for errors
	if len(errChan) > 0 {
		return <-errChan // Return the first error encountered
	}

	// 4. Merge Files
	if err := e.mergeParts(); err != nil {
		return fmt.Errorf("failed to merge files: %w", err)
	}

	return nil
}

func (e *Engine) probeURL(ctx context.Context) (int64, bool, error) {
	// Try HEAD first
	req, err := http.NewRequestWithContext(ctx, "HEAD", e.Config.URL, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := e.Client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		return resp.ContentLength, resp.Header.Get("Accept-Ranges") == "bytes", nil
	}
	if resp != nil {
		resp.Body.Close()
	}

	// If HEAD fails, try GET with Range: bytes=0-0
	req, err = http.NewRequestWithContext(ctx, "GET", e.Config.URL, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Range", "bytes=0-0")

	resp, err = e.Client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPartialContent {
		// Parse Content-Range: bytes 0-0/123456
		cr := resp.Header.Get("Content-Range")
		parts := strings.Split(cr, "/")
		if len(parts) == 2 {
			total, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil {
				return total, true, nil
			}
		}
	} else if resp.StatusCode == http.StatusOK {
		// Server ignored range, returns full content (not resumable usually, or single chunk)
		return resp.ContentLength, false, nil
	}

	return 0, false, fmt.Errorf("probe failed with status: %s", resp.Status)
}

func (e *Engine) calculateSegments() {
	partSize := e.Stats.TotalBytes / int64(e.Config.Concurrency)
	e.Parts = make([]*Part, e.Config.Concurrency)

	for i := 0; i < e.Config.Concurrency; i++ {
		start := int64(i) * partSize
		end := start + partSize - 1

		if i == e.Config.Concurrency-1 {
			end = e.Stats.TotalBytes - 1
		}

		e.Parts[i] = &Part{
			ID:       i,
			Start:    start,
			End:      end,
			TempPath: fmt.Sprintf("%s.part%d", e.Config.OutputName, i),
		}
	}
}

func (e *Engine) downloadPartWithRetry(ctx context.Context, part *Part) error {
	maxRetries := 3
	var err error

	for i := 0; i < maxRetries; i++ {
		err = e.downloadPart(ctx, part)
		if err == nil {
			return nil
		}
		// If context canceled, don't retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Backoff simple
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}
	return fmt.Errorf("failed to download part %d after %d retries: %w", part.ID, maxRetries, err)
}

func (e *Engine) downloadPart(ctx context.Context, part *Part) error {
	req, err := http.NewRequestWithContext(ctx, "GET", e.Config.URL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	if e.IsResumable {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", part.Start, part.End))
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned unexpected status: %s", resp.Status)
	}

	file, err := os.Create(part.TempPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a proxy reader to update progress
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := resp.Body.Read(buf)
			if n > 0 {
				nw, wErr := file.Write(buf[:n])
				if wErr != nil {
					return wErr
				}
				if n != nw {
					return io.ErrShortWrite
				}
				e.Stats.AddDownloaded(int64(n))
			}
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
	}
}

func (e *Engine) mergeParts() error {
	finalFile, err := os.Create(e.Config.OutputName)
	if err != nil {
		return err
	}
	defer finalFile.Close()

	for _, part := range e.Parts {
		partFile, err := os.Open(part.TempPath)
		if err != nil {
			finalFile.Close() // Close before returning
			return err
		}
		
		_, err = io.Copy(finalFile, partFile)
		partFile.Close()
		if err != nil {
			return err
		}

		// Cleanup temp file
		os.Remove(part.TempPath)
	}

	return nil
}
