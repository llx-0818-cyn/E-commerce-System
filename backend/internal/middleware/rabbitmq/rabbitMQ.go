package rabbitmq

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"feedsystem_video_go/internal/config"
	"strconv"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQ 只管理 Connection，Channel 由各组件按需创建
type RabbitMQ struct {
	Conn *amqp.Connection

	mu     sync.Mutex
	config *config.RabbitMQConfig
	closed bool
}

func NewRabbitMQ(cfg *config.RabbitMQConfig) (*RabbitMQ, error) {
	if cfg == nil {
		return nil, errors.New("rabbitmq config is nil")
	}
	configuration := *cfg
	conn, err := dialRabbitMQ(&configuration)
	if err != nil {
		return nil, err
	}
	return &RabbitMQ{Conn: conn, config: &configuration}, nil
}

func (r *RabbitMQ) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.Conn != nil {
		return r.Conn.Close()
	}
	return nil
}

func (r *RabbitMQ) NewChannel() (*amqp.Channel, error) {
	if r == nil {
		return nil, errors.New("rabbitmq connection is not initialized")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("rabbitmq connection is closed")
	}
	if r.Conn == nil || r.Conn.IsClosed() {
		if r.config == nil {
			return nil, errors.New("rabbitmq connection is closed and reconnect config is unavailable")
		}
		connection, err := dialRabbitMQ(r.config)
		if err != nil {
			return nil, err
		}
		r.Conn = connection
	}
	return r.Conn.Channel()
}

func dialRabbitMQ(cfg *config.RabbitMQConfig) (*amqp.Connection, error) {
	url := "amqp://" + cfg.Username + ":" + cfg.Password + "@" + cfg.Host + ":" + strconv.Itoa(cfg.Port) + "/"
	return amqp.Dial(url)
}

func DeclareTopic(ch *amqp.Channel, exchange string, queue string, bindingKey string) error {
	return DeclareReliableTopic(ch, exchange, queue, bindingKey, DefaultRetryDelay)
}

func newEventID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
