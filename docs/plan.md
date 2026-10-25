# Implementation plan

A Go CLI for frame-accurate cuts, muted speed-ups, previews, verified final renders,
and instructions embedded in the same binary. Runtime dependencies are ffmpeg and ffprobe.

## Packages

- `internal/edit`: edit.json loading, validation, frame snapping, keep ranges and timelines.
- `internal/graph`: video/audio filter graphs and encoder arguments.
- `internal/ffmpeg`: process execution, probing and output parsing.
- `internal/join`: lossless joining of recording parts and work-folder initialization.
- `internal/render`, `internal/verify`: inspection, previews, final rendering and checks.
- `internal/audio`: loudness envelope, pauses and suggested cut boundaries.
- `internal/cli`: command parsing, discovery, help and JSON output.
- `internal/skills`: embedded section discovery, extraction and pointer installation.

## Completed functionality

1. Go/Cobra CLI, embedded skills, doctor, build/install targets and CI.
2. Port of the reference renderer: joins, timeline mapping, frame sheets, proxies,
   silences, previews, final output and verification.
3. Audio inspection, MP3 listening ranges and proposed cuts, individual frames,
   frame sampling, inspection of rendered files, status updates and JSON output.
4. Keep mode for selected excerpts; internal cuts and speed-ups still apply.
5. Six instruction sections, full command/schema reference and agent skill installation.

## Validation

All fixtures are invented. No recording names, real source paths, transcripts or
session notes are needed to run the tests. Video fixtures can be recreated
with `python3 testdata/generate_fixtures.py`. The reference script is pinned under
`testdata/golden`; `make golden` recreates expected output without accessing media.

Golden tests compare graphs, timeline mapping, text output, external command arguments
and generated reports. Synthetic-media end-to-end tests cover joining two recording
parts, previews, final verification, inspection commands, MP3 timing and keep mode.
The keep parity test compares two equivalent invented edit representations.

## Release checks

Run `make test`, `make test-e2e` and `make vet`. Build the binary, run doctor, and check
`skills get core --full`. Install explicitly with `make install` and `skills install`.
A real editing session is separate from synthetic-media validation.

Public repository: https://github.com/timurkhakhalev/video-production-cli
