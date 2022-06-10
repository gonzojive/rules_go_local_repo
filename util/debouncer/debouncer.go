package debouncer

import (
	"context"
	"sync"
	"time"
)

type lockedObj[T any] struct {
	obj  T
	lock sync.Mutex
}

func (o *lockedObj[T]) get() T {
	var out T
	return out
}

func (o *lockedObj[T]) with(fn func(obj T, update func(newVal T))) {
	o.lock.Lock()
	defer o.lock.Unlock()
	fn(o.obj, func(newVal T) { o.obj = newVal })
}

// Debouncer is a name taken from JavaScript Reactive programming. It allows
// firing many events in succession but only triggering one output event.
type Debouncer struct {
	delay time.Duration

	lastTriggered lockedObj[time.Time]

	triggerTimes chan time.Time
}

// NewDebouncer creates a new debouncer that ensures at least [delay] time
// period has occurred before triggering an output event.
func NewDebouncer(delay time.Duration) *Debouncer {
	return &Debouncer{
		delay,
		lockedObj[time.Time]{},
		make(chan time.Time, 100),
	}
}

// Trigger is the input event that causes a debounce action to occur (in wait).
func (d *Debouncer) Trigger() {
	now := time.Now()
	d.lastTriggered.with(func(obj time.Time, update func(time.Time)) {
		update(now)
	})
	select {
	case d.triggerTimes <- now:
	default:
	}
}

// Listen calls action based on the trigger events.
func (d *Debouncer) Listen(ctx context.Context, action func() error) error {
	consumeAllTriggerTimes := func() {
		for {
			select {
			case <-d.triggerTimes:
			default:
				return
			}
		}
	}
	timer := time.NewTimer(time.Hour * 1000 * 1000)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			// Ensures enough time has actually passed.
		case <-d.triggerTimes:
		}
		consumeAllTriggerTimes()
		sinceTrigger := time.Since(d.lastTriggered.get())
		remaining := d.delay - sinceTrigger
		if remaining <= 0 {
			if err := action(); err != nil {
				return err
			}
		} else {
			timer.Reset(remaining)
		}
	}

}

// SleepContext sleeps for the provided duration unless the context is cancelled
// first. An error is returned if the context is cancelled.
func SleepContext(ctx context.Context, dur time.Duration) error {
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SleepContextOrInterrupt sleeps for a given duration unless interrupted by a
// message on the given channel.
func SleepContextOrInterrupt[T any](ctx context.Context, dur time.Duration, ch <-chan T) (*T, error) {
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	case val := <-ch:
		return &val, nil
	}
}
