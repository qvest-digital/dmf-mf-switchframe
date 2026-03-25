package playout

import (
	"sync/atomic"
	"testing"
)

func TestPodBoard_CreatePod(t *testing.T) {
	pb := NewPodBoard(1)
	pod, err := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   180000,
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     PriorityNormal,
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	if pod.ID == "" {
		t.Fatal("pod ID should not be empty")
	}
	if pod.State != PodScheduled {
		t.Errorf("state = %v, want scheduled", pod.State)
	}
	if pod.Armed {
		t.Error("new pod should not be armed")
	}

	pods := pb.ListPods()
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
}

func TestPodBoard_CreateFromTemplate(t *testing.T) {
	store := NewPodTemplateStore()
	pb := NewPodBoard(1)
	pb.SetTemplateStore(store)

	pod, err := pb.CreateFromTemplate("quarter")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if pod.Config.Type != "quarter" {
		t.Errorf("type = %q, want quarter", pod.Config.Type)
	}
	if pod.Config.DurationMs != 180000 {
		t.Errorf("duration = %d, want 180000", pod.Config.DurationMs)
	}
}

func TestPodBoard_CreateFromTemplate_NotFound(t *testing.T) {
	store := NewPodTemplateStore()
	pb := NewPodBoard(1)
	pb.SetTemplateStore(store)

	_, err := pb.CreateFromTemplate("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent template")
	}
}

func TestPodBoard_ArmDisarm(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.Arm(pod.ID); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if !got.Armed {
		t.Error("pod should be armed")
	}

	if err := pb.Disarm(pod.ID); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	got, _ = pb.GetPod(pod.ID)
	if got.Armed {
		t.Error("pod should be disarmed")
	}
}

func TestPodBoard_ArmNotFound(t *testing.T) {
	pb := NewPodBoard(1)
	if err := pb.Arm("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestPodBoard_Delete(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.DeletePod(pod.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(pb.ListPods()) != 0 {
		t.Fatal("expected 0 pods after delete")
	}
}

func TestPodBoard_DeleteNotFound(t *testing.T) {
	pb := NewPodBoard(1)
	if err := pb.DeletePod("nonexistent"); err == nil {
		t.Fatal("expected error deleting nonexistent pod")
	}
}

func TestPodBoard_StateChangeCallback(t *testing.T) {
	pb := NewPodBoard(1)
	var called atomic.Int32
	pb.SetOnStateChange(func() { called.Add(1) })

	pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	if called.Load() < 1 {
		t.Error("onStateChange should be called on create")
	}
}

func TestPodBoard_GetPod(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	got, err := pb.GetPod(pod.ID)
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if got.ID != pod.ID {
		t.Errorf("ID = %q, want %q", got.ID, pod.ID)
	}

	_, err = pb.GetPod("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestPodBoard_Status(t *testing.T) {
	pb := NewPodBoard(1)
	pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	pod2, _ := pb.CreatePod(PodConfig{Type: "timeout", DurationMs: 60000})
	pb.Arm(pod2.ID)

	status := pb.Status()
	if len(status.Pods) != 2 {
		t.Fatalf("expected 2 pods in status, got %d", len(status.Pods))
	}
	if status.PodsArmed != 1 {
		t.Errorf("armed = %d, want 1", status.PodsArmed)
	}
	if status.ActivePod != nil {
		t.Error("no pod should be active")
	}
}
