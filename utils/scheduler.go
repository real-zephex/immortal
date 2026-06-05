package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type TelegramEventType string

const (
	TelegramUserMessage   TelegramEventType = "user_msg"
	TelegramScheduledTask TelegramEventType = "scheduled"
)

type TelegramEvent struct {
	Type    TelegramEventType
	ChatID  int64
	Channel string
	Payload string
}

type ScheduledTask struct {
	ID         string
	ChannelKey string
	ChatID     int64
	Task       string
	Interval   time.Duration
	Repeat     bool
	ticker     *time.Ticker
	stop       chan struct{}
}

type scheduler struct {
	mu       sync.Mutex
	tasks    map[string]*ScheduledTask
	counter  int
	eventCh  chan TelegramEvent
}

var sched = &scheduler{
	tasks:   make(map[string]*ScheduledTask),
}

func InitScheduler(eventCh chan TelegramEvent) {
	sched.mu.Lock()
	defer sched.mu.Unlock()
	sched.eventCh = eventCh
}

func AddTask(channelKey string, chatID int64, task string, interval time.Duration, repeat bool) string {
	sched.mu.Lock()
	defer sched.mu.Unlock()

	if sched.eventCh == nil {
		return ""
	}

	sched.counter++
	id := fmt.Sprintf("task_%d", sched.counter)

	t := &ScheduledTask{
		ID:         id,
		ChannelKey: channelKey,
		ChatID:     chatID,
		Task:       task,
		Interval:   interval,
		Repeat:     repeat,
		ticker:     time.NewTicker(interval),
		stop:       make(chan struct{}),
	}
	sched.tasks[id] = t

	go runTask(t)
	return id
}

func CancelTask(id string) error {
	sched.mu.Lock()
	defer sched.mu.Unlock()

	t, ok := sched.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	close(t.stop)
	t.ticker.Stop()
	delete(sched.tasks, id)
	return nil
}

func ListTasks(channelKey string) []ScheduledTask {
	sched.mu.Lock()
	defer sched.mu.Unlock()

	var result []ScheduledTask
	for _, t := range sched.tasks {
		if t.ChannelKey == channelKey {
			result = append(result, *t)
		}
	}
	return result
}

func runTask(t *ScheduledTask) {
	for {
		select {
		case <-t.stop:
			return
		case <-t.ticker.C:
			sched.mu.Lock()
			ch := sched.eventCh
			sched.mu.Unlock()

			if ch == nil {
				return
			}

			ch <- TelegramEvent{
				Type:    TelegramScheduledTask,
				ChatID:  t.ChatID,
				Channel: t.ChannelKey,
				Payload: t.Task,
			}

			if !t.Repeat {
				sched.mu.Lock()
				t.ticker.Stop()
				delete(sched.tasks, t.ID)
				sched.mu.Unlock()
				return
			}
		}
	}
}

func parseInterval(s string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "daily":
		return 24 * time.Hour, nil
	case "hourly":
		return time.Hour, nil
	case "weekly":
		return 7 * 24 * time.Hour, nil
	case "minutely":
		return time.Minute, nil
	}
	return time.ParseDuration(s)
}

func removeTask(id string) {
	sched.mu.Lock()
	defer sched.mu.Unlock()
	delete(sched.tasks, id)
}

func formatTaskList(tasks []ScheduledTask) string {
	if len(tasks) == 0 {
		return "No scheduled tasks."
	}
	var b strings.Builder
	for _, t := range tasks {
		repeat := "one-shot"
		if t.Repeat {
			repeat = "repeating"
		}
		b.WriteString(fmt.Sprintf("- [%s] %s (every %s, %s)\n", t.ID, t.Task, t.Interval, repeat))
	}
	return b.String()
}
