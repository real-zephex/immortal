package utils

type EventType string

const EventTypeUserMessage EventType = "user_message"

type Event struct {
	Type    EventType `json:"event"`
	Payload any
}
