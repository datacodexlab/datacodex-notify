// SPDX-License-Identifier: GPL-3.0-only

// Package skills embeds the agent skill so the app can install it on request.
package skills

import _ "embed"

// DatacodexNotify is the source of truth of the skill text.
//
//go:embed datacodex-notify/SKILL.md
var DatacodexNotify []byte
