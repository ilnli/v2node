package task

import (
	"context"
	"testing"
	"time"
)

func TestCloseCancelsAndJoinsCallback(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	task := &Task{Name: "shutdown", Interval: time.Hour, Execute: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	}}
	if err := task.Start(true); err != nil {
		t.Fatal(err)
	}
	<-started
	stopped := make(chan struct{})
	go func() { task.Close(); close(stopped) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Close did not cancel the running callback")
	}
	select {
	case <-stopped:
		close(release)
		t.Fatal("Close returned while callback was still running")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Close did not join callback")
	}
	task.Close()
}

func TestTimeoutRequestsReloadWithoutOverlappingCallbacks(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	reload := make(chan struct{}, 1)
	task := &Task{Name: "timeout", Interval: 10 * time.Millisecond, ReloadCh: reload, Execute: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}}
	if err := task.Start(true); err != nil {
		t.Fatal(err)
	}
	<-started
	select {
	case <-reload:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("timeout did not request reload")
	}
	done := make(chan struct{})
	go func() { task.Close(); close(done) }()
	select {
	case <-done:
		close(release)
		t.Fatal("Close returned before timed-out callback ended")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed-out callback was not joined")
	}
}
