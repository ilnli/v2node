package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Options struct {
	Directory   string
	MemoryBytes int64
	DiskBytes   int64
}

type QueueStats struct {
	MemoryBytes   int64
	DiskBytes     int64
	ReservedBytes int64
	Batches       int
	LastError     string
}

type entry struct {
	sequence uint64
	id       string
	data     []byte
	size     int64
	charge   int64
}

// Queue owns a bounded FIFO of immutable batches. Disk reservations include
// memory batches, so graceful shutdown can persist them without evicting old data.
// Only the service workers call disk methods, never the proxy dispatcher.
type Queue struct {
	mu                     sync.Mutex
	options                Options
	items                  []entry
	head, count            int
	next                   uint64
	memory, disk, reserved int64
	lastError              string
	closed                 bool
	lock                   *os.File
	notify                 chan struct{}
}

func Open(o Options) (*Queue, error) {
	if o.MemoryBytes == 0 {
		o.MemoryBytes = DefaultMemoryBytes
	}
	if o.DiskBytes == 0 {
		o.DiskBytes = MaxDiskBytes
	}
	if o.Directory == "" || o.MemoryBytes < 1 || o.MemoryBytes > MaxMemoryBytes || o.DiskBytes < 8192 || o.DiskBytes > MaxDiskBytes {
		return nil, errors.New("invalid audit directory or capacity (memory <=128 MiB, disk <=1 GiB)")
	}
	if err := os.MkdirAll(o.Directory, 0700); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(o.Directory)
	if err != nil {
		return nil, err
	}
	q := &Queue{options: o, items: make([]entry, MaxEntries), next: 1, lock: lock, notify: make(chan struct{}, 1)}
	if err = q.recover(); err != nil {
		unlockDirectory(lock)
		return nil, err
	}
	return q, nil
}

// Include a data-block rounding and one extra 4 KiB allowance per file for metadata.
func diskCharge(size int64) int64 { return ((size+4095)/4096 + 1) * 4096 }

func (q *Queue) filename(n uint64) string {
	return filepath.Join(q.options.Directory, fmt.Sprintf("%020d.batch", n))
}

func (q *Queue) recover() error {
	dir, err := os.Open(q.options.Directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	var entries []entry
	for {
		files, readErr := dir.ReadDir(256)
		for _, f := range files {
			name := f.Name()
			if strings.HasSuffix(name, ".batch.tmp") && !f.IsDir() {
				if err := os.Remove(filepath.Join(q.options.Directory, name)); err != nil {
					return err
				}
				continue
			}
			if !strings.HasSuffix(name, ".batch") {
				continue
			}
			n, err := strconv.ParseUint(strings.TrimSuffix(name, ".batch"), 10, 64)
			if err != nil || n == 0 || f.IsDir() {
				return fmt.Errorf("invalid audit spool file %s", name)
			}
			info, err := f.Info()
			if err != nil {
				return err
			}
			if info.Size() <= 0 || info.Size() > MaxBatchBytes {
				return fmt.Errorf("invalid audit batch size: %s", name)
			}
			e := entry{sequence: n, size: info.Size(), charge: diskCharge(info.Size())}
			q.disk += e.charge
			q.reserved += e.charge
			if len(entries) >= MaxEntries || q.reserved > q.options.DiskBytes {
				return errors.New("existing audit spool exceeds configured limit; preserve or drain it before lowering limits")
			}
			entries = append(entries, e)
			if n >= q.next {
				q.next = n + 1
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return readErr
			}
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence < entries[j].sequence })
	copy(q.items, entries)
	q.count = len(entries)
	return nil
}

func (q *Queue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *Queue) Add(b Batch) bool {
	if len(b.Records) == 0 || len(b.Records) > MaxRecords || len(b.ID) > 64 || len(b.Target) > 64 {
		return false
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > MaxBatchBytes {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.count == len(q.items) {
		return false
	}
	e := entry{sequence: q.next, id: b.ID, data: data, size: int64(len(data)), charge: diskCharge(int64(len(data)))}
	if q.reserved+e.charge > q.options.DiskBytes {
		return false
	}
	if q.disk == 0 && q.memory+e.size <= q.options.MemoryBytes {
		q.memory += e.size
	} else {
		if err = q.persist(e); err != nil {
			q.lastError = err.Error()
			return false
		}
		e.data = nil
		q.disk += e.charge
	}
	q.items[(q.head+q.count)%len(q.items)] = e
	q.count++
	q.next++
	q.reserved += e.charge
	q.wake()
	return true
}

func (q *Queue) persist(e entry) error {
	name := q.filename(e.sequence)
	f, err := os.OpenFile(name+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(name + ".tmp")
	_, err = f.Write(e.data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name+".tmp", name)
}

func (q *Queue) Peek() (Batch, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 || q.closed {
		return Batch{}, false
	}
	e := &q.items[q.head]
	data := e.data
	if data == nil {
		var err error
		data, err = os.ReadFile(q.filename(e.sequence))
		if err != nil {
			q.lastError = err.Error()
			return Batch{}, false
		}
	}
	var b Batch
	if err := json.Unmarshal(data, &b); err != nil || len(b.Records) == 0 || len(b.Records) > MaxRecords || b.ID == "" || b.Target == "" {
		q.lastError = "invalid audit batch; retained for inspection"
		return Batch{}, false
	}
	e.id = b.ID
	return b, true
}

func (q *Queue) Ack(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 || q.items[q.head].id != id || id == "" {
		return errors.New("audit acknowledgment does not match queue head")
	}
	e := q.items[q.head]
	if e.data == nil {
		if err := os.Remove(q.filename(e.sequence)); err != nil {
			q.lastError = err.Error()
			return err
		}
		q.disk -= e.charge
	} else {
		q.memory -= e.size
	}
	q.reserved -= e.charge
	q.items[q.head] = entry{}
	q.head = (q.head + 1) % len(q.items)
	q.count--
	q.wake()
	return nil
}

func (q *Queue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return QueueStats{MemoryBytes: q.memory, DiskBytes: q.disk, ReservedBytes: q.reserved, Batches: q.count, LastError: q.lastError}
}

func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	var result error
	for i := 0; i < q.count; i++ {
		e := &q.items[(q.head+i)%len(q.items)]
		if e.data == nil {
			continue
		}
		if err := q.persist(*e); err != nil {
			result = errors.Join(result, err)
			continue
		}
		q.memory -= e.size
		q.disk += e.charge
		e.data = nil
	}
	return errors.Join(result, unlockDirectory(q.lock))
}
