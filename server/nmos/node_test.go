package nmos

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// testStore returns a ResourceStore pre-populated with sample resources.
func testStore() *ResourceStore {
	store := NewResourceStore()

	store.SetNode(&Node{
		ID:          "node-001",
		Version:     NewVersion(),
		Label:       "Test Node",
		Description: "A test NMOS node",
		Hostname:    "switchframe.local",
		Tags:        map[string][]string{},
		Caps:        map[string]interface{}{},
		Href:        "http://switchframe.local:80/",
		API: NodeAPI{
			Versions:  []string{"v1.3"},
			Endpoints: []APIEndpoint{{Host: "switchframe.local", Port: 80, Protocol: "http"}},
		},
		Clocks:     []Clock{{Name: "clk0", RefType: "internal"}},
		Interfaces: []Interface{{Name: "eth0", ChassisID: "aa-bb-cc-dd-ee-ff", PortID: "aa-bb-cc-dd-ee-ff"}},
		Services:   []interface{}{},
	})

	store.AddDevice(&Device{
		ID:          "dev-001",
		Label:       "Switcher Device",
		Description: "Main switcher",
		Tags:        map[string][]string{},
		Type:        DeviceTypePipeline,
		NodeID:      "node-001",
		Senders:     []string{"sender-001"},
		Receivers:   []string{"recv-001"},
		Controls:    []Control{},
	})

	store.AddSource(&Source{
		ID:          "src-001",
		Label:       "Video Source",
		Description: "Camera 1",
		Tags:        map[string][]string{},
		Caps:        map[string]interface{}{},
		Format:      FormatVideo,
		Parents:     []string{},
		DeviceID:    "dev-001",
		ClockName:   "clk0",
	})

	store.AddFlow(&Flow{
		ID:          "flow-001",
		Label:       "Video Flow",
		Description: "1080i video",
		Tags:        map[string][]string{},
		Format:      FormatVideo,
		SourceID:    "src-001",
		DeviceID:    "dev-001",
		Parents:     []string{},
		MediaType:   "video/raw",
		FrameWidth:  1920,
		FrameHeight: 1080,
	})

	store.AddSender(&Sender{
		ID:                "sender-001",
		Label:             "Video Sender",
		Description:       "Sends video",
		Tags:              map[string][]string{},
		FlowID:            "flow-001",
		Transport:         TransportRTP,
		DeviceID:          "dev-001",
		ManifestHref:      "http://switchframe.local/sdp/sender-001",
		InterfaceBindings: []string{"eth0"},
		Subscription:      SenderSubscription{ReceiverID: nil, Active: false},
	})

	store.AddReceiver(&Receiver{
		ID:                "recv-001",
		Label:             "Video Receiver",
		Description:       "Receives video",
		Tags:              map[string][]string{},
		Format:            FormatVideo,
		Caps:              ReceiverCaps{MediaTypes: []string{"video/raw"}},
		Transport:         TransportRTPMcast,
		DeviceID:          "dev-001",
		InterfaceBindings: []string{"eth0"},
		Subscription:      ReceiverSubscription{SenderID: nil, Active: false},
	})

	return store
}

