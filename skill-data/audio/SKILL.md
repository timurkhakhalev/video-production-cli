---
name: audio
description: Locate safe cut boundaries from the loudness envelope and pauses, then listen once to verify.
---

# Track first, then listen

Start with context: watch a proxy with 5–10 seconds on either side of a suspected restart.
Run `audio 1219 1222 --at 1220` to see a 20 ms RMS envelope and sample-level pauses.
Use `--step` to change envelope cells, `--noise -32` for the silence threshold and
`--min 0.08` for the shortest pause. Audio ranges are limited to 60 seconds.
`silences FROM TO` is a longer-range ffmpeg silence scan; both commands accept `--file PATH`
for preview/final times. These measurements identify silence, not words or semantic mistakes.

A safe cut starts 80–120 ms after the previous sound and ends 100–150 ms before the next
onset. The output uses 100/120 ms margins. A suggested point is the middle of that safe
window, with a frame-snapped value beside it. Check that snapping still leaves the margins.
If a pause touches a range edge, widen the range: the sound just outside it is unknown.
Do not split a breath. Plosives lose their attack when you cut too close.

Inspect pose with `frames --at T` and `frames --range FROM TO`. For longer spans use `--fps 2`
for sampling, then narrow to every-frame inspection at the actual boundary.
Run `listen --try FROM TO` to hear a proposed cut with other edits applied, without writing
edit.json. It rejects overlaps with existing cuts/speed-ups. Once saved, use `listen --join K`
for the rendered splice and `frames --join K` for picture. Do not search the boundary by
making ten half-second listening clips: use the track to locate it and listening to verify it.

`listen` defaults to MP3, mono 16 kHz at 40 kbps. `--video` produces a 540p MP4.
`listen --range FROM TO` extracts unedited source audio (up to 300 seconds), or audio from
`--file PATH`. Range output prints the source/file offset; join output prints its clip time.
MP3 includes LAME/Xing timing metadata. Gapless decoders remove encoder delay; players that
ignore the metadata add about 69 ms. The text and JSON (`encoder_delay`) disclose this;
do not infer frame-accurate timing from an unknown player's playhead.
