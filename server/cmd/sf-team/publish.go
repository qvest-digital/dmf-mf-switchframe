package main

func (d db) publish(kind string, payload map[string]any) {
	var p publisherIface
	if d.pubRef != nil && d.pubRef.p != nil {
		p = d.pubRef.p
	} else if d.publisher != nil {
		p = d.publisher
	}
	if p == nil {
		return
	}
	p.Publish(event{Kind: kind, Payload: payload})
}
