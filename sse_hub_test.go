package main

import (
	"encoding/json"
	"testing"
)

func TestSSESlowConsumerReceivesFinalFrame(t *testing.T) {
	h := newSSEHub()
	ch := h.subscribe()
	defer h.unsubscribe(ch)

	const burst = 64
	for i := 0; i < burst; i++ {
		h.broadcast(sseEvent{Type: "speedtest", Reason: "progress"})
	}
	h.broadcast(sseEvent{Type: "speedtest", Reason: "final"})

	var last string
	for {
		select {
		case payload, ok := <-ch:
			if !ok {
				t.Fatal("client channel closed")
			}
			var ev sseEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Fatal(err)
			}
			last = ev.Reason
		default:
			if last != "final" {
				t.Fatalf("last frame = %q", last)
			}
			return
		}
	}
}
