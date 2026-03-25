package playout

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SampleClip describes a generated sample clip.
type SampleClip struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Duration int    `json:"duration"` // seconds
}

// sampleClipDef defines a sample clip to generate via ffmpeg.
type sampleClipDef struct {
	name     string
	title    string
	duration int    // seconds
	filter   string // ffmpeg -f lavfi -i filter expression
}

// sampleClipDefs defines the sample clips we generate.
// Uses basic lavfi sources (no drawtext) for maximum portability.
var sampleClipDefs = []sampleClipDef{
	{
		name: "holding-slate", title: "Holding Slate", duration: 10,
		// Gradient background with grid overlay.
		filter: "rgbtestsrc=s=1920x1080:d=10:r=30," +
			"drawgrid=w=120:h=120:t=2:c=white@0.3," +
			"format=yuv420p",
	},
	{
		name: "bumper-in", title: "Bumper In", duration: 3,
		// Color transition: dark to bright gradient sweep.
		filter: "color=c=black:s=1920x1080:d=3:r=30," +
			"geq=lum='clip(lum(X,Y)+255*T/3,0,255)':" +
			"cb=128:cr=128," +
			"format=yuv420p",
	},
	{
		name: "bumper-out", title: "Bumper Out", duration: 3,
		// Color transition: bright to dark.
		filter: "color=c=white:s=1920x1080:d=3:r=30," +
			"geq=lum='clip(255-255*T/3,0,255)':" +
			"cb=128:cr=128," +
			"format=yuv420p",
	},
	{
		name: "sample-ad-a", title: "Sample Ad A", duration: 15,
		// Animated test pattern (testsrc2).
		filter: "testsrc2=s=1920x1080:d=15:r=30,format=yuv420p",
	},
	{
		name: "sample-ad-b", title: "Sample Ad B", duration: 30,
		// Cellular automaton with color tint.
		filter: "cellauto=s=1920x1080:r=30," +
			"hue=s=2:h=120," +
			"format=yuv420p",
	},
	{
		name: "countdown", title: "Countdown", duration: 10,
		// testsrc has a built-in countdown pattern.
		filter: "testsrc=s=1920x1080:d=10:r=30,format=yuv420p",
	},
}

// GenerateSampleClips creates sample clips in cacheDir via ffmpeg.
// Returns nil gracefully if ffmpeg is not available.
func GenerateSampleClips(cacheDir string) []SampleClip {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("playout: ffmpeg not found, skipping sample clip generation")
		return nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		slog.Warn("playout: failed to create sample cache dir", "dir", cacheDir, "error", err)
		return nil
	}

	var clips []SampleClip
	for _, def := range sampleClipDefs {
		outPath := filepath.Join(cacheDir, def.name+".ts")

		// Skip if already generated.
		if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
			clips = append(clips, SampleClip{
				Name:     def.name,
				Title:    def.title,
				Path:     outPath,
				Duration: def.duration,
			})
			continue
		}

		cmd := exec.Command("ffmpeg",
			"-y", "-loglevel", "error",
			"-f", "lavfi", "-i", def.filter,
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-an",
			"-t", fmt.Sprintf("%d", def.duration),
			"-f", "mpegts", outPath,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			slog.Warn("playout: failed to generate sample clip",
				"name", def.name, "error", err)
			continue
		}

		clips = append(clips, SampleClip{
			Name:     def.name,
			Title:    def.title,
			Path:     outPath,
			Duration: def.duration,
		})
		slog.Info("playout: generated sample clip", "name", def.name, "duration", def.duration)
	}

	return clips
}

// templateNames is the ordered list of available template names.
var templateNames = []string{
	"Live Event with Breaks",
	"VOD Playlist",
	"Scheduled Broadcast",
}

// AvailableTemplates returns the names of all available template playlists.
func AvailableTemplates() []string {
	out := make([]string, len(templateNames))
	copy(out, templateNames)
	return out
}

