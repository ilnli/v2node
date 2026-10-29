package core

import (
	"sync"
	"testing"

	"github.com/wyx2685/v2node/common/counter"
	"github.com/wyx2685/v2node/core/app/dispatcher"
)

func trafficTestCore() (*V2Core, *counter.TrafficStorage) {
	d := &dispatcher.DefaultDispatcher{}
	counters := counter.NewTrafficCounter()
	d.Counter.Store("node", counters)
	return &V2Core{users: &UserMap{uidMap: map[string]int{"user": 42}}, dispatcher: d}, counters.GetCounter("user")
}

func TestTrafficExtractionDoesNotLoseConcurrentIncrements(t *testing.T) {
	core, storage := trafficTestCore()
	const workers, iterations = 4, 100000
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				storage.UpCounter.Add(1)
				storage.DownCounter.Add(2)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	var up, down int64
	drain := func() {
		rows, err := core.GetUserTrafficSlice("node", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			up += row.Upload
			down += row.Download
		}
	}
loop:
	for {
		drain()
		select {
		case <-done:
			break loop
		default:
		}
	}
	drain()
	if up != workers*iterations || down != workers*iterations*2 {
		t.Fatalf("lost concurrent traffic: got upload=%d download=%d, expected %d/%d", up, down, workers*iterations, workers*iterations*2)
	}
}

func TestFinalTrafficExtractionIncludesBelowThresholdBytes(t *testing.T) {
	core, storage := trafficTestCore()
	storage.UpCounter.Add(10)
	storage.DownCounter.Add(20)
	rows, _ := core.GetUserTrafficSlice("node", 1)
	if len(rows) != 0 {
		t.Fatal("periodic threshold ignored")
	}
	rows, _ = core.GetUserTrafficSlice("node", 0)
	if len(rows) != 1 || rows[0].Upload != 10 || rows[0].Download != 20 {
		t.Fatalf("final flush lost small counters: %+v", rows)
	}
}
