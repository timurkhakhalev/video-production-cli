// Package graph builds the ffmpeg filter graph and encoder arguments (render.py build_graph,
// final_codec_args).
package graph

import (
	"fmt"
	"math/big"
	"strings"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// EdgeFade is a 5 ms fade at each splice: no overlap, duration unchanged.
const EdgeFade = 0.005

var (
	PreviewVideo = []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "24"}
	PreviewAudio = []string{"-c:a", "aac", "-b:a", "128k"}
	ProxyArgs    = []string{"-vf", "scale=-2:540", "-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
		"-c:a", "aac", "-b:a", "96k"}
)

// Listening clips: mp3 is readable by every agent (Antigravity rejects AAC), 16 kHz mono is what
// speech models resample to anyway. libmp3lame delays the sound by 1105 samples (576 encoder +
// 529 decoder); decoders that honour the LAME/Xing header remove it, others do not.
// Keep the header and disclose the delay rather than discarding source samples.
const (
	ListenRate       = 16000
	MP3DelaySamples  = 1105
	ListenMP3Bitrate = "40k"
)

// MP3Delay is the encoder delay in seconds.
const MP3Delay = float64(MP3DelaySamples) / ListenRate

// ListenMP3Args encode a listening clip.
var ListenMP3Args = []string{"-ac", "1", "-c:a", "libmp3lame", "-b:a", ListenMP3Bitrate}

const (
	FinalCRF    = 15
	FinalPreset = "slow"
)

// Build returns the filter_complex text for the kept ranges.
func Build(e *edit.Edit, keeps []edit.Keep) string { return build(e, keeps, true) }

// BuildAudio is Build without the picture: the same atrim pieces, fades and muted speed-ups,
// concatenated into [a].
func BuildAudio(e *edit.Edit, keeps []edit.Keep) string { return build(e, keeps, false) }

func build(e *edit.Edit, keeps []edit.Keep, video bool) string {
	fps := e.FPS
	audio := e.EncAudio()
	sr := intOf(get(audio, "sample_rate"))
	layout := "stereo"
	if l, ok := get(audio, "channel_layout").(string); ok && l != "" {
		layout = l
	}
	pieces := e.Timeline(keeps)
	var lines, pairs []string
	for i, p := range pieces {
		dur := p.Out - p.In
		vfx := fmt.Sprintf("trim=start=%.6f:end=%.6f,setpts=PTS-STARTPTS", p.In, p.Out)
		var afx string
		if p.Speed > 1 {
			// keep every speed-th frame and retime it onto the source frame grid; voice is replaced by silence
			vfx += fmt.Sprintf(",select=not(mod(n\\,%d)),setpts=N*%s/%s/TB", p.Speed, fps.Denom(), fps.Num())
			samples := new(big.Rat).SetInt64(p.Frames * sr)
			samples.Quo(samples, fps)
			afx = fmt.Sprintf("anullsrc=r=%d:cl=%s,atrim=end_sample=%d", sr, layout, pyfmt.RoundRat(samples))
		} else {
			afx = fmt.Sprintf("[0:a:0]atrim=start=%.6f:end=%.6f,asetpts=PTS-STARTPTS", p.In, p.Out)
			if EdgeFade > 0 && dur > 4*EdgeFade {
				if i > 0 {
					afx += fmt.Sprintf(",afade=t=in:d=%.4f", EdgeFade)
				}
				if i < len(pieces)-1 {
					afx += fmt.Sprintf(",afade=t=out:st=%.6f:d=%.4f", dur-EdgeFade, EdgeFade)
				}
			}
		}
		if video {
			lines = append(lines, fmt.Sprintf("[0:v:0]%s[v%d];", vfx, i))
		}
		lines = append(lines, fmt.Sprintf("%s[a%d];", afx, i))
		if video {
			pairs = append(pairs, fmt.Sprintf("[v%d][a%d]", i, i))
		} else {
			pairs = append(pairs, fmt.Sprintf("[a%d]", i))
		}
	}
	if video {
		lines = append(lines, fmt.Sprintf("%sconcat=n=%d:v=1:a=1[v][a]", strings.Join(pairs, ""), len(pieces)))
	} else {
		lines = append(lines, fmt.Sprintf("%sconcat=n=%d:v=0:a=1[a]", strings.Join(pairs, ""), len(pieces)))
	}
	return strings.Join(lines, "\n") + "\n"
}

// FinalCodecArgs encodes with the source parameters from encode.json.
func FinalCodecArgs(e *edit.Edit) ([]string, error) {
	v, a := e.EncVideo(), e.EncAudio()
	if c := get(v, "codec"); c != "h264" {
		return nil, fmt.Errorf("source video codec %s is not h264; extend the renderer deliberately, do not improvise", jsonx.Str(c))
	}
	if c := get(a, "codec"); c != "aac" {
		return nil, fmt.Errorf("source audio codec %s is not aac; extend the renderer deliberately, do not improvise", jsonx.Str(c))
	}
	out := []string{"-c:v", "libx264", "-preset", FinalPreset, "-crf", fmt.Sprint(FinalCRF), "-pix_fmt", jsonx.Str(get(v, "pix_fmt"))}
	profile, _ := get(v, "profile").(string)
	profile = strings.ReplaceAll(strings.ToLower(profile), "constrained ", "")
	if profile == "baseline" || profile == "main" || profile == "high" {
		out = append(out, "-profile:v", profile)
	}
	if lv, ok := get(v, "level").(jsonx.Number); ok && lv.Float() != 0 {
		out = append(out, "-level:v", fmt.Sprintf("%.1f", lv.Float()/10.0))
	}
	for _, kv := range [][2]string{{"-color_range", "color_range"}, {"-colorspace", "color_space"},
		{"-color_primaries", "color_primaries"}, {"-color_trc", "color_transfer"}} {
		if s, ok := get(v, kv[1]).(string); ok && s != "" && s != "unknown" {
			out = append(out, kv[0], s)
		}
	}
	if tb, ok := new(big.Rat).SetString(jsonx.Str(get(v, "time_base"))); ok && tb.Num().IsInt64() && tb.Num().Int64() == 1 {
		out = append(out, "-video_track_timescale", tb.Denom().String())
	}
	out = append(out, "-c:a", "aac", "-b:a", jsonx.Str(get(a, "bit_rate")),
		"-ar", jsonx.Str(get(a, "sample_rate")), "-ac", jsonx.Str(get(a, "channels")))
	return out, nil
}

func get(o *jsonx.Object, k string) jsonx.Value {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

func intOf(v jsonx.Value) int64 {
	if n, ok := v.(jsonx.Number); ok {
		return int64(n.Float())
	}
	return 0
}
