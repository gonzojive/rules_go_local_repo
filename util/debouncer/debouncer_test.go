package debouncer

import (
	"context"
	"testing"
	"time"
)

func TestDebouncer_Wait1(t *testing.T) {
	d := NewDebouncer(time.Millisecond * 500)

	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 20)
		d.Trigger()
	}
	time.Sleep(time.Millisecond * 706)
	d.Trigger()

	count := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*4)
	defer cancel()
	if err := d.Listen(ctx, func() error {
		count++
		return nil
	}); err != context.DeadlineExceeded {
		t.Errorf("unexpected failure: %v", err)
	}
	if got, want := count, 2; got != want {
		t.Errorf("got count = %d, want %d", got, want)
	}
}