// TemplatePlaylist returns a pre-built playlist for the given template name.
// Uses sample clip paths. Returns nil if template name is unknown.
func TemplatePlaylist(name string, samples []SampleClip) *Playlist {
	// Index samples by name for quick lookup.
	byName := make(map[string]SampleClip, len(samples))
	for _, s := range samples {
		byName[s.Name] = s
	}

	switch name {
	case "Live Event with Breaks":
		return templateLiveEvent(byName)
	case "VOD Playlist":
		return templateVOD(byName)
	case "Scheduled Broadcast":
		return templateScheduled(byName)
	default:
		return nil
	}
}

// samplePath returns the asset URI for a sample clip, or a placeholder if missing.
func samplePath(byName map[string]SampleClip, name string) string {
	if s, ok := byName[name]; ok {
		return s.Path
	}
	return "/tmp/sample-" + name + ".ts"
}

// sampleDurationMs returns the duration in ms for a sample clip.
func sampleDurationMs(byName map[string]SampleClip, name string) int64 {
	if s, ok := byName[name]; ok {
		return int64(s.Duration) * 1000
	}
	return 10000 // fallback 10s
}

func templateLiveEvent(byName map[string]SampleClip) *Playlist {
	return &Playlist{
		ID:   "template-live-event",
		Name: "Live Event with Breaks",
		DefaultTransition: TransitionConfig{
			Type:       "cut",
			DurationMs: 0,
		},
		IdleAssetURI: samplePath(byName, "holding-slate"),
		IdleLoop:     true,
		Items: []PlaylistItem{
			{
				ID:         "le-01",
				Type:       ItemClip,
				Title:      "Waiting Room",
				AssetURI:   samplePath(byName, "holding-slate"),
				DurationMs: sampleDurationMs(byName, "holding-slate"),
				Loop:       true,
			},
			{
				ID:         "le-02",
				Type:       ItemClip,
				Title:      "Show Open",
				AssetURI:   samplePath(byName, "bumper-in"),
				DurationMs: sampleDurationMs(byName, "bumper-in"),
				Transition: &TransitionConfig{Type: "dissolve", DurationMs: 500},
			},
			{
				ID:     "le-03",
				Type:   ItemLive,
				Title:  "Main Event",
				Source: "srt:live",
				Hold:   true,
			},
			{
				ID:         "le-04",
				Type:       ItemClip,
				Title:      "Break Bumper",
				AssetURI:   samplePath(byName, "bumper-out"),
				DurationMs: sampleDurationMs(byName, "bumper-out"),
				SecondaryEvents: []SecondaryEvent{
					{
						Type:     "scte35_cue",
						OffsetMs: 0,
						Anchor:   "start",
						SCTE35: &SCTE35EventData{
							BreakDurationMs: 120000,
							AutoReturn:      true,
						},
					},
				},
			},
			{
				ID:         "le-05",
				Type:       ItemClip,
				Title:      "Commercial A",
				AssetURI:   samplePath(byName, "sample-ad-a"),
				DurationMs: sampleDurationMs(byName, "sample-ad-a"),
			},
			{
				ID:         "le-06",
				Type:       ItemClip,
				Title:      "Commercial B",
				AssetURI:   samplePath(byName, "sample-ad-b"),
				DurationMs: sampleDurationMs(byName, "sample-ad-b"),
			},
			{
				ID:         "le-07",
				Type:       ItemClip,
				Title:      "Return Bumper",
				AssetURI:   samplePath(byName, "bumper-in"),
				DurationMs: sampleDurationMs(byName, "bumper-in"),
			},
			{
				ID:     "le-08",
				Type:   ItemLive,
				Title:  "Main Event (cont.)",
				Source: "srt:live",
				Hold:   true,
			},
			{
				ID:         "le-09",
				Type:       ItemClip,
				Title:      "Final Break",
				AssetURI:   samplePath(byName, "bumper-out"),
				DurationMs: sampleDurationMs(byName, "bumper-out"),
				SecondaryEvents: []SecondaryEvent{
					{
						Type:     "scte35_cue",
						OffsetMs: 0,
						Anchor:   "start",
						SCTE35: &SCTE35EventData{
							BreakDurationMs: 60000,
							AutoReturn:      true,
						},
					},
				},
			},
			{
				ID:         "le-10",
				Type:       ItemClip,
				Title:      "Post-Show Countdown",
				AssetURI:   samplePath(byName, "countdown"),
				DurationMs: sampleDurationMs(byName, "countdown"),
			},
		},
		PodTemplates: []PodTemplateDef{
			{Label: "Commercial", DurationMs: 120000, AutoReturn: true},
			{Label: "Sponsor", DurationMs: 30000, AutoReturn: true},
		},
	}
}

