package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Task struct {
	Name     string
	Interval time.Duration
	Execute  func(context.Context) error
	Access   sync.RWMutex
	Running  bool
	ReloadCh chan struct{}
	Stop     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
}

func (t *Task) Start(first bool) error {
	t.Access.Lock()
	defer t.Access.Unlock()
	if t.Running {
		return nil
	}
	if t.Interval <= 0 || t.Execute == nil {
		return fmt.Errorf("task %s requires an interval and callback", t.Name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.Stop = make(chan struct{})
	t.done = make(chan struct{})
	done, stop := t.done, t.Stop
	t.Running = true
	go func() {
		defer func() {
			cancel()
			t.Access.Lock()
			t.Running = false
			close(done)
			t.Access.Unlock()
		}()
		if first {
			if err := t.executeWithTimeout(ctx); err != nil {
				log.WithError(err).Errorf("Task %s execution failed", t.Name)
				return
			}
		}
		timer := time.NewTimer(t.Interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
			}
			if err := t.executeWithTimeout(ctx); err != nil {
				log.WithError(err).Errorf("Task %s execution failed", t.Name)
				return
			}
			timer.Reset(t.Interval)
		}
	}()
	return nil
}

func (t *Task) ExecuteWithTimeout() error { return t.executeWithTimeout(context.Background()) }

func (t *Task) executeWithTimeout(parent context.Context) error {
	if parent.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, min(5*t.Interval, 5*time.Minute))
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- t.Execute(ctx) }()
	var err error
	select {
	case err = <-finished:
	case <-ctx.Done():
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			log.Errorf("Task %s execution timed out, requesting reload", t.Name)
			if t.ReloadCh != nil {
				select {
				case t.ReloadCh <- struct{}{}:
				default:
				}
			}
		}
		// Cancellation is cooperative. Never start another callback or allow the
		// owner to destroy its resources while this callback still uses them.
		err = <-finished
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func (t *Task) Close() {
	t.Access.Lock()
	done := t.done
	if t.Running && t.cancel != nil {
		t.cancel()
		select {
		case <-t.Stop:
		default:
			close(t.Stop)
		}
	}
	t.Access.Unlock()
	if done != nil {
		<-done
	}
	log.Debugf("Task %s stopped", t.Name)
}
