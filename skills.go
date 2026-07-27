package mergeableplease

import "embed"

// SkillsFS embeds the agent skills shipped with the binary. The binary
// (cmd/mergeable-please) consumes this via skillsmith. The embed directive
// must live in the root library package because go:embed cannot reference
// parent directories (../skills would be rejected from a sub-directory).
//
//go:embed skills
var SkillsFS embed.FS