func TestNodeAPIRoot(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/")
	if err != nil {
		t.Fatalf("GET root: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var paths []string
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expected := []string{"self/", "sources/", "flows/", "devices/", "senders/", "receivers/"}
	if len(paths) != len(expected) {
		t.Fatalf("paths length: got %d, want %d", len(paths), len(expected))
	}
	pathSet := make(map[string]bool)
	for _, p := range paths {
		pathSet[p] = true
	}
	for _, e := range expected {
		if !pathSet[e] {
			t.Errorf("missing path %q in response %v", e, paths)
		}
	}
}

func TestNodeAPISelf(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/self")
	if err != nil {
		t.Fatalf("GET self: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var node Node
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if node.ID != "node-001" {
		t.Errorf("ID: got %q, want %q", node.ID, "node-001")
	}
	if node.Label != "Test Node" {
		t.Errorf("Label: got %q, want %q", node.Label, "Test Node")
	}
	if node.Hostname != "switchframe.local" {
		t.Errorf("Hostname: got %q, want %q", node.Hostname, "switchframe.local")
	}
}

func TestNodeAPISelfNotSet(t *testing.T) {
	store := NewResourceStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/self")
	if err != nil {
		t.Fatalf("GET self: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestNodeAPIDevices(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// List devices.
	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/devices")
	if err != nil {
		t.Fatalf("GET devices: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var devices []Device
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices count: got %d, want 1", len(devices))
	}
	if devices[0].ID != "dev-001" {
		t.Errorf("device ID: got %q, want %q", devices[0].ID, "dev-001")
	}

	// Get single device.
	resp2, err := http.Get(ts.URL + "/x-nmos/node/v1.3/devices/dev-001")
	if err != nil {
		t.Fatalf("GET devices/dev-001: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp2.StatusCode)
	}

	var dev Device
	if err := json.NewDecoder(resp2.Body).Decode(&dev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dev.ID != "dev-001" {
		t.Errorf("device ID: got %q, want %q", dev.ID, "dev-001")
	}

	// 404 for unknown device.
	resp3, err := http.Get(ts.URL + "/x-nmos/node/v1.3/devices/unknown")
	if err != nil {
		t.Fatalf("GET devices/unknown: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp3.StatusCode)
	}
}

func TestNodeAPISources(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// List sources.
	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/sources")
	if err != nil {
		t.Fatalf("GET sources: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var sources []Source
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources count: got %d, want 1", len(sources))
	}
	if sources[0].ID != "src-001" {
		t.Errorf("source ID: got %q, want %q", sources[0].ID, "src-001")
	}

	// Get single source.
	resp2, err := http.Get(ts.URL + "/x-nmos/node/v1.3/sources/src-001")
	if err != nil {
		t.Fatalf("GET sources/src-001: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp2.StatusCode)
	}

	var src Source
	if err := json.NewDecoder(resp2.Body).Decode(&src); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if src.ID != "src-001" {
		t.Errorf("source ID: got %q, want %q", src.ID, "src-001")
	}

	// 404 for unknown source.
	resp3, err := http.Get(ts.URL + "/x-nmos/node/v1.3/sources/unknown")
	if err != nil {
		t.Fatalf("GET sources/unknown: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp3.StatusCode)
	}
}

func TestNodeAPIFlows(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// List flows.
	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/flows")
	if err != nil {
		t.Fatalf("GET flows: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var flows []Flow
	if err := json.NewDecoder(resp.Body).Decode(&flows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("flows count: got %d, want 1", len(flows))
	}
	if flows[0].ID != "flow-001" {
		t.Errorf("flow ID: got %q, want %q", flows[0].ID, "flow-001")
	}

	// Get single flow.
	resp2, err := http.Get(ts.URL + "/x-nmos/node/v1.3/flows/flow-001")
	if err != nil {
		t.Fatalf("GET flows/flow-001: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp2.StatusCode)
	}

	var flow Flow
	if err := json.NewDecoder(resp2.Body).Decode(&flow); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if flow.ID != "flow-001" {
		t.Errorf("flow ID: got %q, want %q", flow.ID, "flow-001")
	}

	// 404 for unknown flow.
	resp3, err := http.Get(ts.URL + "/x-nmos/node/v1.3/flows/unknown")
	if err != nil {
		t.Fatalf("GET flows/unknown: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp3.StatusCode)
	}
}

func TestNodeAPIReceivers(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// List receivers.
	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/receivers")
	if err != nil {
		t.Fatalf("GET receivers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var receivers []Receiver
	if err := json.NewDecoder(resp.Body).Decode(&receivers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(receivers) != 1 {
		t.Fatalf("receivers count: got %d, want 1", len(receivers))
	}
	if receivers[0].ID != "recv-001" {
		t.Errorf("receiver ID: got %q, want %q", receivers[0].ID, "recv-001")
	}

	// Get single receiver.
	resp2, err := http.Get(ts.URL + "/x-nmos/node/v1.3/receivers/recv-001")
	if err != nil {
		t.Fatalf("GET receivers/recv-001: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp2.StatusCode)
	}

	var recv Receiver
	if err := json.NewDecoder(resp2.Body).Decode(&recv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if recv.ID != "recv-001" {
		t.Errorf("receiver ID: got %q, want %q", recv.ID, "recv-001")
	}

	// 404 for unknown receiver.
	resp3, err := http.Get(ts.URL + "/x-nmos/node/v1.3/receivers/unknown-id")
	if err != nil {
		t.Fatalf("GET receivers/unknown-id: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp3.StatusCode)
	}

	// Verify 404 body.
	var errBody map[string]interface{}
	if err := json.NewDecoder(resp3.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if code, ok := errBody["code"].(float64); !ok || int(code) != 404 {
		t.Errorf("404 code: got %v", errBody["code"])
	}
	if errStr, ok := errBody["error"].(string); !ok || errStr != "Not Found" {
		t.Errorf("404 error: got %v", errBody["error"])
	}
}

func TestNodeAPISenders(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// List senders.
	resp, err := http.Get(ts.URL + "/x-nmos/node/v1.3/senders")
	if err != nil {
		t.Fatalf("GET senders: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var senders []Sender
	if err := json.NewDecoder(resp.Body).Decode(&senders); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(senders) != 1 {
		t.Fatalf("senders count: got %d, want 1", len(senders))
	}
	if senders[0].ID != "sender-001" {
		t.Errorf("sender ID: got %q, want %q", senders[0].ID, "sender-001")
	}

	// Get single sender.
	resp2, err := http.Get(ts.URL + "/x-nmos/node/v1.3/senders/sender-001")
	if err != nil {
		t.Fatalf("GET senders/sender-001: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp2.StatusCode)
	}

	var sender Sender
	if err := json.NewDecoder(resp2.Body).Decode(&sender); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sender.ID != "sender-001" {
		t.Errorf("sender ID: got %q, want %q", sender.ID, "sender-001")
	}

	// 404 for unknown sender.
	resp3, err := http.Get(ts.URL + "/x-nmos/node/v1.3/senders/unknown")
	if err != nil {
		t.Fatalf("GET senders/unknown: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp3.StatusCode)
	}
}

func TestNodeAPIContentType(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	endpoints := []string{
		"/x-nmos/node/v1.3/",
		"/x-nmos/node/v1.3/self",
		"/x-nmos/node/v1.3/devices",
		"/x-nmos/node/v1.3/sources",
		"/x-nmos/node/v1.3/flows",
		"/x-nmos/node/v1.3/senders",
		"/x-nmos/node/v1.3/receivers",
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		ct := resp.Header.Get("Content-Type")
		_ = resp.Body.Close()
		if ct != "application/json" {
			t.Errorf("%s Content-Type: got %q, want %q", ep, ct, "application/json")
		}
	}
}

func TestNodeAPIEmptyCollections(t *testing.T) {
	store := NewResourceStore()
	store.SetNode(&Node{
		ID:      "node-001",
		Version: NewVersion(),
	})
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	endpoints := []string{
		"/x-nmos/node/v1.3/devices",
		"/x-nmos/node/v1.3/sources",
		"/x-nmos/node/v1.3/flows",
		"/x-nmos/node/v1.3/senders",
		"/x-nmos/node/v1.3/receivers",
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}

		var raw json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatalf("%s decode: %v", ep, err)
		}
		_ = resp.Body.Close()

		// Must be a JSON array "[]", not null.
		s := string(raw)
		if s != "[]" {
			t.Errorf("%s: expected empty array '[]', got %q", ep, s)
		}
	}
}

func TestResourceStoreThreadSafety(t *testing.T) {
	store := NewResourceStore()

	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Writers.
				store.SetNode(&Node{ID: "node-001", Version: NewVersion()})
				store.AddDevice(&Device{ID: "dev-001", Version: NewVersion()})
				store.AddSource(&Source{ID: "src-001", Version: NewVersion()})
				store.AddFlow(&Flow{ID: "flow-001", Version: NewVersion()})
				store.AddSender(&Sender{ID: "sender-001", Version: NewVersion()})
				store.AddReceiver(&Receiver{ID: "recv-001", Version: NewVersion()})

				// Readers.
				store.GetNode()
				store.GetDevice("dev-001")
				store.ListDevices()
				store.GetSource("src-001")
				store.ListSources()
				store.GetFlow("flow-001")
				store.ListFlows()
				store.GetSender("sender-001")
				store.ListSenders()
				store.GetReceiver("recv-001")
				store.ListReceivers()

				// Removals.
				store.RemoveDevice("dev-001")
				store.RemoveSource("src-001")
				store.RemoveFlow("flow-001")
				store.RemoveSender("sender-001")
				store.RemoveReceiver("recv-001")

				// Subscription update.
				senderID := "sender-001"
				store.UpdateReceiverSubscription("recv-001", &senderID, true)
			}
		}(g)
	}

	wg.Wait()
}

func TestNodeAPIMethodNotAllowed(t *testing.T) {
	store := testStore()
	srv := NewNodeServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	endpoints := []string{
		"/x-nmos/node/v1.3/",
		"/x-nmos/node/v1.3/self",
		"/x-nmos/node/v1.3/devices",
		"/x-nmos/node/v1.3/devices/dev-001",
		"/x-nmos/node/v1.3/sources",
		"/x-nmos/node/v1.3/flows",
		"/x-nmos/node/v1.3/senders",
		"/x-nmos/node/v1.3/receivers",
	}

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, ep := range endpoints {
		for _, method := range methods {
			req, err := http.NewRequest(method, ts.URL+ep, nil)
			if err != nil {
				t.Fatalf("new request %s %s: %v", method, ep, err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, ep, err)
			}
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: got %d, want 405", method, ep, resp.StatusCode)
			}
			if allow := resp.Header.Get("Allow"); allow != "GET" {
				t.Errorf("%s %s Allow header: got %q, want %q", method, ep, allow, "GET")
			}
		}
	}
}

func TestResourceStoreGetReturnsShallowCopy(t *testing.T) {
	store := NewResourceStore()

	store.AddDevice(&Device{ID: "dev-001", Label: "Original"})
	got := store.GetDevice("dev-001")
	got.Label = "Mutated"

	got2 := store.GetDevice("dev-001")
	if got2.Label != "Original" {
		t.Errorf("GetDevice returned shared pointer: mutation leaked (got %q, want %q)", got2.Label, "Original")
	}
}

func TestResourceStoreListReturnsShallowCopies(t *testing.T) {
	store := NewResourceStore()

	store.AddDevice(&Device{ID: "dev-001", Label: "Original"})
	list := store.ListDevices()
	if len(list) != 1 {
		t.Fatalf("expected 1 device, got %d", len(list))
	}
	list[0].Label = "Mutated"

	list2 := store.ListDevices()
	if list2[0].Label != "Original" {
		t.Errorf("ListDevices returned shared pointer: mutation leaked (got %q, want %q)", list2[0].Label, "Original")
	}
}

func TestResourceStoreRemoveDeviceSourceFlow(t *testing.T) {
	store := NewResourceStore()

	store.AddDevice(&Device{ID: "dev-001"})
	store.AddSource(&Source{ID: "src-001"})
	store.AddFlow(&Flow{ID: "flow-001"})

	if store.GetDevice("dev-001") == nil {
		t.Fatal("device should exist")
	}
	if store.GetSource("src-001") == nil {
		t.Fatal("source should exist")
	}
	if store.GetFlow("flow-001") == nil {
		t.Fatal("flow should exist")
	}

	store.RemoveDevice("dev-001")
	store.RemoveSource("src-001")
	store.RemoveFlow("flow-001")

	if store.GetDevice("dev-001") != nil {
		t.Error("device should be removed")
	}
	if store.GetSource("src-001") != nil {
		t.Error("source should be removed")
	}
	if store.GetFlow("flow-001") != nil {
		t.Error("flow should be removed")
	}

	// No-op on non-existent IDs (should not panic).
	store.RemoveDevice("nonexistent")
	store.RemoveSource("nonexistent")
	store.RemoveFlow("nonexistent")
}

func TestResourceStoreAddBumpsVersion(t *testing.T) {
	store := NewResourceStore()

	origVersion := NewVersion()
	dev := &Device{ID: "dev-001", Version: origVersion}
	store.AddDevice(dev)

	got := store.GetDevice("dev-001")
	if got == nil {
		t.Fatal("device not found after AddDevice")
	}
	if got.Version == origVersion {
		t.Error("AddDevice should have bumped the version")
	}
}

func TestResourceStoreRemove(t *testing.T) {
	store := NewResourceStore()

	store.AddSender(&Sender{ID: "sender-001"})
	store.AddReceiver(&Receiver{ID: "recv-001"})

	if store.GetSender("sender-001") == nil {
		t.Fatal("sender should exist")
	}
	if store.GetReceiver("recv-001") == nil {
		t.Fatal("receiver should exist")
	}

	store.RemoveSender("sender-001")
	store.RemoveReceiver("recv-001")

	if store.GetSender("sender-001") != nil {
		t.Error("sender should be removed")
	}
	if store.GetReceiver("recv-001") != nil {
		t.Error("receiver should be removed")
	}
}

func TestResourceStoreUpdateReceiverSubscription(t *testing.T) {
	store := NewResourceStore()

	store.AddReceiver(&Receiver{
		ID:           "recv-001",
		Subscription: ReceiverSubscription{SenderID: nil, Active: false},
	})

	senderID := "sender-001"
	store.UpdateReceiverSubscription("recv-001", &senderID, true)

	recv := store.GetReceiver("recv-001")
	if recv == nil {
		t.Fatal("receiver not found")
	}
	if recv.Subscription.SenderID == nil || *recv.Subscription.SenderID != senderID {
		t.Errorf("SenderID: got %v, want %q", recv.Subscription.SenderID, senderID)
	}
	if !recv.Subscription.Active {
		t.Error("Active should be true")
	}
}
