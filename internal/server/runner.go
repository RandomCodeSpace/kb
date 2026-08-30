package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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

func (s *server) runSkill(ctx context.Context, user string, scope kbai.Scope, skillName, input string, maxCards int, maxTokens int64) (kbai.RunResult, error) {
	result, err := s.aiRunner().RunSkill(ctx, user, scope, skillName, input, maxCards, maxTokens)
	return result, serverAIError(err)
}

func (s *server) runSkillForRequest(w http.ResponseWriter, r *http.Request, user string, scope kbai.Scope, skillName, input string, maxCards int, maxTokens int64) (kbai.RunResult, error) {
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
	result, err := s.runSkillForRequest(w, r, user, kbai.ScopeFull, req.Skill, req.Input, req.Max, aiStoriesMaxTokens)
	if err != nil {
		writeAIError(w, user, "run-skill", err)
		return
	}
	writeJSON(w, result)
}
