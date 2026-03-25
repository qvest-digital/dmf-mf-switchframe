package demo

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zsiec/switchframe/server/playout"
)

// demoClip describes a synthetic clip to generate via ffmpeg.
type demoClip struct {
	name     string
	title    string
	duration int // seconds
	// ffmpeg complex filtergraph producing [vout] and [aout] streams.
	// Available inputs: none (lavfi sources are created inline).
	filter string
}

// demoClips defines a realistic broadcast channel lineup (~3 minutes total).
// Each clip uses ffmpeg filtergraph to produce visually distinct content.
var demoClips = []demoClip{
	{
		name: "open", title: "Channel Open", duration: 8,
		// Animated gradient with station ID text and upbeat tone.
		filter: `color=c=#1a1a2e:s=1920x1080:d=8:r=30,format=yuv420p,` +
			`drawtext=text='SWITCHFRAME':fontsize=96:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2-60:` +
			`shadowcolor=black:shadowx=3:shadowy=3,` +
			`drawtext=text='DEMO CHANNEL':fontsize=48:fontcolor=#e0e0e0:x=(w-text_w)/2:y=(h-text_h)/2+40,` +
			`drawtext=text='%{pts\:gmtime\:0\:%H %M %S}':fontsize=36:fontcolor=#808080:` +
			`fontfamily=monospace:x=(w-text_w)/2:y=h-80` +
			`[vout];` +
			`sine=f=523:d=0.3,apad=whole_dur=8,volume=0.3[aout]`,
	},
	{
		name: "news_a", title: "Morning Report — Segment A", duration: 30,
		// Simulated news broadcast: blue background with crawl-style text.
		filter: `color=c=#0d2137:s=1920x1080:d=30:r=30,format=yuv420p,` +
			`drawbox=x=0:y=0:w=1920:h=120:color=#cc0000:t=fill,` +
			`drawtext=text='BREAKING NEWS':fontsize=64:fontcolor=white:x=40:y=28,` +
			`drawbox=x=0:y=860:w=1920:h=80:color=#1a3a5c:t=fill,` +
			`drawtext=text='Switchframe Demo Channel — Live Coverage':fontsize=32:fontcolor=#b0b0b0:x=40:y=878,` +
			`drawtext=text='%{pts\:gmtime\:0\:%H\\\:%M\\\:%S}':fontsize=40:fontcolor=white:fontfamily=monospace:x=1700:y=880,` +
			`drawtext=text='The quick brown fox jumps over the lazy dog. ':fontsize=36:fontcolor=yellow:` +
			`x='w-mod(t*200\,w+text_w)':y=960:shadowcolor=black:shadowx=2:shadowy=2` +
			`[vout];` +
			`anoisesrc=d=30:c=pink:a=0.02,volume=0.15[aout]`,
	},
	{
		name: "bumper_1", title: "Coming Up Next", duration: 5,
		// Quick bumper with animated text.
		filter: `color=c=#2d1b69:s=1920x1080:d=5:r=30,format=yuv420p,` +
			`drawtext=text='COMING UP NEXT':fontsize=72:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2-30:` +
			`enable='between(t,0.5,4.5)',` +
			`drawtext=text='Weather & Sports':fontsize=48:fontcolor=#c0c0c0:x=(w-text_w)/2:y=(h-text_h)/2+50:` +
			`enable='between(t,1.0,4.5)'` +
			`[vout];` +
			`sine=f=880:d=0.2,apad=whole_dur=5,volume=0.25[aout]`,
	},
	{
		name: "news_b", title: "Morning Report — Segment B", duration: 25,
		// Weather segment: green screen style background with data.
		filter: `color=c=#004d00:s=1920x1080:d=25:r=30,format=yuv420p,` +
			`drawbox=x=0:y=0:w=1920:h=100:color=#003300:t=fill,` +
			`drawtext=text='WEATHER FORECAST':fontsize=56:fontcolor=white:x=40:y=22,` +
			`drawtext=text='72°F / Partly Cloudy':fontsize=120:fontcolor=white:x=(w-text_w)/2:y=300,` +
			`drawtext=text='Wind\: 12 mph NW   Humidity\: 45%%':fontsize=36:fontcolor=#b0ffb0:x=(w-text_w)/2:y=500,` +
			`drawtext=text='5-Day Outlook\: Sun > Mon Cloud > Tue Rain > Wed Sun > Thu Sun':fontsize=28:fontcolor=#80c080:` +
			`x=(w-text_w)/2:y=600` +
			`[vout];` +
			`anoisesrc=d=25:c=pink:a=0.015,volume=0.12[aout]`,
	},
	{
		name: "promo", title: "Promo — Evening Lineup", duration: 10,
		// Promo spot with animated elements.
		filter: `color=c=#1a0a2e:s=1920x1080:d=10:r=30,format=yuv420p,` +
			`drawtext=text='TONIGHT AT 8':fontsize=80:fontcolor=#ff6600:x=(w-text_w)/2:y=200:` +
			`enable='between(t,0.5,9)',` +
			`drawtext=text='The Championship Finals':fontsize=60:fontcolor=white:x=(w-text_w)/2:y=400:` +
			`enable='between(t,1.5,9)',` +
			`drawtext=text='LIVE on Switchframe':fontsize=44:fontcolor=#cccccc:x=(w-text_w)/2:y=520:` +
			`enable='between(t,2.5,9)',` +
			`drawtext=text='switchframe.dev':fontsize=36:fontcolor=#666666:x=(w-text_w)/2:y=700:` +
			`enable='between(t,3.5,9)'` +
			`[vout];` +
			`sine=f=660:d=0.5,apad=whole_dur=10,volume=0.3[aout]`,
	},
	{
		name: "news_c", title: "Morning Report — Sports", duration: 25,
		// Sports scores segment.
		filter: `color=c=#1a1a1a:s=1920x1080:d=25:r=30,format=yuv420p,` +
			`drawbox=x=0:y=0:w=1920:h=100:color=#cc0000:t=fill,` +
			`drawtext=text='SPORTS CENTER':fontsize=56:fontcolor=white:x=40:y=22,` +
			`drawbox=x=100:y=200:w=1720:h=100:color=#333333:t=fill,` +
			`drawtext=text='Lakers   108  —  Celtics   104':fontsize=48:fontcolor=white:x=200:y=225,` +
			`drawbox=x=100:y=320:w=1720:h=100:color=#2a2a2a:t=fill,` +
			`drawtext=text='Yankees    5  —  Red Sox     3':fontsize=48:fontcolor=white:x=200:y=345,` +
			`drawbox=x=100:y=440:w=1720:h=100:color=#333333:t=fill,` +
			`drawtext=text='Chiefs    24  —  49ers      21':fontsize=48:fontcolor=white:x=200:y=465,` +
			`drawtext=text='FINAL SCORES':fontsize=28:fontcolor=#888888:x=(w-text_w)/2:y=580` +
			`[vout];` +
			`anoisesrc=d=25:c=pink:a=0.02,volume=0.15[aout]`,
	},
	{
		name: "bumper_2", title: "Station ID", duration: 5,
		// Short station ident with tone.
		filter: `color=c=#0a0a1e:s=1920x1080:d=5:r=30,format=yuv420p,` +
			`drawtext=text='SF':fontsize=200:fontcolor=#ffffff:x=(w-text_w)/2:y=(h-text_h)/2-50:` +
			`shadowcolor=#333333:shadowx=5:shadowy=5,` +
			`drawtext=text='switchframe.dev':fontsize=32:fontcolor=#555555:x=(w-text_w)/2:y=(h-text_h)/2+120` +
			`[vout];` +
			`sine=f=440:d=1.5,apad=whole_dur=5,volume=0.2[aout]`,
	},
	{
		name: "close", title: "Channel Close", duration: 6,
		// Sign-off.
		filter: `color=c=#0d0d0d:s=1920x1080:d=6:r=30,format=yuv420p,` +
			`drawtext=text='Thank you for watching':fontsize=56:fontcolor=#999999:x=(w-text_w)/2:y=(h-text_h)/2-40:` +
			`enable='between(t,0.5,5.5)',` +
			`drawtext=text='SWITCHFRAME DEMO':fontsize=36:fontcolor=#555555:x=(w-text_w)/2:y=(h-text_h)/2+40:` +
			`enable='between(t,1.5,5.5)'` +
			`[vout];` +
			`anoisesrc=d=6:c=pink:a=0.01,volume=0.08[aout]`,
	},
}

