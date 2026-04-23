package main

func (d db) publish(kind string, payload map[string]any) {
	if d.publisher == nil {
		return
	}
	d.publisher.Publish(event{Kind: kind, Payload: payload})
}
