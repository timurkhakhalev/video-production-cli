---
name: shorts
description: Select excerpts from a long recording with keep mode and preserve internal cuts and review.
---

# Shorts with keep mode

Read the requested brief: topics, length and number of shorts. Work on each short in its own
edit folder/session so feedback and cuts cannot drift between outputs. Keep complete thoughts;
do not invent words, reorder meaning or add effects. Ask for missing editorial choices.

Always use keep mode for a short or excerpt, even if earlier shorts of the same talk were cut
without it: do not reproduce whole-source chunks with "outside the short" cuts. If a work folder
for this short already exists without keep, init a new one with `--workdir`.
Start with `init talk.mp4 --workdir excerpt-edit --keep 1:40 2:20 --keep 10:50 11:10`.
Repeated `--keep FROM-TO` also works. These are source ranges, sorted and non-overlapping.
Everything outside `keep` is removed automatically; never add redundant outside cuts.
Chunks tile each keep fragment without gaps within that fragment. Large fragments can have
several chunks, but one chunk cannot span the gap between fragments.

Internal `cuts` and `speedups` work as for other videos and must fit inside keep fragments and chunks.
To shorten a selected outer edge, trim keep and its chunk boundary together. Use `status`,
`joins`, `frames --join K` and `listen --join K` to check internal and between-fragment joins.
Review and approval still apply; keep mode does not imply approval or bypass final verification.

Shorts need a tight pace. Before the first preview run `silences FROM TO --min 0.8` over every
keep fragment and shorten each long pause (hesitation, slide change, looking at notes) to about
0.3–0.4 s with a cut inside the pause, unless the brief says otherwise. Check those joins like
any other: `audio` for edges, `listen --try`, then `frames --join K` and `listen --join K`.