// generateClip creates a single .ts clip using ffmpeg with a complex filtergraph.
func generateClip(c demoClip, outPath string) error {
	cmd := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-filter_complex", c.filter,
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k",
		"-t", fmt.Sprintf("%d", c.duration),
		"-f", "mpegts", outPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// GenerateDemoPlaylist creates a realistic broadcast channel lineup in tmpDir
// using ffmpeg and returns a playout.Playlist ready to load. Includes SCTE-35
// secondary events on ad break boundaries. If ffmpeg is not available, returns nil.
func GenerateDemoPlaylist(tmpDir string) (*playout.Playlist, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("demo: ffmpeg not found, skipping playout demo playlist")
		return nil, nil
	}

	type generated struct {
		path string
		clip demoClip
	}
	var clips []generated

	for _, c := range demoClips {
		outPath := filepath.Join(tmpDir, c.name+".ts")
		if err := generateClip(c, outPath); err != nil {
			slog.Warn("demo: failed to generate playout clip", "name", c.name, "error", err)
			continue
		}
		clips = append(clips, generated{path: outPath, clip: c})
		slog.Info("demo: generated playout clip", "name", c.name, "duration", c.duration)
	}

	if len(clips) == 0 {
		slog.Warn("demo: no playout clips generated")
		return nil, nil
	}

	// Build the playlist. Insert ad breaks after news segments using SCTE-35
	// secondary events — just like a real broadcast schedule.
	var items []playout.PlaylistItem
	itemNum := 0

	for _, g := range clips {
		itemNum++
		item := playout.PlaylistItem{
			ID:         fmt.Sprintf("demo-%02d", itemNum),
			Type:       playout.ItemClip,
			Title:      g.clip.title,
			AssetURI:   g.path,
			DurationMs: int64(g.clip.duration) * 1000,
		}

		// Add SCTE-35 cue-out at end of news segments (triggers ad pod).
		if strings.HasPrefix(g.clip.name, "news_") {
			item.SecondaryEvents = append(item.SecondaryEvents, playout.SecondaryEvent{
				Type:     "scte35_cue",
				OffsetMs: -500, // 500ms before segment end
				Anchor:   "end",
				SCTE35: &playout.SCTE35EventData{
					BreakDurationMs: 15000, // 15s ad break
					AutoReturn:      true,
				},
			})
		}

		items = append(items, item)
	}

	return &playout.Playlist{
		ID:   "demo",
		Name: "SF Demo Channel — Morning Block",
		DefaultTransition: playout.TransitionConfig{
			Type:       "dissolve",
			DurationMs: 500,
		},
		Items: items,
	}, nil
}

