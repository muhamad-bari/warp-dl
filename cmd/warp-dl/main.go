package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"warp-dl/internal/downloader"
	"warp-dl/internal/ui"
)

var (
	concurrency int
	output      string
	useDoH      bool
)

var rootCmd = &cobra.Command{
	Use:   "warp-dl [url]",
	Short: "A high-performance multi-threaded download manager",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]
		runDownload(url)
	},
}

func init() {
	rootCmd.Flags().IntVarP(&concurrency, "concurrent", "c", 16, "Number of concurrent connections")
	rootCmd.Flags().StringVarP(&output, "output", "o", "", "Output filename")
	rootCmd.Flags().BoolVarP(&useDoH, "doh", "s", true, "Use DNS over HTTPS (Anti-ISP Block)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runDownload(url string) {
	cfg := downloader.Config{
		URL:         url,
		Concurrency: concurrency,
		OutputName:  output,
		UseDoH:      useDoH,
	}

	engine := downloader.NewEngine(cfg)
	
	// Create context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialise UI model
	model := ui.NewModel(engine.Stats)
	p := tea.NewProgram(model)

	// Run download in background
	go func() {
		if err := engine.Start(ctx); err != nil {
			fmt.Printf("Download failed: %v\n", err)
			p.Quit()
			return
		}
	}()

	// Run UI
	// If user presses Ctrl+C, p.Run() returns, 
	// defer cancel() is called, stopping the engine.
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
