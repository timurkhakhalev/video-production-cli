---
name: core
description: Edit videos with the CLI, from source inspection through review and verified final render.
---

# Video editing

You choose cuts and, when requested, muted speed-ups. Picture and sound are otherwise final.
Read `video-production-cli skills get cutting` before choosing cuts, `audio` before finding
cut points, `speedups` for requested time-lapses, `shorts` for selected excerpts, and `review`
when handing over a preview or applying feedback. `skills get core --full` adds the command
reference and edit.json schema. All examples are made up; never copy their times into a real edit.

Do not add transitions, crossfades, audio overlap, J/L cuts, zooms, loudness processing,
denoising, color changes, scaling, fps changes or speed ramps. Do not run ad-hoc ffmpeg
commands, install transcription tools, edit encode.json, or work around CLI failures.
Use native video/audio understanding. If the CLI cannot do the task, explain the missing feature.

## Working files and time

`init` creates `<name>-edit/edit.json` and `encode.json` next to the source. edit.json is the
single source of truth. Only edit this file; the CLI writes previews, frames and final files.
All edit.json times are source seconds. Proxy and listening ranges print their source offset.
Times in a rendered preview belong to that file: convert with `map` before changing source cuts.
To resume, read edit.json and run `status`; start with the first unapproved chunk and apply `lessons`.

## Workflow

1. Run `doctor`, then `init /path/video.mp4`. Pass recording parts in order to join losslessly:
   `init video-1.mp4 video-2.mp4`. Never join them yourself. Init rejects VFR, nonzero stream
   starts and mismatched recording settings. Report failures to the user.
2. Establish mode from the request, or ask: `step` reviews each chunk; `full` reviews the whole
   edit once. Set `mode` in edit.json. Do not assume user approval or mark chunks approved yourself.
3. For long videos run `chunks suggest`. Move nominal ten-minute boundaries to complete
   thoughts or slide changes (roughly 7–13 min is fine). Adjacent chunks share a boundary;
   cuts and speed-ups must stay within one chunk. Keep mode instead tiles selected fragments.
4. Watch each chunk with `proxy FROM TO`, including 20–30 seconds before it for context.
   Keep proxy ranges near five minutes. For each candidate inspect 5–10 seconds on each side;
   a silent gap alone does not tell you whether a phrase is a restart.
5. Find silent edges with `audio FROM TO --at T`; check pose using `frames --range FROM TO`
   or `frames --at T`. Try the cut without saving it: `listen --try FROM TO`. Then add
   `{"from": 312.48, "to": 315.12, "reason": "false start"}` to `cuts` in source seconds.
   Keep the reason in the video's language. Apply every rule in `lessons`.
6. Run `joins --chunk N`, then `frames --join K` and `listen --join K` for EVERY join.
   Numbers are global, including in keep mode. Inspect sheets and full-resolution pairs;
   video understanding at one frame per second cannot verify the pose at an exact cut.
7. Run `preview --chunk N` in step mode, or `preview` in full mode. Set `chunks set N in_review`
   (or `all`). Share the preview, joins/speed-ups with preview times, and unresolved review_flags.
   Wait for the user's review at this point. Apply feedback through `map T --chunk N` (step)
   or `map T` (full); rebuild and recheck affected joins. Record reusable preferences in `lessons`.
8. On user approval run `chunks set N approved` (or `all`). Continue the next chunk in step mode.
9. When all chunks are approved, run `final`. It renders once from the source, checks frame
   count, durations, source parameters and sampled picture similarity, and writes .verify.json.
   `--force` requires an explicit user request. Report final path, old/new lengths, verification
   and unresolved flags. A failed check remains a failure; report it rather than bypassing it.
