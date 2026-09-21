package music

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

type Track struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Duration float64 `json:"duration"`
	URL      string  `json:"url"`
}

// Search runs yt-dlp in flat-playlist mode and returns the top N results.
// Emits detailed logs so failures are diagnosable without re-running by hand.
func Search(query string, limit int) ([]Track, error) {
	if limit <= 0 {
		limit = 10
	}

	start := time.Now()
	log.Printf("[MUSIC] search start: q=%q limit=%d", query, limit)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	target := fmt.Sprintf("ytsearch%d:%s", limit, query)
	cmd := exec.CommandContext(ctx, "yt-dlp",
		target,
		"--flat-playlist",
		"--dump-json",
		"--no-warnings",
		"--quiet",
		"--no-check-certificates", // avoid TLS stalls on older systems
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		log.Printf("[MUSIC] search: yt-dlp spawn failed: %v", err)
		return nil, fmt.Errorf("yt-dlp spawn: %w", err)
	}
	log.Printf("[MUSIC] search: yt-dlp started (pid=%d)", cmd.Process.Pid)

	waitErr := cmd.Wait()
	elapsed := time.Since(start)

	if waitErr != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = "(no stderr)"
		}
		// Keep stderr short so it fits in a log line.
		if len(errText) > 400 {
			errText = errText[:400] + "…"
		}
		log.Printf("[MUSIC] search: yt-dlp failed after %s: %v | stderr: %s",
			elapsed.Round(time.Millisecond), waitErr, errText)

		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out after %s", elapsed.Round(time.Second))
		}
		return nil, fmt.Errorf("yt-dlp exit: %w (%s)", waitErr, errText)
	}

	log.Printf("[MUSIC] search: yt-dlp finished in %s (%d bytes stdout)",
		elapsed.Round(time.Millisecond), stdout.Len())

	if stdout.Len() == 0 {
		log.Printf("[MUSIC] search: yt-dlp produced no output")
		return nil, fmt.Errorf("yt-dlp returned no results")
	}

	var tracks []Track
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	skipped := 0
	for dec.More() {
		var raw struct {
			ID       string  `json:"id"`
			Title    string  `json:"title"`
			Uploader string  `json:"uploader"`
			Channel  string  `json:"channel"`
			Duration float64 `json:"duration"`
			URL      string  `json:"url"`
			WebURL   string  `json:"webpage_url"`
		}
		if err := dec.Decode(&raw); err != nil {
			skipped++
			continue
		}

		artist := raw.Uploader
		if artist == "" {
			artist = raw.Channel
		}

		url := raw.WebURL
		if url == "" {
			url = raw.URL
		}
		if url == "" && raw.ID != "" {
			url = "https://www.youtube.com/watch?v=" + raw.ID
		}

		tracks = append(tracks, Track{
			ID:       raw.ID,
			Title:    raw.Title,
			Artist:   artist,
			Duration: raw.Duration,
			URL:      url,
		})
	}

	log.Printf("[MUSIC] search: parsed %d tracks (%d malformed entries skipped)",
		len(tracks), skipped)

	return tracks, nil
}
