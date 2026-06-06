package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type LocalScheduledTask struct {
	ID       string
	Task     string
	Interval time.Duration
	Repeat   bool
	ticker   *time.Ticker
	stop     chan struct{}
}

type localScheduler struct {
	mu      sync.Mutex
	tasks   map[string]*LocalScheduledTask
	counter int
	eventCh chan<- Event
}

var localSched = &localScheduler{
	tasks: make(map[string]*LocalScheduledTask),
}

func InitLocalScheduler(eventCh chan<- Event) {
	localSched.mu.Lock()
	defer localSched.mu.Unlock()
	localSched.eventCh = eventCh
}

func AddLocalTask(task string, interval time.Duration, repeat bool) string {
	localSched.mu.Lock()
	defer localSched.mu.Unlock()

	if localSched.eventCh == nil {
		return ""
	}

	localSched.counter++
	id := fmt.Sprintf("task_%d", localSched.counter)

	t := &LocalScheduledTask{
		ID:       id,
		Task:     task,
		Interval: interval,
		Repeat:   repeat,
		ticker:   time.NewTicker(interval),
		stop:     make(chan struct{}),
	}
	localSched.tasks[id] = t

	go runLocalTask(t)
	return id
}

func CancelLocalTask(id string) error {
	localSched.mu.Lock()
	defer localSched.mu.Unlock()

	t, ok := localSched.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	close(t.stop)
	t.ticker.Stop()
	delete(localSched.tasks, id)
	return nil
}

func ListLocalTasks() []LocalScheduledTask {
	localSched.mu.Lock()
	defer localSched.mu.Unlock()

	result := make([]LocalScheduledTask, 0, len(localSched.tasks))
	for _, t := range localSched.tasks {
		result = append(result, *t)
	}
	return result
}

func runLocalTask(t *LocalScheduledTask) {
	for {
		select {
		case <-t.stop:
			return
		case <-t.ticker.C:
			localSched.mu.Lock()
			ch := localSched.eventCh
			localSched.mu.Unlock()

			if ch == nil {
				return
			}

			ch <- Event{
				Type:    EventTypeUserMessage,
				Payload: fmt.Sprintf("⏰ %s", t.Task),
			}

			if !t.Repeat {
				localSched.mu.Lock()
				t.ticker.Stop()
				delete(localSched.tasks, t.ID)
				localSched.mu.Unlock()
				return
			}
		}
	}
}

func formatLocalTaskList(tasks []LocalScheduledTask) string {
	if len(tasks) == 0 {
		return "No scheduled tasks."
	}
	var b strings.Builder
	for _, t := range tasks {
		repeat := "one-shot"
		if t.Repeat {
			repeat = "repeating"
		}
		fmt.Fprintf(&b, "- [%s] %s (every %s, %s)\n", t.ID, t.Task, t.Interval, repeat)
	}
	return b.String()
}
