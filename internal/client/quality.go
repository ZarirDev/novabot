package client

import (
	"encoding/json"
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
func WatchQuality(server string, port int, poll time.Duration) <-chan string {
	ch := make(chan string, 1)
	url := "http://" + server + ":8080/api/v1/audio/quality"

	go func() {
		last := ""
		client := &http.Client{Timeout: 3 * time.Second}

		for {
			resp, err := client.Get(url)
			if err != nil {
				// Server not reachable — that's fine, retry next tick.
				time.Sleep(poll)
				continue
			}

			var body qualityResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
				name := body.Current.Name
				if name != "" && name != last {
					log.Printf("[CLIENT] server quality is %q", name)
					select {
					case ch <- name:
					default:
						// Drop if consumer is slow.
					}
					last = name
				}
			}
			_ = resp.Body.Close()

			time.Sleep(poll)
		}
	}()

	return ch
}
