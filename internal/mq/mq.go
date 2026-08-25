// Package mq centralizes queue names and declarations so every service
// declares queues identically (RabbitMQ errors if declarations disagree).
package mq

import (
	amqp "github.com/rabbitmq/amqp091-go" // RabbitMQ client library
)

const (
	EventsQueue = "events"     // sensors publish agent events here
	DLQ         = "events.dlq" // malformed events end up here
	TriageQueue = "triage"     // detector requests LLM review here
)

// Declare sets up all queues; safe to call repeatedly (declare = create-if-missing).
func Declare(channel *amqp.Channel) error {
	if _, err := channel.QueueDeclare(DLQ, true, false, false, false, nil); err != nil { // durable=true: survives broker restart
		return err
	}
	if _, err := channel.QueueDeclare(EventsQueue, true, false, false, false, amqp.Table{ // extra args below wire up dead-lettering
		"x-dead-letter-exchange":    "",  // route rejected messages via the default exchange...
		"x-dead-letter-routing-key": DLQ, // ...into the DLQ
	}); err != nil {
		return err
	}
	_, err := channel.QueueDeclare(TriageQueue, true, false, false, false, nil)
	return err
}

// PublishJSON sends one JSON message to the named queue.
func PublishJSON(channel *amqp.Channel, queue string, body []byte) error {
	return channel.Publish("", queue, false, false, amqp.Publishing{ // "" = default exchange, queue name = routing key
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // message written to disk, survives broker restart
		Body:         body,
	})
}
