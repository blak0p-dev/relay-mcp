// Package output retains terminal output for independent readers.
package output

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultCapacity bounds retained terminal output to the newest 1 MiB.
	DefaultCapacity = 1 << 20

	// DefaultReadBytes is the normal maximum returned by one snapshot.
	DefaultReadBytes = 32 << 10

	// MaxReadBytes is the largest snapshot the broker will return at once.
	MaxReadBytes = 64 << 10

	// DefaultWait is the bounded wait used by an empty snapshot request.
	DefaultWait = 100 * time.Millisecond

	// MaxWait is the longest an empty snapshot may wait for a state change.
	MaxWait = 5 * time.Second
)

// Status describes the terminal lifecycle state observed with retained output.
type Status string

const (
	// StatusRunning means the terminal may still produce output.
	StatusRunning Status = "running"
	// StatusExited means the terminal completed normally.
	StatusExited Status = "exited"
	// StatusError means the terminal ended with an error.
	StatusError Status = "error"
	// StatusClosed means the terminal was explicitly closed.
	StatusClosed Status = "closed"
)

// Snapshot is an immutable range of retained terminal output.
type Snapshot struct {
	Output       []byte
	Cursor       int64
	NextCursor   int64
	DroppedBytes int64
	Status       Status
}

// Broker retains a bounded tail of terminal output and wakes waiting readers
// whenever output or terminal state changes.
type Broker struct {
	capacity int
	buffer   []byte
	head     int
	size     int

	oldestCursor int64
	nextCursor   int64
	status       Status

	mu      sync.Mutex
	changed chan struct{}
}

// New constructs a broker with the given retained-byte capacity. A non-positive
// capacity uses DefaultCapacity.
func New(capacity int) *Broker {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Broker{
		capacity: capacity,
		buffer:   make([]byte, capacity),
		status:   StatusRunning,
		changed:  make(chan struct{}),
	}
}

// Append retains output in byte order, evicting only the oldest bytes needed to
// keep the configured capacity.
func (b *Broker) Append(output []byte) {
	if len(output) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(output) >= b.capacity {
		copy(b.buffer, output[len(output)-b.capacity:])
		b.nextCursor += int64(len(output))
		b.oldestCursor = b.nextCursor - int64(b.capacity)
		b.head = 0
		b.size = b.capacity
		b.notifyLocked()
		return
	}

	free := b.capacity - b.size
	if overflow := len(output) - free; overflow > 0 {
		b.head = (b.head + overflow) % b.capacity
		b.size -= overflow
		b.oldestCursor += int64(overflow)
	}

	b.copyIntoRingLocked(output)
	b.size += len(output)
	b.nextCursor += int64(len(output))
	b.notifyLocked()
}

// SetStatus records a terminal lifecycle transition and wakes waiting readers.
func (b *Broker) SetStatus(status Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status == status {
		return
	}
	b.status = status
	b.notifyLocked()
}

// Snapshot returns retained output beginning at cursor. It waits only while
// output is unavailable, the terminal is running, and wait is positive.
func (b *Broker) Snapshot(ctx context.Context, cursor int64, maxBytes int, wait time.Duration) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	maxBytes = normalizedMaxBytes(maxBytes)
	if wait <= 0 {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.snapshotLocked(cursor, maxBytes), nil
	}

	deadline := time.Now().Add(wait)
	for {
		b.mu.Lock()
		snapshot := b.snapshotLocked(cursor, maxBytes)
		changed := b.changed
		b.mu.Unlock()

		if len(snapshot.Output) > 0 || snapshot.Status != StatusRunning {
			return snapshot, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return snapshot, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return Snapshot{}, ctx.Err()
		case <-changed:
			stopTimer(timer)
		case <-timer.C:
			return snapshot, nil
		}
	}
}

func normalizedMaxBytes(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultReadBytes
	}
	if maxBytes > MaxReadBytes {
		return MaxReadBytes
	}
	return maxBytes
}

func (b *Broker) snapshotLocked(cursor int64, maxBytes int) Snapshot {
	usedCursor := cursor
	droppedBytes := int64(0)
	if usedCursor < b.oldestCursor {
		droppedBytes = b.oldestCursor - usedCursor
		usedCursor = b.oldestCursor
	}
	if usedCursor > b.nextCursor {
		usedCursor = b.nextCursor
	}

	available := b.nextCursor - usedCursor
	count := int64(maxBytes)
	if available < count {
		count = available
	}
	output := make([]byte, count)
	if count > 0 {
		offset := int(usedCursor - b.oldestCursor)
		start := (b.head + offset) % b.capacity
		first := min(int(count), b.capacity-start)
		copy(output, b.buffer[start:start+first])
		copy(output[first:], b.buffer[:int(count)-first])
	}

	return Snapshot{
		Output:       output,
		Cursor:       usedCursor,
		NextCursor:   usedCursor + count,
		DroppedBytes: droppedBytes,
		Status:       b.status,
	}
}

func (b *Broker) copyIntoRingLocked(output []byte) {
	start := (b.head + b.size) % b.capacity
	first := min(len(output), b.capacity-start)
	copy(b.buffer[start:start+first], output[:first])
	copy(b.buffer[:len(output)-first], output[first:])
}

func (b *Broker) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
