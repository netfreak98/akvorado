package snmp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbjohnson/clock"

	"akvorado/daemon"
	"akvorado/helpers"
	"akvorado/reporter"
)

func expectSNMPLookup(t *testing.T, c *Component, sampler string, ifIndex uint, expected answer) {
	t.Helper()
	gotSamplerName, gotInterface, err := c.Lookup(sampler, ifIndex)
	got := answer{gotSamplerName, gotInterface, err}
	if diff := helpers.Diff(got, expected); diff != "" {
		t.Fatalf("Lookup() (-got, +want):\n%s", diff)
	}
}

func TestLookup(t *testing.T) {
	r := reporter.NewMock(t)
	c := NewMock(t, r, DefaultConfiguration, Dependencies{Daemon: daemon.NewMock(t)})
	defer func() {
		if err := c.Stop(); err != nil {
			t.Fatalf("Stop() error:\n%+v", err)
		}
	}()

	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})
}

func TestSNMPCommunities(t *testing.T) {
	r := reporter.NewMock(t)
	configuration := DefaultConfiguration
	configuration.DefaultCommunity = "notpublic"
	configuration.Communities = map[string]string{
		"127.0.0.1": "public",
		"127.0.0.2": "private",
	}
	c := NewMock(t, r, configuration, Dependencies{Daemon: daemon.NewMock(t)})
	defer func() {
		if err := c.Stop(); err != nil {
			t.Fatalf("Stop() error:\n%+v", err)
		}
	}()

	// Use "public" as a community. Should work.
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})

	// Use "private", should not work
	expectSNMPLookup(t, c, "127.0.0.2", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.2", 765, answer{Err: ErrCacheMiss})

	// Use default community, should not work
	expectSNMPLookup(t, c, "127.0.0.3", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.3", 765, answer{Err: ErrCacheMiss})
}

func TestComponentSaveLoad(t *testing.T) {
	r := reporter.NewMock(t)
	configuration := DefaultConfiguration
	configuration.CachePersistFile = filepath.Join(t.TempDir(), "cache")
	c := NewMock(t, r, configuration, Dependencies{Daemon: daemon.NewMock(t)})

	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop() error:\n%+c", err)
	}

	r = reporter.NewMock(t)
	c = NewMock(t, r, configuration, Dependencies{Daemon: daemon.NewMock(t)})
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop() error:\n%+c", err)
	}
}

func TestAutoRefresh(t *testing.T) {
	r := reporter.NewMock(t)
	configuration := DefaultConfiguration
	mockClock := clock.NewMock()
	c := NewMock(t, r, configuration, Dependencies{Daemon: daemon.NewMock(t), Clock: mockClock})

	// Fetch a value
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})

	// Keep it in the cache!
	mockClock.Add(25 * time.Minute)
	c.Lookup("127.0.0.1", 765)
	mockClock.Add(25 * time.Minute)
	c.Lookup("127.0.0.1", 765)

	// Go forward, we expect the entry to have been refreshed and be still present
	mockClock.Add(25 * time.Minute)
	time.Sleep(10 * time.Millisecond)
	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{
		SamplerName: "127_0_0_1",
		Interface:   Interface{Name: "Gi0/0/765", Description: "Interface 765", Speed: 1000},
	})

	// Stop and look at the cache
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop() error:\n%+v", err)
	}

	gotMetrics := r.GetMetrics("akvorado_snmp_cache_")
	expectedMetrics := map[string]string{
		`expired`:      "0",
		`hit`:          "4",
		`miss`:         "1",
		`size`:         "1",
		`samplers`:     "1",
		`refresh_runs`: "37", // 75/2
		`refresh`:      "1",
	}
	if diff := helpers.Diff(gotMetrics, expectedMetrics); diff != "" {
		t.Fatalf("Metrics (-got, +want):\n%s", diff)
	}
}

func TestConfigCheck(t *testing.T) {
	t.Run("refresh", func(t *testing.T) {
		configuration := DefaultConfiguration
		configuration.CacheDuration = 10 * time.Minute
		configuration.CacheRefresh = 5 * time.Minute
		configuration.CacheCheckInterval = time.Minute
		if _, err := New(reporter.NewMock(t), configuration, Dependencies{Daemon: daemon.NewMock(t)}); err == nil {
			t.Fatal("New() should trigger an error")
		}
	})
	t.Run("interval", func(t *testing.T) {
		configuration := DefaultConfiguration
		configuration.CacheDuration = 10 * time.Minute
		configuration.CacheRefresh = 15 * time.Minute
		configuration.CacheCheckInterval = 12 * time.Minute
		if _, err := New(reporter.NewMock(t), configuration, Dependencies{Daemon: daemon.NewMock(t)}); err == nil {
			t.Fatal("New() should trigger an error")
		}
	})
	t.Run("refresh disabled", func(t *testing.T) {
		configuration := DefaultConfiguration
		configuration.CacheDuration = 10 * time.Minute
		configuration.CacheRefresh = 0
		configuration.CacheCheckInterval = 2 * time.Minute
		if _, err := New(reporter.NewMock(t), configuration, Dependencies{Daemon: daemon.NewMock(t)}); err != nil {
			t.Fatalf("New() error:\n%+v", err)
		}
	})
}

func TestStartStopWithMultipleWorkers(t *testing.T) {
	r := reporter.NewMock(t)
	configuration := DefaultConfiguration
	configuration.Workers = 5
	c := NewMock(t, r, configuration, Dependencies{Daemon: daemon.NewMock(t)})
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop() error:\n%+v", err)
	}
}

type forceCoaelescePoller struct {
	accept   chan bool
	accepted []lookupRequest
}

func (fcp *forceCoaelescePoller) Poll(ctx context.Context, samplerIP string, _ uint16, _ string, ifIndex uint) {
	select {
	case <-ctx.Done():
		return
	case <-fcp.accept:
		fcp.accepted = append(fcp.accepted, lookupRequest{samplerIP, []uint{ifIndex}})
	}
}

func TestCoaelescing(t *testing.T) {
	r := reporter.NewMock(t)
	c := NewMock(t, r, DefaultConfiguration, Dependencies{Daemon: daemon.NewMock(t)})
	defer func() {
		if err := c.Stop(); err != nil {
			t.Fatalf("Stop() error:\n%+v", err)
		}
	}()
	fcp := &forceCoaelescePoller{
		accept:   make(chan bool),
		accepted: []lookupRequest{},
	}
	c.poller = fcp

	expectSNMPLookup(t, c, "127.0.0.1", 765, answer{Err: ErrCacheMiss})
	time.Sleep(10 * time.Millisecond)
	// dispatcher is now blocked, queue requests
	expectSNMPLookup(t, c, "127.0.0.1", 766, answer{Err: ErrCacheMiss})
	expectSNMPLookup(t, c, "127.0.0.1", 767, answer{Err: ErrCacheMiss})
	expectSNMPLookup(t, c, "127.0.0.1", 768, answer{Err: ErrCacheMiss})
	expectSNMPLookup(t, c, "127.0.0.1", 769, answer{Err: ErrCacheMiss})
	fcp.accept <- true
	time.Sleep(10 * time.Millisecond)

	gotMetrics := r.GetMetrics("akvorado_snmp_poller_", "coalesced_count")
	expectedMetrics := map[string]string{
		`coalesced_count`: "4",
	}
	if diff := helpers.Diff(gotMetrics, expectedMetrics); diff != "" {
		t.Fatalf("Metrics (-got, +want):\n%s", diff)
	}

	// TODO: check we have accepted the 4 requests at the same time
}