func templateVOD(byName map[string]SampleClip) *Playlist {
	dissolve := &TransitionConfig{Type: "dissolve", DurationMs: 500}
	return &Playlist{
		ID:   "template-vod",
		Name: "VOD Playlist",
		DefaultTransition: TransitionConfig{
			Type:       "dissolve",
			DurationMs: 500,
		},
		Items: []PlaylistItem{
			{
				ID:         "vod-01",
				Type:       ItemClip,
				Title:      "Sample Ad A",
				AssetURI:   samplePath(byName, "sample-ad-a"),
				DurationMs: sampleDurationMs(byName, "sample-ad-a"),
				Transition: dissolve,
			},
			{
				ID:         "vod-02",
				Type:       ItemClip,
				Title:      "Sample Ad B",
				AssetURI:   samplePath(byName, "sample-ad-b"),
				DurationMs: sampleDurationMs(byName, "sample-ad-b"),
				Transition: dissolve,
			},
			{
				ID:         "vod-03",
				Type:       ItemClip,
				Title:      "Countdown",
				AssetURI:   samplePath(byName, "countdown"),
				DurationMs: sampleDurationMs(byName, "countdown"),
				Transition: dissolve,
			},
			{
				ID:         "vod-04",
				Type:       ItemClip,
				Title:      "Looping End Card",
				AssetURI:   samplePath(byName, "sample-ad-a"),
				DurationMs: sampleDurationMs(byName, "sample-ad-a"),
				Loop:       true,
				Transition: dissolve,
			},
		},
	}
}

func templateScheduled(byName map[string]SampleClip) *Playlist {
	now := time.Now()
	t1 := now.Add(30 * time.Second)
	t2 := now.Add(60 * time.Second)
	t3 := now.Add(120 * time.Second)
	t4 := now.Add(180 * time.Second)

	return &Playlist{
		ID:   "template-scheduled",
		Name: "Scheduled Broadcast",
		DefaultTransition: TransitionConfig{
			Type:       "cut",
			DurationMs: 0,
		},
		IdleAssetURI: samplePath(byName, "holding-slate"),
		IdleLoop:     true,
		Items: []PlaylistItem{
			{
				ID:            "sched-01",
				Type:          ItemClip,
				Title:         "Countdown",
				AssetURI:      samplePath(byName, "countdown"),
				DurationMs:    sampleDurationMs(byName, "countdown"),
				ScheduledTime: &t1,
			},
			{
				ID:            "sched-02",
				Type:          ItemClip,
				Title:         "Sample Ad A",
				AssetURI:      samplePath(byName, "sample-ad-a"),
				DurationMs:    sampleDurationMs(byName, "sample-ad-a"),
				ScheduledTime: &t2,
			},
			{
				ID:            "sched-03",
				Type:          ItemClip,
				Title:         "Sample Ad B",
				AssetURI:      samplePath(byName, "sample-ad-b"),
				DurationMs:    sampleDurationMs(byName, "sample-ad-b"),
				ScheduledTime: &t3,
			},
			{
				ID:            "sched-04",
				Type:          ItemClip,
				Title:         "Sign Off",
				AssetURI:      samplePath(byName, "bumper-out"),
				DurationMs:    sampleDurationMs(byName, "bumper-out"),
				ScheduledTime: &t4,
			},
		},
	}
}
