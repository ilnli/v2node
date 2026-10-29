// Package trafficqueue retains immutable traffic deltas until the panel durably
// acknowledges them. It is independent from the audit spool and its budgets.
package trafficqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	panel "github.com/wyx2685/v2node/api/v2board"
)

const MaxBytes = 4 << 20

type Batch struct {
	ID      string              `json:"id"`
	Traffic []panel.UserTraffic `json:"traffic"`
}

type Queue struct {
	directory string
	mu        sync.Mutex
	batches   []Batch
	dirty     bool
}

type Sender func(context.Context, []panel.UserTraffic, string) error

func Open(directory string) (*Queue, error) {
	if directory == "" {
		return nil, fmt.Errorf("traffic queue directory is required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	q := &Queue{directory: directory}
	// Interrupted atomic writes are not queued batches. Keep the dedicated
	// directory bounded across repeated process crashes during publication.
	leftovers, err := filepath.Glob(filepath.Join(directory, ".traffic-*"))
	if err != nil {
		return nil, err
	}
	for _, name := range leftovers {
		if err := os.Remove(name); err != nil {
			return nil, err
		}
	}
	file, err := os.Open(filepath.Join(directory, "pending.json"))
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("traffic queue exceeds %d bytes", MaxBytes)
	}
	if err := json.Unmarshal(data, &q.batches); err != nil {
		return nil, fmt.Errorf("load pending traffic: %w", err)
	}
	for _, batch := range q.batches {
		if batch.ID == "" || len(batch.ID) > 128 {
			return nil, fmt.Errorf("invalid pending traffic batch ID")
		}
	}
	return q, nil
}

func (q *Queue) Append(traffic []panel.UserTraffic) error {
	if len(traffic) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	q.batches = append(q.batches, Batch{ID: hex.EncodeToString(token[:]), Traffic: append([]panel.UserTraffic(nil), traffic...)})
	q.dirty = true
	// Retain the snapshot in memory if storage fails. The owner must stop taking
	// new snapshots until Drain persists it, and report any shutdown write error.
	if err := q.save(q.batches); err != nil {
		return err
	}
	q.dirty = false
	return nil
}

func (q *Queue) Drain(ctx context.Context, send Sender) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.dirty {
		if err := q.save(q.batches); err != nil {
			return err
		}
		q.dirty = false
	}
	for len(q.batches) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := q.batches[0]
		if err := send(ctx, batch.Traffic, batch.ID); err != nil {
			return err
		}
		remaining := q.batches[1:]
		if err := q.save(remaining); err != nil {
			return err
		}
		q.batches = remaining
	}
	return nil
}

func (q *Queue) save(batches []Batch) error {
	data, err := json.Marshal(batches)
	if err != nil {
		return err
	}
	if len(data) > MaxBytes {
		return fmt.Errorf("traffic queue exceeds %d bytes; pending traffic retained", MaxBytes)
	}
	file, err := os.CreateTemp(q.directory, ".traffic-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(q.directory, "pending.json")); err != nil {
		return err
	}
	directory, err := os.Open(q.directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
