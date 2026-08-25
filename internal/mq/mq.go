// Package mq centralizes queue names and declarations so every service
// declares queues identically (RabbitMQ errors if declarations disagree).
package mq

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	EventsQueue = "events"
	DLQ         = "events.dlq"
	TriageQueue = "triage"
)

// Declare sets up all queues. Rejected events messages (nack with
// requeue=false) are dead-lettered to the DLQ instead of being dropped.
func Declare(channel *amqp.Channel) error {
	// The arguments mean: durable=true (queue survives broker restart),
	// autoDelete=false, exclusive=false, noWait=false.
	if _, err := channel.QueueDeclare(DLQ, true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := channel.QueueDeclare(EventsQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": DLQ,
	}); err != nil {
		return err
	}
	_, err := channel.QueueDeclare(TriageQueue, true, false, false, false, nil)
	return err
}

func PublishJSON(channel *amqp.Channel, queue string, body []byte) error {
	return channel.Publish("", queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // message survives a broker restart
		Body:         body,
	})
}
