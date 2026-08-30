package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/RandomCodeSpace/rig"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
)

const (
	skillRunDeadline           = 4 * time.Minute
	skillWriteDeadline         = 5 * time.Minute
	skillMaxIterations         = kbai.SkillMaxIterations
	runnerSystemPrompt         = kbai.RunnerSystemPrompt
	unknownSkillMessage        = kbai.UnknownSkillMessage
	skillsUnavailableMessage   = kbai.SkillsUnavailableMessage
	skillIterationLimitMessage = kbai.SkillIterationLimitMessage
	cardLimitReachedMessage    = kbai.CardLimitReachedMessage
)

type skillRunResult = kbai.RunResult
type skillScope = kbai.Scope
type skill = kbai.Skill

const (
	skillScopeReadOnly = kbai.ScopeReadOnly
	skillScopeFull     = kbai.ScopeFull
)

func (s *server) aiRunner() *kbai.Runner {
	return kbai.NewRunner(s.store, s.cfg.SkillsDir, s.aiClient, s.linkClient)
}

func serverAIError(err error) error {
	var shared *kbai.Error
	if errors.As(err, &shared) {
		return &aiError{shared.Code, shared.Message}
	}
	return err
}

func (s *server) runSkill(ctx context.Context, user string, scope skillScope, skillName, input string, maxCards int, maxTokens int64) (skillRunResult, error) {
	result, err := s.aiRunner().RunSkill(ctx, user, scope, skillName, input, maxCards, maxTokens)
	return result, serverAIError(err)
}

func (s *server) rigClient(cfg aiConfig) (*rig.Client, error) {
	client, err := s.aiRunner().Client(kbai.Config{BaseURL: cfg.baseURL, Model: cfg.model, Key: cfg.key})
	return client, serverAIError(err)
}

func skillBudget(maxTokens int64) int64 { return kbai.SkillBudget(maxTokens) }

func splitSkills(skills []skill, name string) (skill, []skill, bool) {
	return kbai.SplitSkills(skills, name)
}

func runnerSystem(selected skill, others []skill) string {
	return kbai.RunnerSystem(selected, others)
}

func (s *server) loadSkills() ([]skill, error) { return s.aiRunner().LoadSkills() }

func (s *server) runSkillForRequest(w http.ResponseWriter, r *http.Request, user string, scope skillScope, skillName, input string, maxCards int, maxTokens int64) (skillRunResult, error) {
	extendWriteDeadline(w)
	ctx, cancel := context.WithTimeout(r.Context(), skillRunDeadline)
	defer cancel()
	return s.runSkill(ctx, user, scope, skillName, input, maxCards, maxTokens)
}

func extendWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(skillWriteDeadline))
}

type aiRunSkillRequest struct {
	Skill string `json:"skill"`
	Input string `json:"input"`
	Max   int    `json:"max"`
}

func (s *server) handleAIRunSkill(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req aiRunSkillRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Skill) == "" {
		http.Error(w, "skill required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Input) == "" {
		http.Error(w, "input required", http.StatusBadRequest)
		return
	}
	if len(req.Input) > maxADRBytes {
		http.Error(w, "input too large (max 64 KiB)", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := s.runSkillForRequest(w, r, user, skillScopeFull, req.Skill, req.Input, req.Max, aiStoriesMaxTokens)
	if err != nil {
		writeAIError(w, user, "run-skill", err)
		return
	}
	writeJSON(w, result)
}
