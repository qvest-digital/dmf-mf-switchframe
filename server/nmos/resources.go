package nmos

import "sync"

// ResourceStore is a thread-safe store for NMOS IS-04 resources.
// All access is protected by an RWMutex.
type ResourceStore struct {
	mu        sync.RWMutex
	node      *Node
	devices   map[string]*Device
	sources   map[string]*Source
	flows     map[string]*Flow
	senders   map[string]*Sender
	receivers map[string]*Receiver
}

// NewResourceStore creates a ResourceStore with initialized empty maps.
func NewResourceStore() *ResourceStore {
	return &ResourceStore{
		devices:   make(map[string]*Device),
		sources:   make(map[string]*Source),
		flows:     make(map[string]*Flow),
		senders:   make(map[string]*Sender),
		receivers: make(map[string]*Receiver),
	}
}

// SetNode sets the node resource, bumping its version.
func (s *ResourceStore) SetNode(n *Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.Version = NewVersion()
	s.node = n
}

// Touch gives every resource a new version. A registry refuses a
// registration whose version is older than the one it holds, which is what
// another node presenting the same ids, started later, has registered.
func (s *ResourceStore) Touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.node != nil {
		s.node.Version = NewVersion()
	}
	for _, d := range s.devices {
		d.Version = NewVersion()
	}
	for _, src := range s.sources {
		src.Version = NewVersion()
	}
	for _, f := range s.flows {
		f.Version = NewVersion()
	}
	for _, snd := range s.senders {
		snd.Version = NewVersion()
	}
	for _, r := range s.receivers {
		r.Version = NewVersion()
	}
}

// GetNode returns a shallow copy of the node resource, or nil if unset.
func (s *ResourceStore) GetNode() *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.node == nil {
		return nil
	}
	copy := *s.node
	return &copy
}

// AddDevice adds or replaces a device, bumping its version.
func (s *ResourceStore) AddDevice(d *Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.Version = NewVersion()
	s.devices[d.ID] = d
}

// GetDevice returns a shallow copy of a device by ID, or nil if not found.
func (s *ResourceStore) GetDevice(id string) *Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.devices[id]
	if d == nil {
		return nil
	}
	copy := *d
	return &copy
}

// ListDevices returns shallow copies of all devices.
func (s *ResourceStore) ListDevices() []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		copy := *d
		result = append(result, &copy)
	}
	return result
}

// RemoveDevice removes a device by ID. No-op if not found.
func (s *ResourceStore) RemoveDevice(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devices, id)
}

// AddSource adds or replaces a source, bumping its version.
func (s *ResourceStore) AddSource(src *Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src.Version = NewVersion()
	s.sources[src.ID] = src
}

// GetSource returns a shallow copy of a source by ID, or nil if not found.
func (s *ResourceStore) GetSource(id string) *Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.sources[id]
	if src == nil {
		return nil
	}
	copy := *src
	return &copy
}

// ListSources returns shallow copies of all sources.
func (s *ResourceStore) ListSources() []*Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Source, 0, len(s.sources))
	for _, src := range s.sources {
		copy := *src
		result = append(result, &copy)
	}
	return result
}

// RemoveSource removes a source by ID. No-op if not found.
func (s *ResourceStore) RemoveSource(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, id)
}

// AddFlow adds or replaces a flow, bumping its version.
func (s *ResourceStore) AddFlow(f *Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.Version = NewVersion()
	s.flows[f.ID] = f
}

// GetFlow returns a shallow copy of a flow by ID, or nil if not found.
func (s *ResourceStore) GetFlow(id string) *Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.flows[id]
	if f == nil {
		return nil
	}
	copy := *f
	return &copy
}

// ListFlows returns shallow copies of all flows.
func (s *ResourceStore) ListFlows() []*Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Flow, 0, len(s.flows))
	for _, f := range s.flows {
		copy := *f
		result = append(result, &copy)
	}
	return result
}

// RemoveFlow removes a flow by ID. No-op if not found.
func (s *ResourceStore) RemoveFlow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.flows, id)
}

// AddSender adds or replaces a sender, bumping its version.
func (s *ResourceStore) AddSender(snd *Sender) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snd.Version = NewVersion()
	s.senders[snd.ID] = snd
}

// GetSender returns a shallow copy of a sender by ID, or nil if not found.
func (s *ResourceStore) GetSender(id string) *Sender {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snd := s.senders[id]
	if snd == nil {
		return nil
	}
	copy := *snd
	return &copy
}

// ListSenders returns shallow copies of all senders.
func (s *ResourceStore) ListSenders() []*Sender {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Sender, 0, len(s.senders))
	for _, snd := range s.senders {
		copy := *snd
		result = append(result, &copy)
	}
	return result
}

// RemoveSender removes a sender by ID. No-op if not found.
func (s *ResourceStore) RemoveSender(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.senders, id)
}

// AddReceiver adds or replaces a receiver, bumping its version.
func (s *ResourceStore) AddReceiver(r *Receiver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Version = NewVersion()
	s.receivers[r.ID] = r
}

// GetReceiver returns a shallow copy of a receiver by ID, or nil if not found.
func (s *ResourceStore) GetReceiver(id string) *Receiver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.receivers[id]
	if r == nil {
		return nil
	}
	copy := *r
	return &copy
}

// ListReceivers returns shallow copies of all receivers.
func (s *ResourceStore) ListReceivers() []*Receiver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Receiver, 0, len(s.receivers))
	for _, r := range s.receivers {
		copy := *r
		result = append(result, &copy)
	}
	return result
}

// RemoveReceiver removes a receiver by ID. No-op if not found.
func (s *ResourceStore) RemoveReceiver(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.receivers, id)
}

// UpdateReceiverSubscription updates a receiver's subscription and bumps
// its version. No-op if the receiver does not exist.
func (s *ResourceStore) UpdateReceiverSubscription(id string, senderID *string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.receivers[id]
	if !ok {
		return
	}
	r.Subscription.SenderID = senderID
	r.Subscription.Active = active
	r.Version = NewVersion()
}
