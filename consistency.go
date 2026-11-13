package main

import "strings"

// consistentSet ranks sets independently for each artwork category.
// MostComplete tries complete sets first; CreatorPriority ranks creators first.
// Completeness is relative to the
// unique scopes present in Mediux's returned artwork, not duplicate images.
func (p *provider) consistentSet(b bundle, cfg settings, kind, language string) string {
	priority, excluded := splitSet(cfg.Priority), splitSet(cfg.Excluded)
	expected := map[[2]int]bool{}
	for _, s := range b.Sets {
		for scope := range consistencyScopes(s, kind) {
			expected[scope] = true
		}
	}
	type score struct {
		id                       string
		rank, coverage, language int
		globalRank               mediuxRank
		complete                 bool
	}
	creatorFirst := kind == "season" && cfg.SeasonConsistency == "CreatorPriority" ||
		kind == "still" && cfg.TitlecardConsistency == "CreatorPriority"
	var best *score
	for _, s := range b.Sets {
		creator := strings.ToLower(s.Creator)
		rank, preferred := priority[creator]
		if _, blocked := excluded[creator]; blocked || cfg.OnlyPriority && !preferred {
			continue
		}
		if !preferred {
			rank = 99999999
		}
		scopes := consistencyScopes(s, kind)
		tier := 99
		for _, a := range s.Art {
			if a.Kind != kind || !assetID.MatchString(a.ID) || a.Season < 0 || kind == "still" && a.Episode <= 0 {
				continue
			}
			if t := consistencyLanguageTier(a.Language, language, kind); t < tier {
				tier = t
			}
		}
		if len(scopes) == 0 || s.ID == "" {
			continue
		}
		candidate := score{s.ID, rank, len(scopes), tier, s.GlobalRank, len(scopes) == len(expected)}
		if best == nil || !creatorFirst && candidate.complete && !best.complete || (creatorFirst || candidate.complete == best.complete) && (candidate.rank < best.rank ||
			candidate.rank == best.rank && (candidate.coverage > best.coverage ||
				candidate.coverage == best.coverage && (candidate.language < best.language ||
					candidate.language == best.language && (candidate.globalRank.before(best.globalRank) ||
						!best.globalRank.before(candidate.globalRank) && candidate.id < best.id)))) {
			best = &candidate
		}
	}
	if best == nil {
		return ""
	}
	return best.id
}

func consistencyScopes(s set, kind string) map[[2]int]bool {
	scopes := map[[2]int]bool{}
	for _, a := range s.Art {
		if a.Kind != kind || !assetID.MatchString(a.ID) || a.Season < 0 || kind == "still" && a.Episode <= 0 {
			continue
		}
		episode := 0
		if kind == "still" {
			episode = a.Episode
		}
		scopes[[2]int{a.Season, episode}] = true
	}
	return scopes
}

func consistencyLanguageTier(language, requested, kind string) int {
	language, requested = normalizeLanguage(language), normalizeLanguage(requested)
	if kind == "still" {
		if language == "" {
			return 0
		}
		return 1
	}
	if requested != "" && language == requested {
		return 0
	}
	if language == "en" {
		return 1
	}
	if language != "" {
		return 2
	}
	return 3
}
