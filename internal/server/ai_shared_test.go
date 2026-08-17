package server

import kbai "github.com/RandomCodeSpace/kb/internal/ai"

func validateDraft(d storyDraft) error           { return kbai.ValidateDraft(d) }
func coerceDraftMap(m map[string]any) storyDraft { return kbai.CoerceDraft(m) }
func clampPrio(p int) int                        { return kbai.ClampPriority(p) }
func normalizeStoryCount(max int) int            { return kbai.NormalizeStoryCount(max) }
