// Package vpc holds assets embedded into the video-production-cli binary.
package vpc

import "embed"

// SkillData is the skill content served by `video-production-cli skills`.
//
//go:embed all:skill-data
var SkillData embed.FS

// PointerSkill is the small agent entrypoint installed by skills install.
//
//go:embed skills/video-production-cli/SKILL.md
var PointerSkill []byte
