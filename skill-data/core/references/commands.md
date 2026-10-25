# Command reference

Times accept seconds, m:ss.s or h:mm:ss.s. Ranges accept positional FROM TO or
`--range FROM TO`. Select edit.json by positional .json argument, then `--edit PATH`,
then ./edit.json, then a single ./*-edit/edit.json. Multiple matches require --edit.
Without --chunk, commands use the whole edit. --json returns machine-readable results;
ffmpeg progress stays on stderr. Use command --help for flags and examples.

| Command | Purpose |
|---|---|
| `init SRC [SRC2 ...] [--workdir DIR] [--joined PATH] [--chunk-minutes 10]` | Probe and create work files; join parts losslessly first. Refuses existing edit.json and joined output. |
| `init SRC --keep FROM TO [--keep FROM-TO ...]` | Select keep fragments and build chunks within them. |
| `status` | Chunks, cuts, speed-ups, output length, notes, existing final verification, next step. Existing reports may be stale after edits; rerender after changes. |
| `chunks suggest [--every 600 --window 120]` | Longest pauses near nominal chunk marks; does not move boundaries. |
| `chunks set ID\|all draft\|in_review\|approved` | Validate and atomically save status, preserving unknown fields. |
| `audio FROM TO [--at T --step 0.02 --noise -32 --min 0.08]` | RMS envelope and pauses, nearest safe point. At most 60 seconds. |
| `silences FROM TO [--noise -32 --min 0.08]` | Silence intervals in source/file seconds. |
| `listen --join K [--span 3]` | Exact splice with context; MP3 by default. |
| `listen --try FROM TO [--span 3]` | Preview an additional non-overlapping cut, without saving. |
| `listen --range FROM TO` | Unedited source range, at most 300 seconds. |
| `listen ... --video` | MP4 with 540p picture instead of mono 16 kHz 40 kbps MP3. |
| `frames --join K [--span 0.5]` | Before/after contact sheet plus full-resolution last/first pair. |
| `frames --range FROM TO [--fps N]` | Contact sheet, at most 120 frames; default every frame. Sampling helps inspect long ranges. |
| `frames --at T [T ...]` | Full-resolution individual frames. |
| `proxy FROM TO` | 540p source clip; prints offset and warns above 90 MB. |
| `joins [--chunk N]` | Global join numbers, source/output times, reasons, speed-ups, tiny-fragment warnings. |
| `map T [--chunk N]` | Preview/final time to source time, including speed-ups. |
| `preview [--chunk N]` | Fast render and duration/frame verification. |
| `final [--force]` | One render from source and verification report. Force only on explicit user request. |
| `verify [FILE] [--chunk N \| --final]` | Recheck an existing preview/final. Defaults to matching output. |
| `skills list` | List embedded sections. |
| `skills get NAME [--full]` | Read instructions; full adds references. |
| `skills path [NAME]` | Extract embedded sections to versioned user cache. |
| `skills install [--agent claude\|gemini\|agy2\|codex\|all]` | Install the pointer skill into agent skill directories; default all. |
| `doctor` | Check ffmpeg/ffprobe 7+ and required filters. |

`audio`, `silences`, `frames` and `listen --range` accept `--file PATH` to inspect a rendered
file without edit.json. Times then belong to that file. Join and try require edit.json.
MP3 gapless metadata preserves clip times; decoders ignoring it add about 69 ms.

Final uses libx264 slow CRF 15 and source profile, pixel format, level, color tags, track
clock; AAC uses source rate/channels and at least source bitrate. Only H264/AAC sources
are supported for final rendering. Cuts snap to source frames; each splice gets a 5 ms
non-overlapping fade. Muted speed-ups select every Nth frame and keep source frame rate.
Verification checks durations, frame count, A/V agreement and final stream parameters,
plus PSNR on sampled 1x pieces. It cannot replace visual/audio review of every join.
