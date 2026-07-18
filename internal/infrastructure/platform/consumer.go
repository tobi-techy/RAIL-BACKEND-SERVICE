package platform

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

const (
	processTimeout    = 30 * time.Second
	maxReconnectDelay = 30 * time.Second
)

// consumerCore is a self-healing AMQP consumer: it maintains a connection,
// re-establishes it on failure, dead-letters permanent failures, and requeues
// only transient (retryable) ones. Both the message and action consumers share it.
type consumerCore struct {
	amqpURL    string
	exchange   string
	queueName  string
	routingKey string
	handle     func(ctx context.Context, body []byte) error

	mu        sync.RWMutex
	conn      *amqp091.Connection
	consumeCh *amqp091.Channel
	pubCh     *amqp091.Channel

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newConsumerCore(amqpURL, exchange, queueName, routingKey string, handle func(ctx context.Context, body []byte) error) *consumerCore {
	return &consumerCore{
		amqpURL:    amqpURL,
		exchange:   exchange,
		queueName:  queueName,
		routingKey: routingKey,
		handle:     handle,
		done:       make(chan struct{}),
	}
}

// setupTopology declares the exchange, a dead-letter exchange+queue, and the
// main queue (routing permanent failures to the DLQ), then returns a consume
// channel and a dedicated publish channel.
func setupTopology(conn *amqp091.Connection, exchange, queueName, routingKey string) (*amqp091.Channel, *amqp091.Channel, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, fmt.Errorf("open consume channel: %w", err)
	}
	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("set qos: %w", err)
	}
	if err := ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("declare exchange: %w", err)
	}

	// Dead-letter exchange + queue for messages that fail permanently.
	dlx := exchange + ".dlx"
	dlq := queueName + ".dead"
	if err := ch.ExchangeDeclare(dlx, "fanout", true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("declare dlx: %w", err)
	}
	if _, err := ch.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("declare dlq: %w", err)
	}
	if err := ch.QueueBind(dlq, "", dlx, false, nil); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("bind dlq: %w", err)
	}

	queueArgs := amqp091.Table{"x-dead-letter-exchange": dlx}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, queueArgs); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("declare queue: %w", err)
	}
	if err := ch.QueueBind(queueName, routingKey, exchange, false, nil); err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("bind queue: %w", err)
	}

	pubCh, err := conn.Channel()
	if err != nil {
		ch.Close()
		return nil, nil, fmt.Errorf("open publish channel: %w", err)
	}
	if err := pubCh.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		pubCh.Close()
		ch.Close()
		return nil, nil, fmt.Errorf("declare exchange on publish channel: %w", err)
	}

	return ch, pubCh, nil
}

// Start dials AMQP and begins consuming. It returns an error only if the initial
// connection fails; subsequent drops are recovered automatically.
func (c *consumerCore) Start() error {
	if err := c.connect(); err != nil {
		return err
	}
	log.Printf("platform consumer started: queue=%s", c.queueName)
	return nil
}

func (c *consumerCore) connect() error {
	conn, err := amqp091.Dial(c.amqpURL)
	if err != nil {
		return fmt.Errorf("dial amqp: %w", err)
	}
	consumeCh, pubCh, err := setupTopology(conn, c.exchange, c.queueName, c.routingKey)
	if err != nil {
		conn.Close()
		return err
	}
	msgs, err := consumeCh.Consume(c.queueName, "", false, false, false, false, nil)
	if err != nil {
		conn.Close()
		return fmt.Errorf("consume: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.consumeCh = consumeCh
	c.pubCh = pubCh
	c.mu.Unlock()

	closeCh := conn.NotifyClose(make(chan *amqp091.Error, 1))
	c.wg.Add(1)
	go c.consumeLoop(msgs, closeCh)
	return nil
}

func (c *consumerCore) consumeLoop(msgs <-chan amqp091.Delivery, closeCh chan *amqp091.Error) {
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case err := <-closeCh:
			select {
			case <-c.done:
				return
			default:
				log.Printf("amqp connection closed (queue=%s): %v; reconnecting", c.queueName, err)
				c.reconnect()
				return
			}
		case delivery, ok := <-msgs:
			if !ok {
				select {
				case <-c.done:
					return
				default:
					log.Printf("delivery channel closed (queue=%s); reconnecting", c.queueName)
					c.reconnect()
					return
				}
			}
			c.handleDelivery(delivery)
		}
	}
}

func (c *consumerCore) reconnect() {
	backoff := time.Second
	for {
		select {
		case <-c.done:
			return
		case <-time.After(backoff):
		}
		if err := c.connect(); err != nil {
			log.Printf("amqp reconnect failed (queue=%s): %v", c.queueName, err)
			if backoff < maxReconnectDelay {
				backoff *= 2
			}
			continue
		}
		log.Printf("amqp reconnected (queue=%s)", c.queueName)
		return
	}
}

func (c *consumerCore) handleDelivery(delivery amqp091.Delivery) {
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	if err := c.handle(ctx, delivery.Body); err != nil {
		if IsRetryable(err) {
			log.Printf("retryable error (queue=%s): %v", c.queueName, err)
			if nackErr := delivery.Nack(false, true); nackErr != nil {
				log.Printf("nack(requeue) failed: %v", nackErr)
			}
			return
		}
		log.Printf("permanent error, dead-lettering (queue=%s): %v", c.queueName, err)
		if nackErr := delivery.Nack(false, false); nackErr != nil {
			log.Printf("nack(dead-letter) failed: %v", nackErr)
		}
		return
	}
	if ackErr := delivery.Ack(false); ackErr != nil {
		log.Printf("ack failed (queue=%s): %v", c.queueName, ackErr)
	}
}

// Publish sends a message on the consumer's dedicated publish channel.
func (c *consumerCore) Publish(ctx context.Context, exchange, routingKey string, data []byte) error {
	c.mu.RLock()
	ch := c.pubCh
	c.mu.RUnlock()
	if ch == nil {
		return fmt.Errorf("amqp publish channel not ready")
	}
	return ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp091.Publishing{
		ContentType:  "application/json",
		Body:         data,
		DeliveryMode: amqp091.Persistent,
	})
}

func (c *consumerCore) Shutdown() error {
	c.closeOnce.Do(func() { close(c.done) })
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pubCh != nil {
		_ = c.pubCh.Close()
	}
	if c.consumeCh != nil {
		_ = c.consumeCh.Close()
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("close connection: %w", err)
		}
	}
	return nil
}

// Consumer handles inbound platform messages.
type Consumer struct{ *consumerCore }

// ActionConsumer handles action-confirmation postbacks.
type ActionConsumer struct{ *consumerCore }

func NewConsumer(amqpURL, exchange, queueName, routingKey string, processor *Processor) (*Consumer, error) {
	return &Consumer{newConsumerCore(amqpURL, exchange, queueName, routingKey, processor.Process)}, nil
}

func NewActionConsumer(amqpURL, exchange, queueName, routingKey string, processor *Processor) (*ActionConsumer, error) {
	return &ActionConsumer{newConsumerCore(amqpURL, exchange, queueName, routingKey, processor.ProcessAction)}, nil
}