// SetupDemoPods creates pre-armed ad break pods on a channel's pod board
// to demonstrate the pod management workflow.
func SetupDemoPods(mgr *playout.Manager, channelID int) {
	pb, err := mgr.PodBoard(channelID)
	if err != nil {
		slog.Warn("demo: failed to get pod board", "channel", channelID, "error", err)
		return
	}

	// Create and arm a "quarter break" pod (typical 2-minute commercial pod).
	pod1, err := pb.CreateFromTemplate("quarter")
	if err != nil {
		slog.Warn("demo: failed to create quarter pod", "error", err)
	} else {
		if err := pb.Arm(pod1.ID); err != nil {
			slog.Warn("demo: failed to arm quarter pod", "error", err)
		} else {
			slog.Info("demo: armed quarter break pod", "id", pod1.ID)
		}
	}

	// Create and arm a "timeout" pod (30s quick break).
	pod2, err := pb.CreateFromTemplate("timeout")
	if err != nil {
		slog.Warn("demo: failed to create timeout pod", "error", err)
	} else {
		if err := pb.Arm(pod2.ID); err != nil {
			slog.Warn("demo: failed to arm timeout pod", "error", err)
		} else {
			slog.Info("demo: armed timeout pod", "id", pod2.ID)
		}
	}

	slog.Info("demo: pod board configured", "channel", channelID, "armed", 2)
}
