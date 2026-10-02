// Package mq centralizes queue names and declarations so every service
// declares queues identically (RabbitMQ errors if declarations disagree).
package mq

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	EventsQueue = "events"     // sensors publish agent events here
	DLQ         = "events.dlq" // malformed events end up here
	TriageQueue = "triage"     // detector requests LLM review here
)

// Declare sets up all queues; safe to call repeatedly (declare = create-if-missing).
// Every queue is durable, so it survives a broker restart.
func Declare(channel *amqp.Channel) error {
	if _, err := channel.QueueDeclare(DLQ, true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := channel.QueueDeclare(EventsQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    "",  // route rejected messages via the default exchange...
		"x-dead-letter-routing-key": DLQ, // ...into the DLQ
	}); err != nil {
		return err
	}
	_, err := channel.QueueDeclare(TriageQueue, true, false, false, false, nil)
	return err
}

// PublishJSON sends one JSON message to the named queue through the default
// exchange, which routes by queue name.
func PublishJSON(channel *amqp.Channel, queue string, body []byte) error {
	return channel.Publish("", queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // with the durable queues, messages survive a broker restart
		Body:         body,
	})
}
