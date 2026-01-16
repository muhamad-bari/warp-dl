package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NewEngine creates a new download engine
func NewEngine(cfg Config) *Engine {
	return &Engine{
		Config: cfg,
		Stats:  &Stats{},
		Client: &http.Client{
			Timeout: 0, // No timeout for large downloads, individual reads handled by context
		},
	}
}

// Start initiates the download process
func (e *Engine) Start(ctx context.Context) error {
	// 1. Probe the URL
	req, err := http.NewRequestWithContext(ctx, "HEAD", e.Config.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to probe URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned non-200 status: %s", resp.Status)
	}

	e.Stats.TotalBytes = resp.ContentLength
	e.IsResumable = resp.Header.Get("Accept-Ranges") == "bytes" && e.Stats.TotalBytes > 0

	// Handle output filename
	if e.Config.OutputName == "" {
		// Try to get filename from Content-Disposition or URL
		// Simple fallback for now
		e.Config.OutputName = filepath.Base(e.Config.URL)
	}

	// 2. Segmentation
	if e.IsResumable {
		e.calculateSegments()
	} else {
		// Fallback to single connection
		e.Parts = []*Part{{
			ID:    0,
			Start: 0,
			End:   e.Stats.TotalBytes - 1,
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
				wErr := 0
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
