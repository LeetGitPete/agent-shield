package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/leetgitpete/agent-shield/internal/mq"
)

// Stats is the JSON response of GET /stats: the state of the events queue.
// Consumers is the number of running detectors; the two rates are events
// per second in and out.
type Stats struct {
	Ready       int     `json:"ready"`
	Unacked     int     `json:"unacked"`
	PublishRate float64 `json:"publish_rate"`
	AckRate     float64 `json:"ack_rate"`
	Consumers   int     `json:"consumers"`
}

// queueInfo is the part of the management API's queue object that Stats is
// built from. The broker omits message_stats for a queue it has not sampled
// yet; the rates then stay zero.
type queueInfo struct {
	MessagesReady          int `json:"messages_ready"`
	MessagesUnacknowledged int `json:"messages_unacknowledged"`
	Consumers              int `json:"consumers"`
	MessageStats           struct {
		PublishDetails struct {
			Rate float64 `json:"rate"`
		} `json:"publish_details"`
		AckDetails struct {
			Rate float64 `json:"rate"`
		} `json:"ack_details"`
	} `json:"message_stats"`
}

// fetchStats makes one request to the management API for the events queue of
// the default virtual host. mgmtURL is the base URL including credentials;
// the client sends them as basic auth.
//
// Its errors are fixed descriptions, at most with the broker's status code.
// The HTTP client's own error text is never passed on, because it embeds the
// management URL.
func fetchStats(client *http.Client, mgmtURL string) (Stats, error) {
	// The default virtual host is named "/", so it must stay percent-encoded in the path.
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(mgmtURL, "/")+"/api/queues/%2F/"+mq.EventsQueue, nil)
	if err != nil {
		return Stats{}, errors.New("broker management URL is invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		return Stats{}, errors.New("broker management API is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { // 404: the queue is not declared yet; 401: rejected credentials
		return Stats{}, fmt.Errorf("broker management API answered %d", response.StatusCode)
	}

	var queue queueInfo
	if err := json.NewDecoder(response.Body).Decode(&queue); err != nil {
		return Stats{}, errors.New("broker management API sent an unreadable response")
	}
	return Stats{
		Ready:       queue.MessagesReady,
		Unacked:     queue.MessagesUnacknowledged,
		PublishRate: queue.MessageStats.PublishDetails.Rate,
		AckRate:     queue.MessageStats.AckDetails.Rate,
		Consumers:   queue.Consumers,
	}, nil
}
