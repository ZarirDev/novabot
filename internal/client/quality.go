package client

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type qualityResponse struct {
	Current struct {
		Name string `json:"name"`
	} `json:"current"`
}

// WatchQuality polls the server's audio quality endpoint and delivers the
// current name to the returned channel whenever it changes. The channel is
// never closed; the caller can abandon it when shutting down.
//
// Emits the current server value immediately on start so the client can
// sync its format before it begins capturing.
func WatchQuality(server string, port int, poll time.Duration) <-chan string {
	ch := make(chan string, 4)
	url := fmt.Sprintf("http://%s:%d/api/v1/audio/quality", server, port)

	log.Printf("[CLIENT] polling quality endpoint: %s (every %s)", url, poll)

	go func() {
		last := ""
		httpClient := &http.Client{Timeout: 2 * time.Second}
		consecutiveErrs := 0

		for {
			resp, err := httpClient.Get(url)
			if err != nil {
				consecutiveErrs++
				// Log first failure and then every 10th to avoid spam.
				if consecutiveErrs == 1 || consecutiveErrs%10 == 0 {
					log.Printf("[CLIENT] quality poll failed (%d consecutive): %v",
						consecutiveErrs, err)
				}
				time.Sleep(poll)
				continue
			}
			if consecutiveErrs > 0 {
				log.Printf("[CLIENT] quality poll recovered after %d failures", consecutiveErrs)
				consecutiveErrs = 0
			}

			var body qualityResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()

			if decodeErr != nil {
				log.Printf("[CLIENT] quality poll decode failed: %v", decodeErr)
				time.Sleep(poll)
				continue
			}

			name := body.Current.Name
			if name == "" {
				time.Sleep(poll)
				continue
			}

			if name != last {
				log.Printf("[CLIENT] server quality is %q", name)
				select {
				case ch <- name:
				default:
					// Consumer is behind; drop and let it catch up on next poll.
				}
				last = name
			}

			time.Sleep(poll)
		}
	}()

	return ch
}
