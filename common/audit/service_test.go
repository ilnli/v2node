package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRetryKeepsIdentityAndStopPersists(t *testing.T) {
	dir := t.TempDir()
	calls := make(chan Batch, 8)
	fail := make(chan struct{})
	send := func(ctx context.Context, b Batch) error {
		calls <- b
		select {
		case <-fail:
			return nil
		default:
			return errors.New("offline")
		}
	}
	s, err := NewService(Options{Directory: dir, MemoryBytes: 1024, DiskBytes: 65536}, map[string]Sender{"node-a": send}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Record("node-a", sampleBatch("x").Records[0]) {
		t.Fatal("rejected")
	}
	first := receive(t, calls)
	second := receive(t, calls)
	if first.ID == "" || first.ID != second.ID || len(first.Records) != 1 {
		t.Fatal("retry identity lost")
	}
	close(fail)
	deadline := time.Now().Add(3 * time.Second)
	for s.Stats().Sent == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Stats().Sent != 1 {
		t.Fatal("not acknowledged")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	q, err := Open(Options{Directory: dir, MemoryBytes: 1024, DiskBytes: 65536})
	if err != nil {
		t.Fatal(err)
	}
	if q.Stats().Batches != 0 {
		t.Fatal("success left spool files")
	}
	q.Close()
}

func TestConcurrentCollectionAndShutdown(t *testing.T) {
	dir := t.TempDir()
	s, err := NewService(Options{Directory: dir, MemoryBytes: 8192, DiskBytes: 1 << 20}, map[string]Sender{"node-a": func(ctx context.Context, b Batch) error { <-ctx.Done(); return ctx.Err() }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Record("node-a", sampleBatch("x").Records[0])
			}
		}()
	}
	wg.Wait()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Record("node-a", sampleBatch("x").Records[0]) {
		t.Fatal("accepted after close")
	}
	q, err := Open(Options{Directory: dir, MemoryBytes: 8192, DiskBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	var count int
	for {
		b, ok := q.Peek()
		if !ok {
			break
		}
		count += len(b.Records)
		if err = q.Ack(b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if count != 800 {
		t.Fatalf("graceful shutdown lost data: %d", count)
	}
}

func receive(t *testing.T, ch <-chan Batch) Batch {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(3 * time.Second):
		t.Fatal("no upload")
		return Batch{}
	}
}

func TestSaturatedSpoolDropsNewRecords(t *testing.T) {
	dir := t.TempDir()
	started := make(chan Batch, 1)
	s, err := NewService(Options{Directory: dir, MemoryBytes: 1, DiskBytes: 16384}, map[string]Sender{"node-a": func(ctx context.Context, b Batch) error { started <- b; <-ctx.Done(); return ctx.Err() }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		r := sampleBatch("x").Records[0]
		r.UserID = i + 1
		s.Record("node-a", r)
	}
	b := receive(t, started)
	if b.Records[0].UserID != 1 {
		t.Fatal("wrong first batch")
	}
	for i := 0; i < 64; i++ {
		r := sampleBatch("x").Records[0]
		r.UserID = 1000 + i
		s.Record("node-a", r)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Stats().Dropped != 64 {
		t.Fatalf("wrong drop count: %+v", s.Stats())
	}
	q, err := Open(Options{Directory: dir, MemoryBytes: 1, DiskBytes: 16384})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	b, ok := q.Peek()
	if !ok || b.Records[0].UserID != 1 || len(b.Records) != 64 {
		t.Fatal("new records replaced oldest", b)
	}
}
