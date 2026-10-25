# video-production-cli

Go CLI for editing screencasts, lessons and shorts from `edit.json`, with instructions
bundled into the binary for AI agents. Requires Go (see go.mod) to build and ffmpeg/ffprobe 7+
to run. Final rendering supports H264 video with AAC audio.

```sh
make build
bin/video-production-cli doctor
bin/video-production-cli skills get core --full
bin/video-production-cli init /path/to/lesson.mp4
cd /path/to/lesson-edit
/path/to/video-production-cli/bin/video-production-cli status
```

Set the review mode in edit.json, inspect source ranges with `proxy`, locate silent cut points
with `audio`, and check cuts with `frames` and `listen`. Preview for user review before marking
chunks approved; `final` renders from source and verifies the result. MP3 listening clips are
mono 16 kHz at 40 kbps, with encoder-delay metadata and a warning for decoders that ignore it.

For shorts, `init talk.mp4 --keep 100 150 --keep 200-220 --workdir short-edit` selects source
fragments; all other footage is cut automatically. Internal cuts and speed-ups still apply.

Each command has `--help` and `--json`. By default edit.json is discovered in the current
folder or a single child `*-edit` folder; `--edit PATH` selects it explicitly.

```sh
make test          # Golden cases against the pinned render.py plus unit/CLI tests
make test-e2e      # synthetic media, lossless part join, previews/finals, inspection and keep mode
make vet
```

All test fixtures are invented; no personal recording metadata or media is included.
`python3 testdata/generate_fixtures.py` recreates the synthetic lesson/short fixtures.
`make golden` regenerates reference outputs with Python 3.9+ (only a test dependency).

`make install` installs the binary into ~/.local/bin. `video-production-cli skills install`
installs the pointer skill for Claude, Gemini, agy2 and Codex; `--agent NAME` selects one.
These installation commands are explicit actions, not part of build or test.

The implementation plan and rollout boundary are in [docs/plan.md](docs/plan.md).
