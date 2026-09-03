// The triage service is the second-tier reviewer: it consumes triage
// requests published by the detector, asks Gemini whether the finding is
// really malicious, and writes the verdict onto the finding in Postgres.
// Rules are cheap and run on everything; the LLM is slow and rate-limited,
// so it only sees events that already matched a rule.
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
)

// Mirrors the detector's TriageRequest.
type TriageRequest struct {
	FindingID int64       `json:"finding_id"`
	Event     event.Event `json:"event"`
	Rule      string      `json:"rule"`
	Severity  string      `json:"severity"`
	Detail    string      `json:"detail"`
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	amqpURL := envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	postgresURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")
	apiKey := os.Getenv("GEMINI_API_KEY")
	model := envOr("GEMINI_MODEL", "gemini-flash-lite-latest")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY is required")
	}

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()

	connection, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatal("connect to rabbitmq: ", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		log.Fatal("open channel: ", err)
	}
	defer channel.Close()
	if err := mq.Declare(channel); err != nil {
		log.Fatal("declare queues: ", err)
	}

	if err := channel.Qos(1, 0, false); err != nil { // one at a time: the LLM is the bottleneck anyway
		log.Fatal("set qos: ", err)
	}
	deliveries, err := channel.Consume(mq.TriageQueue, "triage", false, false, false, false, nil)
	if err != nil {
		log.Fatal("consume: ", err)
	}

	log.Println("triage running, model:", model)

	for delivery := range deliveries {
		var request TriageRequest
		if err := json.Unmarshal(delivery.Body, &request); err != nil {
			log.Printf("malformed triage request, dropping: %.100s", delivery.Body)
			delivery.Ack(false) // triage is best-effort: a lost verdict is acceptable, a stuck queue is not
			continue
		}

		verdict, err := judge(apiKey, model, request)
		if err != nil {
			if delivery.Redelivered { // second failure: give up rather than loop forever
				log.Printf("finding %d: triage failed twice, giving up: %v", request.FindingID, err)
				delivery.Ack(false)
			} else {
				log.Printf("finding %d: triage failed, will retry: %v", request.FindingID, err)
				time.Sleep(10 * time.Second) // crude rate-limit backoff before retrying
				delivery.Nack(false, true)
			}
			continue
		}

		summary := verdict.Verdict + ": " + verdict.Reason
		if _, err := db.Exec(`UPDATE findings SET llm_verdict = $1 WHERE id = $2`, summary, request.FindingID); err != nil {
			log.Println("update finding: ", err)
			delivery.Nack(false, true)
			continue
		}
		log.Printf("finding %d (%s): %s", request.FindingID, request.Rule, summary)
		delivery.Ack(false)
	}
}

// Gemini REST API request/response shapes (only the fields we use).
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}
type geminiPart struct {
	Text string `json:"text"`
}
type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

// judge asks Gemini for a verdict on one finding.
func judge(apiKey, model string, request TriageRequest) (Verdict, error) {
	eventJSON, _ := json.Marshal(request.Event)
	prompt := fmt.Sprintf(`You review security findings about AI agents' tool calls.
Rule %q (severity %s) flagged this event: %s
Event: %s

Is this genuinely malicious or a false positive? Reply with ONLY this JSON, no other text:
{"verdict": "malicious" or "benign", "reason": "<one short sentence>"}`,
		request.Rule, request.Severity, request.Detail, eventJSON)

	body, _ := json.Marshal(geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	})

	url := "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent"
	httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	httpRequest.Header.Set("x-goog-api-key", apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Verdict{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return Verdict{}, err
	}
	if response.StatusCode != http.StatusOK { // includes 429 rate limits; caller retries
		return Verdict{}, fmt.Errorf("gemini returned %d: %.200s", response.StatusCode, responseBody)
	}

	var parsed geminiResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return Verdict{}, err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return Verdict{}, fmt.Errorf("gemini returned no candidates")
	}
	return parseVerdict(parsed.Candidates[0].Content.Parts[0].Text)
}
