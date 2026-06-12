package config

import "slices"

// Content kinds. These identify the pool partitions and generation paths and
// are referenced by the config, content and app packages alike, so they live in
// this low-level package to keep the dependency graph acyclic.
const (
	KindDrill       = "drill"
	KindWord        = "word"
	KindIdiom       = "idiom"
	KindTip         = "tip"
	KindCollocation = "collocation"
	KindStory       = "story"
)

// Difficulty levels (Change F). The level is injected into generation prompts
// and used to partition the content pool.
const (
	LevelBeginner     = "beginner"
	LevelIntermediate = "intermediate"
	LevelUpperInt     = "upper-intermediate"
	LevelAdvanced     = "advanced"

	DefaultLevel = LevelIntermediate
)

// AllLevels is the ordered set of selectable levels.
var AllLevels = []string{LevelBeginner, LevelIntermediate, LevelUpperInt, LevelAdvanced}

// ConfigurableKinds is the set of content kinds whose pool size the admin can
// override individually via /config.
var ConfigurableKinds = []string{KindDrill, KindWord, KindIdiom, KindCollocation, KindStory, KindTip}

// IsConfigurableKind reports whether kind is one the admin may override.
func IsConfigurableKind(kind string) bool {
	return slices.Contains(ConfigurableKinds, kind)
}
