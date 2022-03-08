// Package flow handle incoming flows (currently Netflow v9).
package flow

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	reuseport "github.com/libp2p/go-reuseport"
	flowmessage "github.com/netsampler/goflow2/pb"
	"github.com/netsampler/goflow2/producer"
	"golang.org/x/time/rate"
	"gopkg.in/tomb.v2"

	"akvorado/daemon"
	"akvorado/reporter"
)

// Component represents the flow component.
type Component struct {
	r      *reporter.Reporter
	d      *Dependencies
	t      tomb.Tomb
	config Configuration

	// Templates and sampling
	templatesLock *sync.RWMutex
	templates     map[string]*templateSystem
	samplingLock  *sync.RWMutex
	sampling      map[string]producer.SamplingRateSystem

	// Metrics
	metrics metrics

	// Channel for receiving flows.
	incomingFlows chan *flowmessage.FlowMessage

	// Local address used by the Netflow server. Only valid after Start().
	Address net.Addr
}

// Dependencies are the dependencies of the flow component.
type Dependencies struct {
	Daemon daemon.Component
}

// New creates a new flow component.
func New(r *reporter.Reporter, configuration Configuration, dependencies Dependencies) (*Component, error) {
	c := Component{
		r:             r,
		d:             &dependencies,
		config:        configuration,
		incomingFlows: make(chan *flowmessage.FlowMessage, configuration.BufferLength),
	}
	c.d.Daemon.Track(&c.t, "flow")
	c.initMetrics()
	return &c, nil
}

// Flows returns a channel to receive flows.
func (c *Component) Flows() <-chan *flowmessage.FlowMessage {
	return c.incomingFlows
}

// Start starts the flow component.
func (c *Component) Start() error {
	c.templates = make(map[string]*templateSystem)
	c.templatesLock = &sync.RWMutex{}
	c.sampling = make(map[string]producer.SamplingRateSystem)
	c.samplingLock = &sync.RWMutex{}

	c.r.Info().Str("listen", c.config.Listen).Msg("starting flow server")
	for i := 0; i < c.config.Workers; i++ {
		if err := c.spawnWorker(i); err != nil {
			return fmt.Errorf("unable to spawn worker %d: %w", i, err)
		}
	}

	return nil
}

func (c *Component) spawnWorker(workerID int) error {
	// Listen to UDP port
	var listenAddr net.Addr
	if c.Address != nil {
		// We already are listening on one address, let's
		// listen to the same (useful when using :0).
		listenAddr = c.Address
	} else {
		var err error
		listenAddr, err = reuseport.ResolveAddr("udp", c.config.Listen)
		if err != nil {
			return fmt.Errorf("unable to resolve %v: %w", c.config.Listen, err)
		}
	}
	pconn, err := reuseport.ListenPacket("udp", listenAddr.String())
	if err != nil {
		return fmt.Errorf("unable to listen to %v: %w", listenAddr, err)
	}
	udpConn := pconn.(*net.UDPConn)
	c.Address = udpConn.LocalAddr()

	// Go routine for worker
	payload := make([]byte, 9000)
	c.t.Go(func() error {
		errLimiter := rate.NewLimiter(rate.Every(time.Minute), 1)
		for {
			// Read one packet
			size, source, err := udpConn.ReadFromUDP(payload)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				if errLimiter.Allow() {
					c.r.Err(err).Int("worker", workerID).Msg("unable to receive UDP packet")
				}
				c.metrics.trafficErrors.WithLabelValues("netflow").Inc()
				continue
			}

			c.metrics.trafficBytes.WithLabelValues(source.IP.String(), "netflow").
				Add(float64(size))
			c.metrics.trafficPackets.WithLabelValues(source.IP.String(), "netflow").
				Inc()
			c.metrics.trafficPacketSizeSum.WithLabelValues(source.IP.String(), "netflow").
				Observe(float64(size))
			c.r.Debug().Msg("hello")

			c.decodeFlow(payload[:size], source)
		}
	})

	// Watch for termination and close on dying
	c.t.Go(func() error {
		<-c.t.Dying()
		c.r.Debug().Int("worker", workerID).Msg("stopping flow worker")
		udpConn.Close()
		return nil
	})
	return nil
}

// Stop stops the flow component
func (c *Component) Stop() error {
	defer close(c.incomingFlows)
	c.t.Kill(nil)
	return c.t.Wait()
}
