package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Sender func(context.Context, Batch) error
type event struct {
	target string
	record Record
}

type Stats struct {
	QueueStats
	Received uint64
	Dropped  uint64
	Sent     uint64
}

type Service struct {
	queue                   *Queue
	targets                 map[string]Sender
	input                   chan event
	mu                      sync.RWMutex
	closed                  bool
	once                    sync.Once
	closeErr                error
	cancel                  context.CancelFunc
	writerDone, senderDone  chan struct{}
	received, dropped, sent atomic.Uint64
	log                     func(string, ...any)
}

func NewService(o Options, targets map[string]Sender, logger func(string, ...any)) (*Service, error) {
	if len(targets) == 0 || len(targets) > 256 {
		return nil, errors.New("audit requires 1..256 configured nodes")
	}
	copied := make(map[string]Sender, len(targets))
	for k, v := range targets {
		if k == "" || len(k) > 64 || v == nil {
			return nil, errors.New("invalid audit sender")
		}
		copied[strings.Clone(k)] = v
	}
	q, err := Open(o)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{queue: q, targets: copied, input: make(chan event, 4096), cancel: cancel, writerDone: make(chan struct{}), senderDone: make(chan struct{}), log: logger}
	go s.collect()
	go s.upload(ctx)
	return s, nil
}

// Record never performs disk or network I/O. False means the new event was
// rejected. A later queue-capacity/disk failure is reflected in Dropped as well.
func (s *Service) Record(target string, r Record) bool {
	s.received.Add(1)
	r, ok := r.Clone()
	if !ok || s.targets[target] == nil {
		s.dropped.Add(1)
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.dropped.Add(1)
		return false
	}
	select {
	case s.input <- event{target: strings.Clone(target), record: r}:
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

func (s *Service) warn(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}

func (s *Service) collect() {
	defer close(s.writerDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	records := make([]Record, 0, MaxRecords)
	var target string
	flush := func() {
		if len(records) == 0 {
			return
		}
		b, err := newBatch(target, records)
		if err != nil || !s.queue.Add(b) {
			s.dropped.Add(uint64(len(records)))
		}
		clear(records)
		records = records[:0]
	}
	var lastDropped uint64
	var ticks int
	for {
		select {
		case e, ok := <-s.input:
			if !ok {
				flush()
				return
			}
			if len(records) > 0 && target != e.target {
				flush()
			}
			target = e.target
			records = append(records, e.record)
			if len(records) == MaxRecords {
				flush()
			}
		case <-ticker.C:
			flush()
			ticks++
			if ticks%30 == 0 {
				stats := s.Stats()
				if stats.Dropped != lastDropped {
					s.warn("audit dropped %d new records (total %d); memory=%d disk=%d queued_batches=%d; last_error=%s", stats.Dropped-lastDropped, stats.Dropped, stats.MemoryBytes, stats.DiskBytes, stats.Batches, stats.LastError)
					lastDropped = stats.Dropped
				}
			}
		}
	}
}

func (s *Service) upload(ctx context.Context) {
	defer close(s.senderDone)
	retry := time.Second
	var lastReadError string
	for {
		if ctx.Err() != nil {
			return
		}
		b, ok := s.queue.Peek()
		if !ok {
			stats := s.queue.Stats()
			if stats.Batches > 0 && stats.LastError != "" && stats.LastError != lastReadError {
				s.warn("audit spool read pending: %s", stats.LastError)
				lastReadError = stats.LastError
			}
			select {
			case <-ctx.Done():
				return
			case <-s.queue.notify:
			case <-time.After(time.Second):
			}
			continue
		}
		send := s.targets[b.Target]
		var err error
		if send == nil {
			err = errors.New("audit batch belongs to an unconfigured panel/node; restore its configuration to drain it")
		} else {
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = send(sendCtx, b)
			cancel()
		}
		if err == nil {
			err = s.queue.Ack(b.ID)
			if err == nil {
				s.sent.Add(uint64(len(b.Records)))
				retry = time.Second
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.warn("audit upload pending: %v; retry in %s", err, retry)
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		retry = min(30*time.Second, retry*2)
	}
}

func (s *Service) Stats() Stats {
	return Stats{QueueStats: s.queue.Stats(), Received: s.received.Load(), Dropped: s.dropped.Load(), Sent: s.sent.Load()}
}

func (s *Service) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.input)
		s.mu.Unlock()
		s.cancel() // Stop even a slow upload before flushing the final memory backlog.
		<-s.writerDone
		<-s.senderDone
		s.closeErr = s.queue.Close()
		if n := s.dropped.Load(); n > 0 {
			s.warn("audit stopped; dropped_new_records=%d", n)
		}
	})
	return s.closeErr
}
