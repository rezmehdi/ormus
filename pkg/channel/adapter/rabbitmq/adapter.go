package rbbitmqchannel

import (
	"fmt"
	"sync"
	"time"

	"github.com/ormushq/ormus/destination/dconfig"
	"github.com/ormushq/ormus/pkg/channel"
	"github.com/ormushq/ormus/pkg/errmsg"
	amqp "github.com/rabbitmq/amqp091-go"
)

type ChannelAdapter struct {
	wg                       *sync.WaitGroup
	done                     <-chan bool
	channels                 map[string]*rabbitmqChannel
	config                   dconfig.RabbitMQConsumerConnection
	rabbitmq                 *Rabbitmq
	rabbitmqConnectionClosed chan bool
}
type Rabbitmq struct {
	connection *amqp.Connection
	cond       *sync.Cond
}

func New(done <-chan bool, wg *sync.WaitGroup, config dconfig.RabbitMQConsumerConnection) *ChannelAdapter {
	cond := sync.NewCond(&sync.Mutex{})
	rabbitmq := Rabbitmq{
		cond:       cond,
		connection: &amqp.Connection{},
	}
	c := &ChannelAdapter{
		done:                     done,
		wg:                       wg,
		config:                   config,
		rabbitmq:                 &rabbitmq,
		channels:                 make(map[string]*rabbitmqChannel),
		rabbitmqConnectionClosed: make(chan bool),
	}

	for {
		err := c.connect()
		time.Sleep(time.Second * time.Duration(config.ReconnectSecond))
		failOnError(err, "rabbitmq connection failed")
		if err == nil {
			break
		}
	}

	return c
}

func (ca *ChannelAdapter) connect() error {
	ca.rabbitmq.cond.L.Lock()
	defer ca.rabbitmq.cond.L.Unlock()
	close(ca.rabbitmqConnectionClosed)
	ca.rabbitmqConnectionClosed = make(chan bool)

	conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@%s:%d/%s",
		ca.config.User, ca.config.Password, ca.config.Host,
		ca.config.Port, ca.config.Vhost))
	failOnError(err, "Failed to connect to rabbitmq server")
	if err != nil {
		return err
	}
	ca.rabbitmq.connection = conn
	ca.rabbitmq.cond.Broadcast()

	ca.wg.Add(1)
	go func() {
		defer ca.wg.Done()
		for {
			select {
			case <-ca.done:

				return
			case <-ca.rabbitmqConnectionClosed:

				return
			}
		}
	}()
	go ca.waitForConnectionClose()

	return nil
}

func (ca *ChannelAdapter) waitForConnectionClose() {
	connectionClosedChannel := make(chan *amqp.Error)
	ca.rabbitmq.connection.NotifyClose(connectionClosedChannel)

	for {
		select {
		case <-ca.done:
			return
		case err := <-connectionClosedChannel:
			fmt.Println(err)
			for {
				e := ca.connect()
				time.Sleep(time.Second * time.Duration(ca.config.ReconnectSecond))
				failOnError(e, "Connection failed to rabbitmq")
				if e == nil {
					break
				}
			}

			return
		}
	}
}

func (ca *ChannelAdapter) NewChannel(name string, mode channel.Mode, bufferSize, numberInstants, maxRetryPolicy int) {
	ca.channels[name] = newChannel(
		ca.done,
		ca.wg,
		rabbitmqChannelParams{
			mode:           mode,
			rabbitmq:       ca.rabbitmq,
			exchange:       name + "-exchange",
			queue:          name + "-queue",
			bufferSize:     bufferSize,
			numberInstants: numberInstants,
			maxRetryPolicy: maxRetryPolicy,
		})
}

func (ca *ChannelAdapter) GetInputChannel(name string) (chan<- []byte, error) {
	if c, ok := ca.channels[name]; ok {
		return c.GetInputChannel(), nil
	}

	return nil, fmt.Errorf(errmsg.ErrChannelNotFound, name)
}

func (ca *ChannelAdapter) GetOutputChannel(name string) (<-chan channel.Message, error) {
	if c, ok := ca.channels[name]; ok {
		return c.GetOutputChannel(), nil
	}

	return nil, fmt.Errorf(errmsg.ErrChannelNotFound, name)
}

func (ca *ChannelAdapter) GetMode(name string) (channel.Mode, error) {
	if c, ok := ca.channels[name]; ok {
		return c.GetMode(), nil
	}

	return "", fmt.Errorf(errmsg.ErrChannelNotFound, name)
}

func WaitForConnection(rabbitmq *Rabbitmq) {
	rabbitmq.cond.L.Lock()
	defer rabbitmq.cond.L.Unlock()
	for rabbitmq.connection.IsClosed() {
		fmt.Println(rabbitmq.connection.IsClosed())
		rabbitmq.cond.Wait()

	}
}
