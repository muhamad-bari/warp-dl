package downloader

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Config holds the configuration for the download
type Config struct {
	URL         string
	Concurrency int
	OutputName  string
}

// Stats holds real-time statistics
type Stats struct {
	TotalBytes      int64
	DownloadedBytes int64 // Atomic
	Speed           float64
	Progress        float64
}

// Part represents a segment of the file to download
type Part struct {
	ID        int
	Start     int64
	End       int64
	TempPath  string
	Downloaded int64
}

// Engine handles the download process
type Engine struct {
	Config     Config
	Stats      *Stats
	Client     *http.Client
	Parts      []*Part
	PartFiles  []*os.File
	IsResumable bool
}

// UpdateDownloaded atomically updates the downloaded bytes count
func (s *Stats) AddDownloaded(n int64) {
	atomic.AddInt64(&s.DownloadedBytes, n)
}

// GetDownloaded atomically gets the downloaded bytes count
func (s *Stats) GetDownloaded() int64 {
	return atomic.LoadInt64(&s.DownloadedBytes)
}
